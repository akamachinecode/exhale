package app

import (
	"context"
	"testing"
)

func TestGlance(t *testing.T) {
	f := newFixture(t) // today is 2026-11-05 in Madrid, Spanish
	f.create(t, CreateSubscriptionInput{Name: "Netflix", Price: "13.99", Currency: "EUR", Interval: 1, Unit: "month", FirstCharge: "2026-11-06"})
	f.create(t, CreateSubscriptionInput{Name: "iCloud", Price: "2.99", Currency: "USD", Interval: 1, Unit: "month", FirstCharge: "2026-11-20"})
	f.create(t, CreateSubscriptionInput{Name: "Gym", Price: "30", Currency: "EUR", Interval: 1, Unit: "year", FirstCharge: "2026-11-05"})
	f.now = f.now.AddDate(0, 0, 1) // the gym's charge of yesterday waits to be confirmed
	g, err := f.svc.Glance(context.Background(), f.owner, GlanceInput{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Language != "es" || len(g.Charges) != 3 || g.Charges[0].Name != "Netflix" || g.Charges[0].DaysUntil != 0 {
		t.Fatalf("glance = %+v", g)
	}
	if g.Charges[0].Amount != "13,99\u00a0€" {
		t.Errorf("amount written %q", g.Charges[0].Amount)
	}
	if len(g.Totals) != 2 || g.Totals[0] != "27,98\u00a0€" {
		t.Errorf("totals %q", g.Totals)
	}
	if g.Overdue != 1 {
		t.Errorf("overdue %d", g.Overdue)
	}
	if g.Awake.Mode != KeepAwakeOff {
		t.Errorf("keep awake %+v", g.Awake)
	}
	g, _ = f.svc.Glance(context.Background(), f.owner, GlanceInput{Limit: 1})
	if len(g.Charges) != 1 || len(g.Totals) != 2 {
		t.Errorf("limit 1: %+v", g)
	}
}
