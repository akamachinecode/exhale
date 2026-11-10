package httpapi

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// TestImportReviewThenExport is the way in and out: a spreadsheet is read
// into a review list, previewed, imported, and comes back out as CSV.
func TestImportReviewThenExport(t *testing.T) {
	srv, _, _ := setup(t, true)
	sheet := "Service,Price,Cycle,Next payment,Category,Card\nNetflix,13.99 EUR,Monthly,2026-11-12,Streaming,Visa\nDomain,$12,Yearly,2027-03-01,Hosting,Visa\nBroken,abc,Monthly,2026-11-12,,\n"
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/import/parse", strings.NewReader(sheet))
	req.Header.Set("Content-Type", "text/csv")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), `"source":"csv"`) {
		t.Fatalf("parse: %d %s", res.StatusCode, body)
	}
	items := []map[string]any{
		{"name": "Netflix", "price": "13.99", "currency": "EUR", "unit": "month", "nextCharge": "2026-11-12", "category": "Streaming", "paymentMethod": "Visa"},
		{"name": "Domain", "price": "12", "currency": "USD", "unit": "year", "nextCharge": "2027-03-01", "category": "Hosting", "paymentMethod": "Visa"},
		{"name": "Broken", "price": "abc", "unit": "month", "nextCharge": "2026-11-12"},
	}
	code, preview := do(t, "POST", srv.URL+"/api/v1/import", map[string]any{"items": items, "dryRun": true}, nil)
	if code != 200 || preview["created"].(float64) != 2 || preview["errors"].(float64) != 1 {
		t.Fatalf("preview: %d %v", code, preview)
	}
	if _, list := do(t, "GET", srv.URL+"/api/v1/subscriptions", nil, nil); len(list["subscriptions"].([]any)) != 0 {
		t.Fatal("a preview must not save anything")
	}
	code, done := do(t, "POST", srv.URL+"/api/v1/import", map[string]any{"items": items}, nil)
	if code != 200 || done["created"].(float64) != 2 || len(done["newCategories"].([]any)) != 1 || len(done["newPaymentMethods"].([]any)) != 1 {
		t.Fatalf("import: %d %v", code, done)
	}
	// Importing the same file again creates nothing.
	_, again := do(t, "POST", srv.URL+"/api/v1/import", map[string]any{"items": items}, nil)
	if again["created"].(float64) != 0 || again["duplicates"].(float64) != 2 {
		t.Fatalf("second import: %v", again)
	}
	code, csv := get(t, srv.URL+"/api/v1/export/subscriptions.csv")
	if code != 200 || !strings.Contains(csv, "Netflix,13.99,EUR,monthly,1,month,2026-11-12,active,Streaming,Visa,Alex") {
		t.Fatalf("subscriptions.csv: %d\n%s", code, csv)
	}
	code, csv = get(t, srv.URL+"/api/v1/export/charges.csv")
	if code != 200 || !strings.Contains(csv, "2026-11-12,Netflix,13.99,EUR,pending") {
		t.Fatalf("charges.csv: %d\n%s", code, csv)
	}
}

func TestCalendarFeed(t *testing.T) {
	srv, _, _ := setup(t, true)
	do(t, "POST", srv.URL+"/api/v1/subscriptions", map[string]any{"name": "Gym, annual", "price": "390", "currency": "EUR", "unit": "year", "firstCharge": "2026-12-20", "noticeDays": 30}, nil)
	code, feed := do(t, "POST", srv.URL+"/api/v1/calendar/feed", nil, nil)
	if code != 200 || feed["path"] == nil {
		t.Fatalf("create feed: %d %v", code, feed)
	}
	path := feed["path"].(string)
	code, ics := get(t, srv.URL+path)
	if code != 200 {
		t.Fatalf("feed: %d", code)
	}
	for _, want := range []string{"BEGIN:VCALENDAR\r\n", "DTSTART;VALUE=DATE:20261220\r\n", `SUMMARY:Gym\, annual · 390.00 EUR`, "SUMMARY:Last day to cancel Gym\\, annual\r\n", "DTSTART;VALUE=DATE:20261120\r\n", "END:VCALENDAR\r\n"} {
		if !strings.Contains(ics, want) {
			t.Fatalf("feed lacks %q:\n%s", want, ics)
		}
	}
	if code, _ := get(t, srv.URL+"/feeds/calendar/wrong.ics"); code != 404 {
		t.Fatalf("a wrong key must not exist: %d", code)
	}
	// Replacing the link kills the old one.
	_, replaced := do(t, "POST", srv.URL+"/api/v1/calendar/feed", nil, nil)
	if code, _ := get(t, srv.URL+path); code != 404 {
		t.Fatalf("the old link must stop working: %d", code)
	}
	if code, _ := get(t, srv.URL+replaced["path"].(string)); code != 200 {
		t.Fatalf("the new link must work: %d", code)
	}
	do(t, "DELETE", srv.URL+"/api/v1/calendar/feed", nil, nil)
	if code, _ := get(t, srv.URL+replaced["path"].(string)); code != 404 {
		t.Fatalf("a removed link must stop working: %d", code)
	}
}
