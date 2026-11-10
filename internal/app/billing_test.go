package app

import (
	"context"
	"slices"
	"testing"
)

func due(d SubscriptionDetail) []string {
	var out []string
	for _, c := range d.Upcoming {
		out = append(out, c.DueDate.String())
	}
	return out
}

// The fixture's today is 2026-11-05.

func TestThirdWednesdayWithPreviewAndWords(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Club", Price: "12", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-18", DayRule: "weekday"})
	want := []string{"2026-11-18", "2026-12-16", "2027-01-20", "2027-02-17"}
	if got := due(d); !slices.Equal(got[:4], want) {
		t.Fatalf("charges = %v", got)
	}
	if d.Subscription.BillingDay != "on the third Wednesday" || d.Subscription.Cadence != "monthly" {
		t.Fatalf("view = %q %q", d.Subscription.BillingDay, d.Subscription.Cadence)
	}
	p, err := f.svc.PreviewSchedule(ctx, f.owner, PreviewScheduleInput{Interval: 1, Unit: "month", FirstCharge: "2026-11-18", DayRule: "weekday", Count: 4})
	if err != nil || p.Dates[3].String() != "2027-02-17" {
		t.Fatalf("preview = %v %v", p.Dates, err)
	}
	// A fifth weekday does not exist in every month: the error says what to do.
	_, err = f.svc.PreviewSchedule(ctx, f.owner, PreviewScheduleInput{Interval: 1, Unit: "month", FirstCharge: "2026-12-30", DayRule: "weekday"})
	if err == nil {
		t.Fatal("a fifth Wednesday must be refused")
	}
}

func TestWeekendShiftKeepsTheBillingDay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// The 15th; 15 Nov 2026 is a Sunday, 15 Aug 2027 too.
	d := f.create(t, CreateSubscriptionInput{Name: "Gym", Price: "30", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-15", WeekendShift: "next"})
	if got := due(d); !slices.Equal(got[:3], []string{"2026-11-16", "2026-12-15", "2027-01-15"}) {
		t.Fatalf("charges = %v", got)
	}
	s := d.Subscription
	if s.NextCharge.String() != "2026-11-16" || s.NextScheduled.String() != "2026-11-15" {
		t.Fatalf("next = %s, scheduled %s", s.NextCharge, s.NextScheduled)
	}
	if s.BillingDay != "moved to the next business day when it falls on a weekend" {
		t.Fatalf("words = %q", s.BillingDay)
	}
	// Every 2 months from the next charge: still the 15th, not the 16th.
	two := 2
	d, err := f.svc.UpdateSubscription(ctx, f.owner, UpdateSubscriptionInput{ID: s.ID, Interval: &two})
	if err != nil {
		t.Fatal(err)
	}
	if got := due(d); !slices.Equal(got[:2], []string{"2026-11-16", "2027-01-15"}) {
		t.Fatalf("every 2 months = %v", got)
	}
	// Shift to the previous business day instead.
	prev := "previous"
	d, err = f.svc.UpdateSubscription(ctx, f.owner, UpdateSubscriptionInput{ID: s.ID, WeekendShift: &prev})
	if err != nil {
		t.Fatal(err)
	}
	if got := due(d); !slices.Equal(got[:2], []string{"2026-11-13", "2027-01-15"}) {
		t.Fatalf("previous business day = %v", got)
	}
	// Cancelling at period end ends the service the day before the (shifted)
	// next charge, which is also the first one.
	d, err = f.svc.SetSubscriptionStatus(ctx, f.owner, SetSubscriptionStatusInput{ID: s.ID, Action: "cancel_at_period_end"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Subscription.CancelledUntil.String() != "2026-11-12" || len(d.Upcoming) != 0 {
		t.Fatalf("cancelled: until %s, upcoming %v", d.Subscription.CancelledUntil, due(d))
	}
}

func TestSwitchingToAWeekdayRuleAndMovingFollowing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Paper", Price: "9", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-27"})
	// 27 Nov 2026 is the last Friday of November.
	last := "last-weekday"
	d, err := f.svc.UpdateSubscription(ctx, f.owner, UpdateSubscriptionInput{ID: d.Subscription.ID, DayRule: &last})
	if err != nil {
		t.Fatal(err)
	}
	if got := due(d); !slices.Equal(got[:3], []string{"2026-11-27", "2026-12-25", "2027-01-29"}) {
		t.Fatalf("last Friday = %v", got)
	}
	// From the December charge on, the second Tuesday: the rule stays a
	// weekday of the month.
	res, err := f.svc.ChangeChargeDate(ctx, f.owner, ChangeChargeDateInput{ChargeID: d.Upcoming[1].ID, NewDate: "2026-12-08", Scope: "following"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Charge.DueDate.String() != "2026-12-08" {
		t.Fatalf("moved charge = %+v", res.Charge)
	}
	d, _ = f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: d.Subscription.ID})
	if got := due(d); !slices.Equal(got[:3], []string{"2026-11-27", "2026-12-08", "2027-01-12"}) {
		t.Fatalf("second Tuesday = %v", got)
	}
	// A weekly cycle has no weekday of the month: it is dropped.
	week := "week"
	if d, err = f.svc.UpdateSubscription(ctx, f.owner, UpdateSubscriptionInput{ID: d.Subscription.ID, Unit: &week}); err != nil {
		t.Fatal(err)
	}
	if d.Subscription.BillingDay != "" || d.Subscription.Cadence != "weekly" {
		t.Fatalf("weekly: %q %q", d.Subscription.BillingDay, d.Subscription.Cadence)
	}
}
