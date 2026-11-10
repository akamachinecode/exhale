// Package importer turns files from other tools into Exhale import items:
// a Wallos database or export, a spreadsheet saved as CSV, or Exhale's own
// export. It only reads; the app decides what to create (the user reviews
// the list first).
package importer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the Wallos database is a SQLite file

	"github.com/akamachinecode/exhale/internal/app"
)

// ErrUnknownFormat is returned when a file is none of the supported kinds.
var ErrUnknownFormat = errors.New("this file is not a Wallos database, a Wallos export, an Exhale export or a CSV with a name and a price column")

// Result is what a file contained.
type Result struct {
	Source string           `json:"source" jsonschema:"what the file was: wallos-db, wallos-export, exhale-export, csv, transactions (a bank export), statement (a PDF), picture or text"`
	Items  []app.ImportItem `json:"items"`
	Notes  []string         `json:"notes" jsonschema:"things the reviewer should know about the file as a whole"`
	// Model is the model that proposed or tidied the items, when one did.
	Model string `json:"model,omitempty" jsonschema:"the model that read the file or tidied the names; empty when Exhale read it by itself"`
	// Transactions are the outgoing payments of a statement, for matching
	// with the expected charges. They stay on the server.
	Transactions []app.StatementLine `json:"-"`
	// Match says which expected charges the statement confirms, when it
	// listed transactions.
	Match *app.StatementMatch `json:"match,omitempty"`
}

// MaxFileSize bounds an uploaded file.
const MaxFileSize = 20 << 20

// Parse detects the format of data and returns its subscriptions.
// defaultCurrency fills items whose currency the file does not say.
func Parse(ctx context.Context, data []byte, defaultCurrency string) (Result, error) {
	switch {
	case bytes.HasPrefix(data, []byte("SQLite format 3\x00")):
		return wallosDB(ctx, data)
	case len(bytes.TrimSpace(data)) > 0 && bytes.TrimSpace(data)[0] == '{':
		return fromJSON(data, defaultCurrency)
	}
	return fromCSV(data, defaultCurrency)
}

// --- Wallos database -------------------------------------------------------------

