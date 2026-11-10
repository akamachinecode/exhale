package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/akamachinecode/exhale/internal/app"
)

// wallosFile builds a small database with the tables and columns of a real
// Wallos v5 instance.
func wallosFile(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wallos.db")
	conn, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE currencies (id INTEGER PRIMARY KEY, name TEXT, symbol TEXT, code TEXT, rate TEXT)`,
		`CREATE TABLE cycles (id INTEGER PRIMARY KEY, days INTEGER, name TEXT)`,
		`CREATE TABLE categories (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE payment_methods (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE notification_settings (days INTEGER)`,
		`CREATE TABLE subscriptions (id INTEGER PRIMARY KEY, name TEXT, logo TEXT, price REAL, currency_id INTEGER, next_payment DATE, cycle INTEGER, frequency INTEGER, notes TEXT, payment_method_id INTEGER, payer_user_id INTEGER, category_id INTEGER, notify BOOLEAN, url TEXT, inactive BOOLEAN, notify_days_before INTEGER, cancellation_date DATE, start_date DATE, auto_renew BOOLEAN)`,
		`INSERT INTO currencies VALUES (1, 'Euro', '€', 'EUR', '1'), (2, 'US Dollar', '$', 'USD', '1.1')`,
		`INSERT INTO cycles VALUES (1, 1, 'Daily'), (2, 7, 'Weekly'), (3, 30, 'Monthly'), (4, 365, 'Yearly'), (5, 0, 'One-time')`,
		`INSERT INTO categories VALUES (1, 'No category'), (3, 'Music')`,
		`INSERT INTO payment_methods VALUES (38, 'Revolut Debit')`,
		`INSERT INTO notification_settings VALUES (1)`,
		`INSERT INTO subscriptions VALUES (1, 'Proton Mail &amp; Calendar', '', 4.99, 1, '2026-11-28', 3, 1, '', 38, 1, 1, 1, NULL, 0, -1, NULL, '2026-02-28', 1)`,
		`INSERT INTO subscriptions VALUES (2, 'Spotify', '', 13.59, 2, '2026-02-09', 3, 1, 'Spotify', 38, 1, 3, 1, 'spotify.com', 1, 3, NULL, '2023-11-28', 1)`,
		`INSERT INTO subscriptions VALUES (3, 'example.com', '', 12, 1, '2027-03-01', 4, 2, '', NULL, 1, 1, 0, '', 0, 0, NULL, NULL, 0)`,
	} {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWallosDatabase(t *testing.T) {
	res, err := Parse(context.Background(), wallosFile(t), "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "wallos-db" || len(res.Items) != 3 {
		t.Fatalf("source %s, %d items", res.Source, len(res.Items))
	}
	got, _ := json.Marshal(res.Items)
	want, _ := json.Marshal([]app.ImportItem{
		{Name: "Proton Mail & Calendar", Price: "4.99", Currency: "EUR", Interval: 1, Unit: "month", NextCharge: "2026-11-28", StartedOn: "2026-02-28", PaymentMethod: "Revolut Debit", ReminderDays: []int{1, 0}},
		{Name: "Spotify", Price: "13.59", Currency: "USD", Interval: 1, Unit: "month", NextCharge: "2023-11-28", StartedOn: "2023-11-28", EndDate: "2026-02-08", Status: "cancelled", Category: "Music", PaymentMethod: "Revolut Debit", URL: "spotify.com", Notes: "Spotify", ReminderDays: []int{3, 0}, Remark: "inactive in Wallos: imported as cancelled"},
		{Name: "example.com", Price: "12.00", Currency: "EUR", Interval: 2, Unit: "year", NextCharge: "2027-03-01", Renewal: "manual", ReminderDays: []int{}},
	})
	if string(got) != string(want) {
		t.Fatalf("items:\n got %s\nwant %s", got, want)
	}
}

func TestWallosExportAndCSV(t *testing.T) {
	export := `{"success":true,"subscriptions":[{"Name":"ChatGPT","Payment Cycle":"Every 3 Months","Next Payment":"2026-11-20","Renewal":"Automatic","Category":"Productivity / Work","Payment Method":"Capital One","Paid By":"alex","Price":"$19.99","Notes":"","URL":"chatgpt.com","State":"Enabled","Notifications":"Enabled","Cancellation Date":null,"Active":"Yes"}]}`
	res, err := Parse(context.Background(), []byte(export), "EUR")
	if err != nil {
		t.Fatal(err)
	}
	it := res.Items[0]
	if res.Source != "wallos-export" || it.Price != "19.99" || it.Currency != "USD" || it.Interval != 3 || it.Unit != "month" || it.Category != "Productivity" {
		t.Fatalf("wallos export item = %+v", it)
	}

	sheet := "Servicio;Precio;Ciclo;Próximo pago;Tarjeta;Estado\nNetflix;13,99 €;Mensual;06/12/2026;Visa;\nGym;39;Yearly;2026-12-20;;cancelled\n;;;;;\n"
	res, err = Parse(context.Background(), []byte(sheet), "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "csv" || len(res.Items) != 2 {
		t.Fatalf("csv: %+v", res)
	}
	n, g := res.Items[0], res.Items[1]
	if n.Price != "13.99" || n.Currency != "EUR" || n.NextCharge != "2026-12-06" || n.PaymentMethod != "Visa" || n.Unit != "month" {
		t.Fatalf("netflix = %+v", n)
	}
	if g.Unit != "year" || g.Status != "cancelled" || g.Price != "39" {
		t.Fatalf("gym = %+v", g)
	}
	if _, err := Parse(context.Background(), []byte("hello\nworld\n"), "EUR"); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("an unrelated file must be refused, got %v", err)
	}
}
