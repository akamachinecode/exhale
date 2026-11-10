package importer

import (
	"bytes"
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/merchant"
)

// A bank or card export lists transactions, not subscriptions. The charges
// that recur are found without a model: transactions are grouped by a
// normalised merchant, and a group is a subscription when its dates are a
// regular cycle. It is deterministic, works with the assistant off and
// sends nothing anywhere.

// FileKind is what an upload looks like from its first bytes.
type FileKind int

// Kinds of upload.
const (
	FileUnknown FileKind = iota
	FileSQLite
	FileJSON
	FilePDF
	FileImage
	FileText
)

// Sniff says what data is and, for pictures and PDFs, its media type.
func Sniff(data []byte) (FileKind, string) {
	switch {
	case bytes.HasPrefix(data, []byte("SQLite format 3\x00")):
		return FileSQLite, ""
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return FilePDF, "application/pdf"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return FileImage, "image/png"
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return FileImage, "image/jpeg"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return FileImage, "image/webp"
	}
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if len(trimmed) == 0 || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return FileUnknown, ""
	}
	if trimmed[0] == '{' {
		return FileJSON, ""
	}
	return FileText, ""
}

// Column names banks use. The first match in each list wins.
var (
	txDate     = []string{"completed date", "booking date", "transaction date", "date", "fecha", "fecha operación", "fecha operacion", "datum", "buchungstag", "started date", "posted date", "value date", "fecha valor"}
	txAmount   = []string{"amount", "importe", "betrag", "value", "transaction amount", "amount (eur)", "amount (usd)", "amount (gbp)"}
	txDebit    = []string{"debit", "paid out", "money out", "withdrawal", "withdrawals", "cargo", "débito", "debito", "debit amount"}
	txText     = []string{"merchant", "payee", "partner name", "counterparty", "name", "description", "concepto", "concept", "details", "transaction description", "narrative", "memo", "beneficiary", "beneficiario", "descripción", "descripcion", "text", "verwendungszweck", "payment reference", "reference"} //nolint:misspell // Spanish
	txCurrency = []string{"currency", "moneda", "divisa", "ccy", "amount currency"}
	txState    = []string{"state", "status", "estado"}
	// txOnly are columns only a bank export has.
	txOnly = []string{"balance", "saldo", "type", "tipo", "transaction id", "time", "fee", "iban", "partner iban", "started date", "completed date", "booking date", "value date", "fecha valor", "reference", "movimiento", "original amount", "local amount", "account", "product", "disponible", "mcc"}
	// subOnly are columns only a list of subscriptions has.
	subOnly = []string{"cycle", "payment cycle", "billing cycle", "frequency", "period", "billing", "interval", "every", "unit", "next payment", "next charge", "next", "next billing", "renewal date", "next date", "due date", "price", "cost", "precio", "coste", "ciclo", "periodo", "frecuencia", "próximo pago", "proximo pago", "payment method"}
)

type txn struct {
	date     time.Time
	text     string
	key      string
	minor    int64 // positive: money that left the account
	currency string
}

// MaxTransactions bounds a bank export.
const MaxTransactions = 50_000

// Transactions reads a bank or card export and returns the charges that
// recur, and every outgoing payment for matching with the expected charges.
// shaped is false when data is not a list of transactions at all; when it
// is, the result may still have no items (nothing recurs).
func Transactions(data []byte, defaultCurrency string, today engine.Date) (res Result, shaped bool) {
	all, shaped := csvTransactions(data, defaultCurrency)
	if !shaped {
		return Result{}, false
	}
	return detect(all, today, "transactions"), true
}

