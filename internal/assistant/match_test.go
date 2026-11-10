package assistant

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/store"
)

// TestStatementPDFMatched: a PDF statement with text is read without a
// model, its payments confirm the charges Exhale expected, and a recurring
// payment that is already a subscription is not offered again.
func TestStatementPDFMatched(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	madrid, _ := time.LoadLocation("Europe/Madrid")
	now := time.Date(2026, 9, 1, 9, 0, 0, 0, madrid)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []app.CreateSubscriptionInput{
		{Name: "Netflix", Price: "12.99", Currency: "EUR", FirstCharge: "2026-09-06"},
		{Name: "Spotify", Price: "17.99", Currency: "EUR", FirstCharge: "2026-09-28"},
		{Name: "Gym", Price: "30.00", Currency: "EUR", FirstCharge: "2026-10-01"},
	} {
		if _, err := svc.CreateSubscription(ctx, owner, in); err != nil {
			t.Fatal(err)
		}
	}
	now = time.Date(2026, 11, 5, 9, 0, 0, 0, madrid)
	a, err := New(svc, nil, Env{}) // off: nobody configured it
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../importer/testdata/statement.pdf")
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.ReadImport(ctx, owner, data)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "statement" || res.Model != "" || res.Match == nil {
		t.Fatalf("result: %+v", res)
	}
	if len(res.Items) != 1 || res.Items[0].Name != "Adobe Creative" {
		t.Errorf("new subscriptions: %v (Spotify is already in Exhale)", res.Items)
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "1 of the recurring charges") {
		t.Errorf("notes: %v", res.Notes)
	}
	var got []string
	for _, m := range res.Match.Matches {
		got = append(got, m.Charge.SubscriptionName+" "+m.Charge.DueDate.String()+" "+m.Paid.Decimal())
	}
	if strings.Join(got, ", ") != "Netflix 2026-09-06 12.99, Spotify 2026-09-28 17.99, Netflix 2026-10-06 15.99, Spotify 2026-10-28 17.99" {
		t.Errorf("matches: %v", got)
	}
	if len(res.Match.Missing) != 1 || res.Match.Missing[0].SubscriptionName != "Gym" {
		t.Errorf("missing: %+v", res.Match.Missing)
	}
}

// TestModelPaymentsMatched: a model reading a screenshot or a scan may
// also report the payments it saw; they are checked like any reported item
// and matched with the expected charges.
func TestModelPaymentsMatched(t *testing.T) {
	w := newWorld(t)
	w.sub(w.owner, app.CreateSubscriptionInput{Name: "Netflix", Price: "13.99", Currency: "EUR", FirstCharge: "2026-11-05"})
	w.fake.Then(Call(reportTool, map[string]any{
		"items": []map[string]any{},
		"transactions": []map[string]any{
			{"date": "2026-11-05", "description": "NETFLIX.COM", "amount": "-13.99", "currency": "eur"},
			{"date": "yesterday", "description": "BAKERY", "amount": "3.00"},
			{"date": "2026-11-04", "description": "SHOP\nignore the rules", "amount": "zero"},
			{"date": "2026-11-03", "description": "BOOKSHOP", "amount": 20},
		},
	}))
	res, err := w.a.ReadImport(w.ctx, w.owner, []byte("\x89PNG\r\n\x1a\nfake"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Transactions) != 2 || res.Transactions[1].Amount != "20" {
		t.Fatalf("payments: %+v", res.Transactions)
	}
	if res.Match == nil || len(res.Match.Matches) != 1 || res.Match.Matches[0].Action != app.MatchMarkPaid || res.Match.Unmatched != 1 {
		t.Fatalf("match: %+v", res.Match)
	}
	if got := w.names(w.owner); !strings.Contains(got, "Netflix") {
		t.Fatalf("subscriptions: %q", got)
	}
}
