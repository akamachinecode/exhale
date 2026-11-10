package importer

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/merchant"
)

// A statement as text (a PDF's text, or lines pasted from one) lists one
// transaction per line: a date, a description, the amount and often the
// balance. TextTransactions reads those lines without a model.

// minTextTransactions is how many dated lines with an amount make a text a
// statement.
const minTextTransactions = 3

var (
	// textDate finds a full date in a line: 2026-11-03, 03/11/2026,
	// 03.11.26, 3 Nov 2026, Nov 3, 2026.
	textDate = regexp.MustCompile(`(?i)\b(\d{4}-\d{2}-\d{2}|\d{1,2}[/.-]\d{1,2}[/.-](?:\d{4}|\d{2})|\d{1,2}[ -](?:[a-z]{3,9}\.?)[ -]\d{4}|(?:[a-z]{3,9}\.?) \d{1,2},? \d{4})\b`)
	// textAmount finds an amount with two decimals, so reference numbers
	// and card digits are not taken for money: -12.99, (12,99), 1.234,56 €,
	// €12.99, 12,99-.
	textAmount = regexp.MustCompile(`(?:^|[\s:])([-−+]?\(?\s*(?:[€$£]\s*)?[-−]?\d{1,3}(?:[.,']\d{3})*[.,]\d{2}\)?(?:\s*(?:€|EUR|USD|GBP|CHF)\b)?-?)(?:\s|$)`)
	// summaryWords mark lines that are totals or balances, not payments.
	summaryWords = []string{"balance", "saldo", "total", "opening", "closing", "previous", "anterior", "subtotal", "carried", "brought"}
)

// TextTransactions reads the transactions of a statement written as text,
// one per line. shaped is false when fewer than three lines look like a
// transaction.
func TextTransactions(text, defaultCurrency string, today engine.Date, source string) (res Result, shaped bool) {
	var all []txn
	spent, received := 0, 0
	mf := monthFirst(text)
	for line := range strings.Lines(text) {
		t, ok := textLine(line, defaultCurrency, mf)
		if !ok {
			continue
		}
		if t.minor < 0 {
			spent++
		} else {
			received++
		}
		t.minor = -t.minor
		all = append(all, t)
		if len(all) > MaxTransactions {
			return Result{}, false
		}
	}
	if len(all) < minTextTransactions {
		return Result{}, false
	}
	// Statements often print what left without a sign, and refunds with
	// one: the majority sign is the money that left, as in a CSV.
	flip(all, spent, received)
	return detect(all, today, source), true
}

// textLine reads one line: its first date, the description that follows
// and the first amount after the description (a later one is usually the
// balance).
func textLine(line, defaultCurrency string, monthFirst bool) (txn, bool) {
	line = strings.TrimSpace(strings.NewReplacer(" ", " ", "\t", "  ").Replace(line))
	dates := textDate.FindAllStringIndex(line, -1)
	if len(dates) == 0 {
		return txn{}, false
	}
	d, ok := lineDate(line[dates[0][0]:dates[0][1]], monthFirst)
	if !ok {
		return txn{}, false
	}
	// Blank out every date (a statement may print the booking and the
	// value date) so neither is read as the description or an amount.
	rest := []byte(line)
	for _, span := range dates {
		for i := span[0]; i < span[1]; i++ {
			rest[i] = ' '
		}
	}
	// Spaces doubled, so two amounts with one space between them are both
	// found (each match takes the space around it).
	body := strings.ReplaceAll(string(rest), " ", "  ")
	amounts := textAmount.FindAllStringSubmatchIndex(body, -1)
	if len(amounts) == 0 {
		return txn{}, false
	}
	first := amounts[0]
	desc := strings.Join(strings.Fields(body[:first[2]]), " ")
	if desc == "" {
		// The amount came first: the description is what follows it.
		end := len(body)
		if len(amounts) > 1 {
			end = amounts[1][2]
		}
		desc = strings.Join(strings.Fields(body[first[3]:end]), " ")
	}
	lower := strings.ToLower(desc)
	for _, w := range summaryWords {
		if strings.Contains(lower, w) {
			return txn{}, false
		}
	}
	raw := strings.TrimSpace(body[first[2]:first[3]])
	negative := strings.HasSuffix(raw, "-")
	minor, cur, ok := parseAmount(strings.TrimSuffix(raw, "-"))
	if !ok || minor == 0 {
		return txn{}, false
	}
	if negative {
		minor = -abs(minor)
	}
	key := merchant.Key(desc)
	if key == "" {
		return txn{}, false
	}
	if cur == "" {
		cur = defaultCurrency
	}
	return txn{date: d, text: desc, key: key, minor: minor, currency: cur}, true
}

var (
	isoDate     = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	numericDate = regexp.MustCompile(`^(\d{1,2})[/.-](\d{1,2})[/.-](\d{2}|\d{4})$`)
	dayMonth    = regexp.MustCompile(`^(\d{1,2})[ -]([a-z]+)\.?[ -](\d{4})$`)
	monthDay    = regexp.MustCompile(`^([a-z]+)\.? (\d{1,2}),? (\d{4})$`)
)

// months are the first three letters of month names in English and
// Spanish.
var months = map[string]time.Month{
	"jan": 1, "ene": 1, "feb": 2, "mar": 3, "apr": 4, "abr": 4, "may": 5, "jun": 6, "jul": 7,
	"aug": 8, "ago": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12, "dic": 12,
}

// lineDate reads a date found in a statement line. Numbers are day first
// (03/11/2026 is 3 November) unless the statement writes the month first;
// two-digit years are this century.
func lineDate(s string, monthFirst bool) (time.Time, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	var y, m, d int
	num := func(v string) int { n, _ := strconv.Atoi(v); return n }
	month := func(v string) int {
		if len(v) < 3 {
			return 0
		}
		return int(months[v[:3]])
	}
	switch {
	case isoDate.MatchString(s):
		p := isoDate.FindStringSubmatch(s)
		y, m, d = num(p[1]), num(p[2]), num(p[3])
	case numericDate.MatchString(s):
		p := numericDate.FindStringSubmatch(s)
		d, m, y = num(p[1]), num(p[2]), num(p[3])
		if monthFirst {
			d, m = m, d
		}
		if len(p[3]) == 2 {
			y += 2000
		}
	case dayMonth.MatchString(s):
		p := dayMonth.FindStringSubmatch(s)
		d, m, y = num(p[1]), month(p[2]), num(p[3])
	case monthDay.MatchString(s):
		p := monthDay.FindStringSubmatch(s)
		m, d, y = month(p[1]), num(p[2]), num(p[3])
	}
	if m < 1 || m > 12 || d < 1 || d > 31 || y < 1990 || y > 2200 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t, t.Day() == d // 31 April is not a date
}

// monthFirst reports whether a text writes numeric dates month first
// (11/03/2026): some date has a second number above 12 and none has a
// first one above 12.
func monthFirst(text string) bool {
	first, second := false, false
	for _, f := range textDate.FindAllString(text, 2000) {
		if p := numericDate.FindStringSubmatch(strings.TrimSpace(f)); p != nil {
			a, _ := strconv.Atoi(p[1])
			b, _ := strconv.Atoi(p[2])
			first, second = first || a > 12, second || b > 12
		}
	}
	return second && !first
}
