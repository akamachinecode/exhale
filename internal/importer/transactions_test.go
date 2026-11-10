package importer

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/merchant"
)

var today = engine.MustDate(2026, time.November, 10)

func byName(items []app.ImportItem) map[string]app.ImportItem {
	m := map[string]app.ImportItem{}
	for _, it := range items {
		m[it.Name] = it
	}
	return m
}

// TestRecurringChargesInBankExport reads half a year of an account: the
// subscriptions are found with their cycle, price and next charge, and the
// shopping, the salary, the refund and the failed payment are not.
func TestRecurringChargesInBankExport(t *testing.T) {
	data, err := os.ReadFile("testdata/bank.csv")
	if err != nil {
		t.Fatal(err)
	}
	res, shaped := Transactions(data, "EUR", today)
	if !shaped {
		t.Fatal("a bank export was not recognised")
	}
	if res.Source != "transactions" || res.Model != "" {
		t.Fatalf("source %q model %q", res.Source, res.Model)
	}
	got := byName(res.Items)
	want := map[string]struct {
		price, unit, next string
		interval          int
		remark            string
	}{
		"Netflix":             {"13.99", "month", "2026-12-06", 1, "6 charges every month, last on 2026-11-06; the amount went from 12.99 to 13.99"},
		"Spotify":             {"17.99", "month", "2026-11-28", 1, "5 charges every month, last on 2026-10-28"},
		"Adobe Creative":      {"67.25", "month", "2026-11-15", 1, "6 charges every month"},
		"Iberdrola Comercial": {"61.07", "month", "2026-12-03", 1, "the amount varies between 48.10 and 74.90: the last one is used"},
		"Gym Urban":           {"29.00", "week", "", 2, "every 2 weeks"},
		"Hetzner":             {"23.80", "month", "2026-12-01", 3, "2 charges every 3 months"},
		"Dropbox":             {"9.99", "month", "2026-11-10", 1, "not charged since: it may have ended"},
	}
	for name, w := range want {
		it, ok := got[name]
		if !ok {
			t.Errorf("%s was not found; got %v", name, names(res.Items))
			continue
		}
		if it.Price != w.price || it.Unit != w.unit || it.Interval != w.interval || it.Currency != "EUR" {
			t.Errorf("%s: %s %s every %d %s", name, it.Price, it.Currency, it.Interval, it.Unit)
		}
		if w.next != "" && it.NextCharge != w.next {
			t.Errorf("%s: next charge %s, want %s", name, it.NextCharge, w.next)
		}
		if !strings.Contains(it.Remark, w.remark) {
			t.Errorf("%s: remark %q does not say %q", name, it.Remark, w.remark)
		}
		if it.NextCharge < today.String() {
			t.Errorf("%s: the next charge %s is in the past", name, it.NextCharge)
		}
	}
	for _, name := range []string{"Mercadona", "Salary", "Amazon", "Zara", "Renfe", "Acme"} {
		for got := range got {
			if strings.Contains(got, name) {
				t.Errorf("%s is not a subscription but was listed as %q", name, got)
			}
		}
	}
	if len(res.Items) != len(want) {
		t.Errorf("found %v, want %d items", names(res.Items), len(want))
	}
}

func names(items []app.ImportItem) []string {
	var n []string
	for _, it := range items {
		n = append(n, it.Name)
	}
	return n
}