// csvTransactions reads the rows of a bank export. The amounts it returns
// are positive for money that left the account.
func csvTransactions(data []byte, defaultCurrency string) ([]txn, bool) {
	rows, err := readCSV(data)
	if err != nil || len(rows) < 3 || len(rows) > MaxTransactions {
		return nil, false
	}
	cols := map[string]int{}
	for i, h := range rows[0] {
		h = strings.ToLower(strings.TrimSpace(h))
		if _, dup := cols[h]; !dup && h != "" {
			cols[h] = i
		}
	}
	find := func(names []string) int {
		for _, n := range names {
			if i, ok := cols[n]; ok {
				return i
			}
		}
		return -1
	}
	if find(subOnly) >= 0 {
		return nil, false
	}
	dateCol, amountCol, debitCol, textCol := find(txDate), find(txAmount), find(txDebit), find(txText)
	if dateCol < 0 || textCol < 0 || (amountCol < 0 && debitCol < 0) {
		return nil, false
	}
	curCol, stateCol := find(txCurrency), find(txState)
	headerCurrency := ""
	if amountCol >= 0 {
		if m := currencyInHeader.FindStringSubmatch(strings.ToUpper(rows[0][amountCol])); m != nil {
			headerCurrency = m[1]
		}
	}
	cell := func(row []string, i int) string {
		if i >= 0 && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	var all []txn
	spent, received := 0, 0
	for _, row := range rows[1:] {
		switch strings.ToLower(cell(row, stateCol)) {
		case "reverted", "failed", "declined", "cancelled", "canceled", "pending":
			continue
		}
		d, err := time.Parse(time.DateOnly, AnyDate(firstField(cell(row, dateCol))))
		if err != nil {
			continue
		}
		t := txn{date: d, text: cell(row, textCol), currency: cmp.Or(strings.ToUpper(cell(row, curCol)), headerCurrency, defaultCurrency)}
		// With a debit column, only what is in it left the account. With one
		// signed column the sign says it; which sign means "spent" is decided
		// below, once every row is read.
		amount := cell(row, amountCol)
		if debitCol >= 0 {
			amount = cell(row, debitCol)
		}
		minor, cur, ok := parseAmount(amount)
		if !ok || t.text == "" || minor == 0 {
			continue
		}
		if debitCol >= 0 {
			minor = -abs(minor)
		}
		if minor < 0 {
			spent++
		} else {
			received++
		}
		t.minor, t.currency = -minor, cmp.Or(cur, t.currency)
		if t.key = merchant.Key(t.text); t.key != "" {
			all = append(all, t)
		}
	}
	flip(all, spent, received)
	repeated := false
	seen := map[string]bool{}
	for _, t := range all {
		k := t.key + "|" + t.currency
		repeated = repeated || (t.minor > 0 && seen[k])
		seen[k] = seen[k] || t.minor > 0
	}
	if find(txOnly) < 0 && spent == 0 && !repeated {
		return nil, false // more likely a list of subscriptions with a date
	}
	return all, true
}

// flip makes the money that left positive. A bank account export is mostly
// negative amounts (spending); a card statement is mostly positive ones
// (charges), with refunds negative. The majority sign is the money that
// left. Amounts arrive negated (spent is positive when it was negative).
func flip(all []txn, spent, received int) {
	if received > spent {
		for i := range all {
			all[i].minor = -all[i].minor
		}
	}
}

// detect finds the recurring charges among the transactions, and keeps the
// outgoing payments for matching.
func detect(all []txn, today engine.Date, source string) Result {
	groups := map[string][]txn{}
	res := Result{Source: source, Items: []app.ImportItem{}, Notes: []string{}, Transactions: []app.StatementLine{}}
	for _, t := range all {
		if t.minor > 0 {
			groups[t.key+"|"+t.currency] = append(groups[t.key+"|"+t.currency], t)
			res.Transactions = append(res.Transactions, app.StatementLine{Date: t.date.Format(time.DateOnly), Description: truncate(t.text, 200), Amount: decimal(t.minor), Currency: t.currency})
		}
	}
	for _, g := range groups {
		if it, ok := recurring(g, today); ok {
			res.Items = append(res.Items, it)
		}
	}
	slices.SortFunc(res.Items, func(a, b app.ImportItem) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	slices.SortStableFunc(res.Transactions, func(a, b app.StatementLine) int { return cmp.Compare(a.Date, b.Date) })
	if len(res.Items) > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("Found by looking for charges that repeat on a regular cycle in %d transactions. Check each line: a bank's description is not always the name of the service.", len(all)))
	}
	return res
}

var currencyInHeader = regexp.MustCompile(`\(([A-Z]{3})\)`)

// firstField drops the time of "2026-10-06 09:12:44".
func firstField(s string) string {
	if AnyDate(s) != "" {
		return s
	}
	f, _, _ := strings.Cut(s, " ")
	if AnyDate(f) != "" {
		return f
	}
	f, _, _ = strings.Cut(s, "T")
	return f
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

var amountRe = regexp.MustCompile(`^([^\d(+-]*)([(+-]?)\s*([\d.,'\s]*\d)\s*\)?\s*([^\d]*)$`)

// parseAmount reads "-12.99", "1.234,56", "(12.99)", "€12,99" or
// "12.99 EUR" into minor units (two decimals) and a currency, if written.
func parseAmount(s string) (minor int64, currency string, ok bool) {
	s = strings.NewReplacer("−", "-", " ", " ").Replace(strings.TrimSpace(s))
	m := amountRe.FindStringSubmatch(s)
	if m == nil {
		return 0, "", false
	}
	digits := strings.NewReplacer(" ", "", "'", "").Replace(m[3])
	// The last separator is the decimal one when one or two digits follow.
	whole, frac := digits, ""
	if i := strings.LastIndexAny(digits, ".,"); i >= 0 && len(digits)-i-1 <= 2 {
		whole, frac = digits[:i], digits[i+1:]
	}
	whole = strings.NewReplacer(".", "", ",", "").Replace(whole)
	frac = (frac + "00")[:2]
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0, "", false
	}
	if m[2] == "-" || m[2] == "(" {
		n = -n
	}
	for _, c := range []string{strings.TrimSpace(m[1]), strings.TrimSpace(m[4])} {
		if code := symbols[c]; code != "" {
			currency = code
		} else if len(c) == 3 && strings.ToUpper(c) == c && c != "" {
			currency = c
		}
	}
	return n, currency, true
}