func wallosDB(ctx context.Context, data []byte) (Result, error) {
	f, err := os.CreateTemp("", "exhale-import-*.db")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Result{}, err
	}
	conn, err := sql.Open("sqlite", "file:"+f.Name()+"?mode=ro&immutable=1")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = conn.Close() }()
	var n int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('subscriptions', 'currencies', 'cycles')`).Scan(&n); err != nil || n != 3 {
		return Result{}, fmt.Errorf("%w (it is a SQLite file, but not a Wallos one)", ErrUnknownFormat)
	}
	lookup := func(query string) map[int64]string {
		m := map[int64]string{}
		rows, err := conn.QueryContext(ctx, query)
		if err != nil {
			return m // older Wallos versions lack some tables
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id int64
			var name sql.NullString
			if rows.Scan(&id, &name) == nil {
				m[id] = html.UnescapeString(name.String)
			}
		}
		return m
	}
	currencies := lookup(`SELECT id, code FROM currencies`)
	categories := lookup(`SELECT id, name FROM categories`)
	methods := lookup(`SELECT id, name FROM payment_methods`)
	defaultDays := 1
	_ = conn.QueryRowContext(ctx, `SELECT days FROM notification_settings LIMIT 1`).Scan(&defaultDays)

	cols := map[string]bool{}
	if rows, err := conn.QueryContext(ctx, `SELECT name FROM pragma_table_info('subscriptions')`); err == nil {
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil {
				cols[c] = true
			}
		}
		_ = rows.Close()
	}
	col := func(name, fallback string) string {
		if cols[name] {
			return name
		}
		return fallback + " AS " + name
	}
	// Column names come from the fixed list below, never from the file.
	query := "SELECT name, price, currency_id, next_payment, cycle, frequency, " + //nolint:gosec // fixed identifiers only
		strings.Join([]string{col("notes", "''"), col("payment_method_id", "NULL"), col("category_id", "NULL"), col("notify", "1"),
			col("url", "''"), col("inactive", "0"), col("notify_days_before", "-1"), col("start_date", "NULL"), col("auto_renew", "1")}, ", ") +
		" FROM subscriptions ORDER BY id"
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return Result{}, fmt.Errorf("reading the Wallos subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := Result{Source: "wallos-db", Items: []app.ImportItem{}, Notes: []string{}}
	for rows.Next() {
		var name, next, notes, url, start sql.NullString
		var price float64
		var currency, cycle, frequency, method, category, notify, inactive, days, autoRenew sql.NullInt64
		if err := rows.Scan(&name, &price, &currency, &next, &cycle, &frequency, &notes, &method, &category, &notify, &url, &inactive, &days, &start, &autoRenew); err != nil {
			return Result{}, fmt.Errorf("reading a Wallos subscription: %w", err)
		}
		it := app.ImportItem{
			Name: html.UnescapeString(name.String), Price: strconv.FormatFloat(price, 'f', 2, 64), Currency: currencies[currency.Int64],
			Interval: int(max(frequency.Int64, 1)), Notes: html.UnescapeString(notes.String), URL: url.String,
			Category: categories[category.Int64], PaymentMethod: methods[method.Int64], StartedOn: dateOnly(start.String),
		}
		if it.Category == "No category" {
			it.Category = ""
		}
		switch cycle.Int64 {
		case 1:
			it.Unit = "day"
		case 2:
			it.Unit = "week"
		case 3:
			it.Unit = "month"
		case 4:
			it.Unit = "year"
		default:
			it.Remark = "one-time payment in Wallos: imported as ended"
			it.Unit, it.Status = "year", "cancelled"
		}
		switch {
		case notify.Int64 == 0:
			it.ReminderDays = []int{}
		case days.Int64 < 0:
			it.ReminderDays = []int{defaultDays, 0}
		case days.Int64 == 0:
			it.ReminderDays = []int{0}
		default:
			it.ReminderDays = []int{int(days.Int64), 0}
		}
		if autoRenew.Valid && autoRenew.Int64 == 0 {
			it.Renewal = "manual"
		}
		nextDate := dateOnly(next.String)
		if inactive.Int64 != 0 || it.Status == "cancelled" {
			// Ended: it ran from its start to the day before the payment that
			// never happened.
			it.Status = "cancelled"
			it.NextCharge = it.StartedOn
			if it.NextCharge == "" {
				it.NextCharge = nextDate
			}
			if d, err := time.Parse(time.DateOnly, nextDate); err == nil && nextDate > it.NextCharge {
				it.EndDate = d.AddDate(0, 0, -1).Format(time.DateOnly)
			}
			if it.Remark == "" {
				it.Remark = "inactive in Wallos: imported as cancelled"
			}
		} else {
			it.NextCharge = nextDate
		}
		out.Items = append(out.Items, it)
	}
	out.Notes = append(out.Notes, "Logos and notification settings are not imported.")
	return out, rows.Err()
}

func dateOnly(s string) string {
	if len(s) >= 10 {
		if _, err := time.Parse(time.DateOnly, s[:10]); err == nil {
			return s[:10]
		}
	}
	return ""
}

// --- JSON: Wallos export or Exhale export ---------------------------------------

var symbols = map[string]string{"€": "EUR", "$": "USD", "£": "GBP", "¥": "JPY", "₹": "INR", "R$": "BRL", "zł": "PLN", "₺": "TRY", "₩": "KRW", "₽": "RUB", "Fr": "CHF", "kr": "SEK", "Kč": "CZK", "Ft": "HUF", "₪": "ILS"}

var priceRe = regexp.MustCompile(`^\s*([^\d\s.,-]*)\s*(-?[\d.,]+)\s*([^\d\s.,-]*)\s*$`)

// SplitPrice separates "€12.99", "12,99 EUR" or "$5" into amount and currency.
func SplitPrice(s, fallback string) (amount, currency string) {
	m := priceRe.FindStringSubmatch(s)
	if m == nil {
		return strings.TrimSpace(s), fallback
	}
	amount = m[2]
	if strings.Count(amount, ",") == 1 && !strings.Contains(amount, ".") {
		amount = strings.Replace(amount, ",", ".", 1) // 12,99 → 12.99
	}
	amount = strings.ReplaceAll(amount, ",", "")
	mark := strings.TrimSpace(m[1] + m[3])
	switch {
	case mark == "":
		return amount, fallback
	case symbols[mark] != "":
		return amount, symbols[mark]
	case len(mark) == 3:
		return amount, strings.ToUpper(mark)
	}
	return amount, fallback
}

var everyRe = regexp.MustCompile(`(?i)^every\s+(\d+)\s+(day|week|month|year)s?$`)

// cadence reads "Monthly", "yearly", "Every 3 Months", "weekly"…
func cadence(s string) (interval int, unit string, ok bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if m := everyRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return max(n, 1), strings.ToLower(m[2]), true
	}
	switch s {
	case "daily", "day", "days", "diario", "diaria":
		return 1, "day", true
	case "weekly", "week", "weeks", "semanal":
		return 1, "week", true
	case "biweekly", "fortnightly":
		return 2, "week", true
	case "monthly", "month", "months", "mensual", "":
		return 1, "month", s != ""
	case "quarterly", "trimestral":
		return 3, "month", true
	case "semiannual", "semi-annual", "biannual", "half-yearly":
		return 6, "month", true
	case "yearly", "annual", "annually", "year", "years", "anual": //nolint:misspell // Spanish
		return 1, "year", true
	}
	return 1, "month", false
}

func fromJSON(data []byte, defaultCurrency string) (Result, error) {
	var probe struct {
		Format        string            `json:"format"`
		Subscriptions []json.RawMessage `json:"subscriptions"`
	}
	if err := json.Unmarshal(data, &probe); err != nil || len(probe.Subscriptions) == 0 {
		return Result{}, ErrUnknownFormat
	}
	if probe.Format == app.ExportFormat {
		return exhaleExport(data)
	}
	out := Result{Source: "wallos-export", Items: []app.ImportItem{}, Notes: []string{
		"A Wallos export has currency symbols, not codes: check the currency of each item.",
		"It has no start dates. Upload the Wallos database file (db/wallos.db) for a complete import.",
	}}
	for _, raw := range probe.Subscriptions {
		var w map[string]any
		if json.Unmarshal(raw, &w) != nil {
			continue
		}
		str := func(k string) string { v, _ := w[k].(string); return strings.TrimSpace(v) }
		if str("Name") == "" {
			return Result{}, ErrUnknownFormat
		}
		it := app.ImportItem{Name: html.UnescapeString(str("Name")), Notes: html.UnescapeString(str("Notes")), URL: str("URL"),
			Category: first(str("Category")), PaymentMethod: str("Payment Method"), NextCharge: dateOnly(str("Next Payment"))}
		it.Price, it.Currency = SplitPrice(str("Price"), defaultCurrency)
		var ok bool
		if it.Interval, it.Unit, ok = cadence(str("Payment Cycle")); !ok {
			it.Remark = "payment cycle “" + str("Payment Cycle") + "” not understood: set to monthly"
		}
		if it.Category == "No category" {
			it.Category = ""
		}
		if strings.EqualFold(str("Renewal"), "Manual") {
			it.Renewal = "manual"
		}
		if strings.EqualFold(str("Notifications"), "Disabled") {
			it.ReminderDays = []int{}
		}
		if strings.EqualFold(str("Active"), "No") || strings.EqualFold(str("State"), "Disabled") {
			it.Status = "cancelled"
			if d, err := time.Parse(time.DateOnly, it.NextCharge); err == nil {
				it.EndDate = d.AddDate(0, 0, -1).Format(time.DateOnly)
				it.NextCharge = "" // unknown start: use the end as the only known date
				it.StartedOn = it.EndDate
			}
			it.Remark = "inactive in Wallos: imported as cancelled"
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

func first(list string) string {
	s, _, _ := strings.Cut(list, " / ")
	return strings.TrimSpace(s)
}

func exhaleExport(data []byte) (Result, error) {
	var ex app.ExportData
	if err := json.Unmarshal(data, &ex); err != nil {
		return Result{}, fmt.Errorf("reading the Exhale export: %w", err)
	}
	out := Result{Source: "exhale-export", Items: []app.ImportItem{}, Notes: []string{"Subscriptions are imported with their terms; past charges are not. To move a whole instance, restore a backup instead."}}
	for _, s := range ex.Subscriptions {
		rule := s.Schedule.Versions[len(s.Schedule.Versions)-1].Rule
		it := app.ImportItem{
			Name: s.Name, Price: s.Price.Decimal(), Currency: s.Price.Currency, Interval: rule.Interval, Unit: string(rule.Unit),
			Status: string(s.Status), Category: s.CategoryName, Payer: s.PayerName, URL: s.URL, Notes: s.Notes,
			CancelURL: s.CancelURL, Account: s.Account, Renewal: string(s.Renewal), NoticeDays: s.NoticeDays,
			ReminderDays: s.Reminders.DaysBefore, AutoConfirm: s.AutoConfirm,
		}
		for _, pm := range ex.PaymentMethods {
			if pm.ID == s.PaymentMethodID {
				it.PaymentMethod = pm.Name
			}
		}
		if !s.StartedOn.IsZero() {
			it.StartedOn = s.StartedOn.String()
		}
		if !s.TrialEndsOn.IsZero() {
			it.TrialEndsOn = s.TrialEndsOn.String()
		}
		switch {
		case !s.NextCharge.IsZero():
			it.NextCharge = s.NextCharge.String()
		default:
			it.NextCharge = s.Schedule.First().String()
		}
		if !s.Schedule.Until.IsZero() {
			it.EndDate = s.Schedule.Until.String()
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

// --- CSV -------------------------------------------------------------------------

// headers maps the column names people use to import fields.
var headers = map[string]string{
	"name": "name", "service": "name", "subscription": "name", "title": "name", "merchant": "name", "nombre": "name", "servicio": "name",
	"price": "price", "amount": "price", "cost": "price", "precio": "price", "importe": "price", "coste": "price",
	"currency": "currency", "moneda": "currency", "divisa": "currency",
	"cycle": "cycle", "payment cycle": "cycle", "billing cycle": "cycle", "frequency": "cycle", "period": "cycle", "billing": "cycle", "ciclo": "cycle", "periodo": "cycle", "frecuencia": "cycle",
	"interval": "interval", "every": "interval",
	"unit":         "unit",
	"next payment": "next", "next charge": "next", "next": "next", "next billing": "next", "renewal date": "next", "next date": "next", "due date": "next", "date": "next", "próximo pago": "next", "proximo pago": "next", "fecha": "next",
	"start date": "start", "started": "start", "since": "start", "started on": "start", "inicio": "start",
	"end date": "end", "ended": "end", "cancelled on": "end", "fin": "end",
	"category": "category", "categoría": "category", "categoria": "category",
	"payment method": "method", "method": "method", "card": "method", "paid with": "method", "método de pago": "method", "metodo de pago": "method", "tarjeta": "method",
	"paid by": "payer", "payer": "payer", "owner": "payer", "pagador": "payer",
	"notes": "notes", "note": "notes", "description": "notes", "notas": "notes",
	"url": "url", "website": "url", "link": "url", "web": "url",
	"status": "status", "state": "status", "active": "active", "estado": "status",
	"cancel url": "cancelurl", "cancel link": "cancelurl",
	"account": "account", "email": "account", "cuenta": "account",
	"notice days": "notice", "notice": "notice",
	"trial ends": "trial", "trial end": "trial",
}

// readCSV reads comma, semicolon or tab separated text, whichever the first
// line uses most.
func readCSV(data []byte) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	firstLine, _, _ := bytes.Cut(data, []byte("\n"))
	r := csv.NewReader(bytes.NewReader(data))
	if bytes.Count(firstLine, []byte(";")) > bytes.Count(firstLine, []byte(",")) {
		r.Comma = ';'
	} else if bytes.Count(firstLine, []byte("\t")) > bytes.Count(firstLine, []byte(",")) {
		r.Comma = '\t'
	}
	r.FieldsPerRecord, r.LazyQuotes, r.TrimLeadingSpace = -1, true, true
	return r.ReadAll()
}

func fromCSV(data []byte, defaultCurrency string) (Result, error) {
	rows, err := readCSV(data)
	if err != nil || len(rows) < 2 {
		return Result{}, ErrUnknownFormat
	}
	cols := map[string]int{}
	for i, h := range rows[0] {
		if f := headers[strings.ToLower(strings.TrimSpace(h))]; f != "" {
			if _, dup := cols[f]; !dup {
				cols[f] = i
			}
		}
	}
	if _, ok := cols["name"]; !ok {
		return Result{}, ErrUnknownFormat
	}
	if _, ok := cols["price"]; !ok {
		return Result{}, ErrUnknownFormat
	}
	out := Result{Source: "csv", Items: []app.ImportItem{}, Notes: []string{}}
	for _, row := range rows[1:] {
		get := func(f string) string {
			if i, ok := cols[f]; ok && i < len(row) {
				return strings.TrimSpace(row[i])
			}
			return ""
		}
		if get("name") == "" {
			continue
		}
		it := app.ImportItem{Name: get("name"), Category: get("category"), PaymentMethod: get("method"), Payer: get("payer"), Notes: get("notes"),
			URL: get("url"), CancelURL: get("cancelurl"), Account: get("account"), NextCharge: AnyDate(get("next")), StartedOn: AnyDate(get("start")),
			EndDate: AnyDate(get("end")), TrialEndsOn: AnyDate(get("trial"))}
		it.Price, it.Currency = SplitPrice(get("price"), defaultCurrency)
		if c := get("currency"); c != "" {
			it.Currency = strings.ToUpper(c)
			if code := symbols[c]; code != "" {
				it.Currency = code
			}
		}
		it.Interval, it.Unit, _ = cadence(get("cycle"))
		if n, err := strconv.Atoi(get("interval")); err == nil && n > 0 {
			it.Interval = n
		}
		if _, u, ok := cadence(get("unit")); ok {
			it.Unit = u
		}
		it.NoticeDays, _ = strconv.Atoi(get("notice"))
		switch strings.ToLower(get("status")) {
		case "cancelled", "canceled", "inactive", "disabled", "ended", "cancelada", "cancelado", "inactiva":
			it.Status = "cancelled"
		case "paused", "pausada":
			it.Status = "paused"
		}
		switch strings.ToLower(get("active")) {
		case "no", "false", "0":
			it.Status = "cancelled"
		}
		if it.NextCharge == "" && it.StartedOn == "" && it.TrialEndsOn == "" {
			it.Remark = "no date in the file: add the next charge date"
		}
		out.Items = append(out.Items, it)
	}
	if len(out.Items) == 0 {
		return Result{}, ErrUnknownFormat
	}
	return out, nil
}

// AnyDate reads the date formats spreadsheets produce; ambiguous numeric
// dates are read day first (31/12/2026), the more common order worldwide.
func AnyDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{time.DateOnly, "2006/01/02", "02/01/2006", "2/1/2006", "02-01-2006", "02.01.2006", "2.1.2006", "Jan 2, 2006", "2 Jan 2006", "January 2, 2006", "2 January 2006", time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format(time.DateOnly)
		}
	}
	return ""
}
