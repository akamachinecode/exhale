package httpapi

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/importer"
)

// TestBillingRulesRoundTrip: a weekday-of-the-month rule and a weekend shift
// survive the JSON export and the CSV export, read back by the importer.
func TestBillingRulesRoundTrip(t *testing.T) {
	srv, svc, owner := setup(t, true)
	ctx := context.Background()
	originals := []app.CreateSubscriptionInput{
		// 15 Nov 2026 is a Sunday: charged on Monday the 16th.
		{Name: "Gym", Price: "30", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-15", WeekendShift: "next"},
		{Name: "Club", Price: "12", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-18", DayRule: "weekday"},
		{Name: "Insurance", Price: "300", Currency: "EUR", Unit: "year", FirstCharge: "2026-11-27", DayRule: "last-weekday", WeekendShift: "previous"},
	}
	want := map[string][]string{}
	for _, in := range originals {
		d, err := svc.CreateSubscription(ctx, owner, in)
		if err != nil {
			t.Fatal(err)
		}
		want[in.Name] = dueDates(d)
	}
	if !slices.Equal(want["Gym"][:2], []string{"2026-11-16", "2026-12-15"}) || want["Club"][1] != "2026-12-16" {
		t.Fatalf("created: %v", want)
	}

	check := func(source string, data []byte) {
		t.Helper()
		parsed, err := importer.Parse(ctx, data, "EUR")
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		res, err := svc.Import(ctx, owner, app.ImportInput{Items: parsed.Items, AllowDuplicates: true})
		if err != nil || res.Created != len(originals) {
			t.Fatalf("%s import: %v %+v (items %+v)", source, err, res, parsed.Items)
		}
		for _, it := range res.Items {
			d, err := svc.GetSubscription(ctx, owner, app.GetSubscriptionInput{ID: it.ID})
			if err != nil {
				t.Fatal(err)
			}
			if got := dueDates(d); !slices.Equal(got, want[it.Name]) {
				t.Errorf("%s: %s charges %v, want %v", source, it.Name, got, want[it.Name])
			}
		}
	}

	exp, err := svc.Export(ctx, owner, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(exp)
	check("json", data)

	code, csv := get(t, srv.URL+"/api/v1/export/subscriptions.csv")
	if code != 200 {
		t.Fatalf("csv: %d", code)
	}
	// One line per name: the JSON round trip added copies.
	var lines []string
	seen := map[string]bool{}
	for i, line := range strings.Split(strings.TrimSpace(csv), "\n") {
		name, _, _ := strings.Cut(line, ",")
		if i == 0 || !seen[name] {
			lines = append(lines, line)
			seen[name] = true
		}
	}
	check("csv", []byte(strings.Join(lines, "\n")+"\n"))
}

func dueDates(d app.SubscriptionDetail) []string {
	var out []string
	for _, c := range d.Upcoming {
		out = append(out, c.DueDate.String())
	}
	return out
}