// cycles are the billing cycles a group of dates can follow, as a range of
// days between charges (wide enough for weekends and short months).
var cycles = []struct {
	min, max int
	interval int
	unit     engine.Unit
	label    string
}{
	{6, 8, 1, engine.Week, "every week"},
	{13, 15, 2, engine.Week, "every 2 weeks"},
	{26, 35, 1, engine.Month, "every month"},
	{56, 66, 2, engine.Month, "every 2 months"},
	{85, 97, 3, engine.Month, "every 3 months"},
	{175, 190, 6, engine.Month, "every 6 months"},
	{355, 375, 1, engine.Year, "every year"},
}

// recurring decides whether the transactions of one merchant are a
// subscription, and describes it.
func recurring(g []txn, today engine.Date) (app.ImportItem, bool) {
	slices.SortFunc(g, func(a, b txn) int { return a.date.Compare(b.date) })
	// Two charges on the same day count once (a retry, or two lines for one
	// purchase).
	days := g[:1]
	for _, t := range g[1:] {
		if !t.date.Equal(days[len(days)-1].date) {
			days = append(days, t)
		}
	}
	if len(days) < 2 {
		return app.ImportItem{}, false
	}
	gaps := make([]int, 0, len(days)-1)
	for i := 1; i < len(days); i++ {
		gaps = append(gaps, int(days[i].date.Sub(days[i-1].date).Hours()/24+0.5))
	}
	sorted := slices.Clone(gaps)
	slices.Sort(sorted)
	median := sorted[len(sorted)/2]
	for _, c := range cycles {
		if median < c.min || median > c.max {
			continue
		}
		fit := 0
		for _, gap := range gaps {
			if gap >= c.min && gap <= c.max {
				fit++
			}
		}
		// Every gap must fit, or three in four when there are enough of
		// them to forgive a skipped month.
		if fit < len(gaps) && (len(gaps) < 4 || fit*4 < len(gaps)*3) {
			return app.ImportItem{}, false
		}
		lo, hi := days[0].minor, days[0].minor
		for _, t := range days {
			lo, hi = min(lo, t.minor), max(hi, t.minor)
		}
		last := days[len(days)-1]
		steady := (hi-lo)*4 <= last.minor // within a quarter of the last amount
		switch {
		case c.unit == engine.Week && (len(days) < 3 || !steady):
			return app.ImportItem{}, false // a weekly shop, not a subscription
		case len(days) == 2 && (hi-lo)*10 > last.minor:
			return app.ImportItem{}, false // two different amounts prove nothing
		}
		it := app.ImportItem{
			Name: merchant.Name(last.key), Price: decimal(last.minor), Currency: last.currency, Interval: c.interval, Unit: string(c.unit),
		}
		lastDay := engine.DateOf(last.date)
		if sched, err := engine.NewSchedule(engine.Rule{Interval: c.interval, Unit: c.unit, Anchor: lastDay}); err == nil {
			from := today
			if !lastDay.Before(today) {
				from = lastDay.AddDays(1)
			}
			if next, ok := sched.NextOnOrAfter(from); ok {
				it.NextCharge = next.String()
			}
		}
		remark := fmt.Sprintf("%d charges %s, last on %s", len(days), c.label, lastDay)
		if lo != hi {
			if steady {
				remark += fmt.Sprintf("; the amount went from %s to %s", decimal(days[0].minor), decimal(last.minor))
			} else {
				remark += fmt.Sprintf("; the amount varies between %s and %s: the last one is used", decimal(lo), decimal(hi))
			}
		}
		if lastDay.DaysUntil(today) > c.max*3/2 {
			remark += "; not charged since: it may have ended"
		}
		it.Remark = remark + " · “" + truncate(last.text, 60) + "”"
		return it, true
	}
	return app.ImportItem{}, false
}

func decimal(minor int64) string {
	return fmt.Sprintf("%d.%02d", minor/100, minor%100)
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