// TestCardStatementAndOtherShapes: charges are positive on a card
// statement, banks write amounts and dates in their own ways, and a list of
// subscriptions is not mistaken for transactions.
func TestCardStatementAndOtherShapes(t *testing.T) {
	card := "Date,Description,Amount\n" +
		"03/08/2026,NETFLIX.COM,13.99\n03/09/2026,NETFLIX.COM,13.99\n03/10/2026,NETFLIX.COM,13.99\n" +
		"10/09/2026,PAYMENT RECEIVED - THANK YOU,-250.00\n12/09/2026,RESTAURANTE LA BARCA,45.00\n"
	res, shaped := Transactions([]byte(card), "EUR", today)
	if !shaped || len(res.Items) != 1 || res.Items[0].Name != "Netflix" || res.Items[0].NextCharge != "2026-12-03" {
		t.Fatalf("card statement: shaped=%v %+v", shaped, res.Items)
	}

	es := "Fecha;Concepto;Importe;Divisa;Saldo\n" +
		"05/08/2026;RECIBO MOVISTAR FIBRA 600;-45,90;EUR;1.200,10\n05/09/2026;RECIBO MOVISTAR FIBRA 600;-45,90;EUR;1.154,20\n05/10/2026;RECIBO MOVISTAR FIBRA 600;-45,90;EUR;1.108,30\n" +
		"28/09/2026;NOMINA EMPRESA SL;2.100,00;EUR;3.208,30\n"
	res, shaped = Transactions([]byte(es), "USD", today)
	if !shaped || len(res.Items) != 1 || res.Items[0].Price != "45.90" || res.Items[0].Currency != "EUR" || res.Items[0].Name != "Movistar Fibra" {
		t.Fatalf("spanish export: shaped=%v %+v", shaped, res.Items)
	}

	debit := "Transaction Date,Details,Debit,Credit,Balance\n" +
		"2026-08-01,DISNEY PLUS,8.99,,100\n2026-09-01,DISNEY PLUS,8.99,,91\n2026-10-01,DISNEY PLUS,8.99,,82\n2026-10-02,REFUND DISNEY PLUS,,8.99,91\n"
	res, shaped = Transactions([]byte(debit), "GBP", today)
	if !shaped || len(res.Items) != 1 || res.Items[0].Name != "Disney Plus" || res.Items[0].Currency != "GBP" {
		t.Fatalf("debit and credit columns: shaped=%v %+v", shaped, res.Items)
	}

	if _, shaped := Transactions([]byte("Service,Price,Cycle,Next payment\nNetflix,13.99,Monthly,2026-11-12\nDomain,12,Yearly,2027-03-01\n"), "EUR", today); shaped {
		t.Error("a list of subscriptions was read as transactions")
	}
	if _, shaped := Transactions([]byte("Name,Amount,Date\nNetflix,13.99,2026-11-12\nDomain,12,2027-03-01\n"), "EUR", today); shaped {
		t.Error("a list with a name, an amount and a date, nothing repeated and no bank column, is a list of subscriptions")
	}
	res, shaped = Transactions([]byte("Date,Description,Amount,Balance\n2026-10-01,COFFEE,-3.00,10\n2026-10-09,BOOKSHOP,-20.00,7\n"), "EUR", today)
	if !shaped || len(res.Items) != 0 {
		t.Errorf("transactions with nothing recurring: shaped=%v %+v", shaped, res.Items)
	}
}

func TestParseAmountAndMerchant(t *testing.T) {
	for in, want := range map[string]int64{"-12.99": -1299, "12,99": 1299, "1.234,56": 123456, "1,234.56": 123456, "(12.99)": -1299, "€12,99": 1299, "12.99 EUR": 1299, "−5": -500, "1 234,5": 123450, "+7.5": 750} {
		if got, _, ok := parseAmount(in); !ok || got != want {
			t.Errorf("parseAmount(%q) = %d %v, want %d", in, got, ok, want)
		}
	}
	if _, cur, _ := parseAmount("12.99 USD"); cur != "USD" {
		t.Errorf("currency: %q", cur)
	}
	if _, _, ok := parseAmount("n/a"); ok {
		t.Error("text is not an amount")
	}
	for in, want := range map[string]string{
		"NETFLIX.COM 866-579-7172 CA": "netflix", "Netflix.com Amsterdam": "netflix amsterdam", "SPOTIFY P1A2B3C4D5": "spotify",
		"GOOGLE *YouTubePremium": "google youtubepremium", "CARD PAYMENT TO ADOBE CREATIVE CLOUD": "adobe creative", "PAYPAL *DROPBOX 4029357733": "dropbox",
	} {
		if got := merchant.Key(in); got != want {
			t.Errorf("merchant.Key(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSniff(t *testing.T) {
	for in, want := range map[string]FileKind{
		"%PDF-1.7\n…": FilePDF, "\x89PNG\r\n\x1a\nrest": FileImage, "\xff\xd8\xff\xe0jpeg": FileImage, "RIFF\x00\x00\x00\x00WEBPVP8 ": FileImage,
		"SQLite format 3\x00": FileSQLite, ` {"format":"exhale/1"}`: FileJSON, "name,price\nNetflix,1\n": FileText, "PK\x03\x04\x00\x00zip": FileUnknown, "": FileUnknown,
	} {
		if got, _ := Sniff([]byte(in)); got != want {
			t.Errorf("Sniff(%q) = %d, want %d", in, got, want)
		}
	}
}
