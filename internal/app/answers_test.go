package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/store/db"
)

func TestStoppedPayingCountsCancelledNotPaused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.svc.Store().Q.UpsertFxRate(ctx, db.UpsertFxRateParams{Day: "2026-11-05", Currency: "USD", Rate: "1.1224", Source: "ecb"}); err != nil {
		t.Fatal(err)
	}
	netflix := f.create(t, CreateSubscriptionInput{Name: "Netflix", Price: "10.00", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-12"})
	copilot := f.create(t, CreateSubscriptionInput{Name: "Copilot", Price: "112.24", Currency: "USD", Unit: "year", FirstCharge: "2026-11-20"})
	paused := f.create(t, CreateSubscriptionInput{Name: "Gym", Price: "39.00", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-14"})
	spotify := f.create(t, CreateSubscriptionInput{Name: "Spotify", Price: "10.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-14"})
	f.create(t, CreateSubscriptionInput{Name: "Spotify Family", Price: "17.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20"})
	for id, action := range map[string]string{netflix.Subscription.ID: "cancel_now", copilot.Subscription.ID: "cancel_at_period_end", paused.Subscription.ID: "pause", spotify.Subscription.ID: "cancel_now"} {
		if _, err := f.svc.SetSubscriptionStatus(ctx, f.owner, SetSubscriptionStatusInput{ID: id, Action: action}); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := f.svc.Summary(ctx, f.owner, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	// Netflix 120 EUR + Copilot 112.20 USD (its yearly cost, normalised
	// through the month like everywhere else) = 99.96 EUR; the paused gym
	// does not count, and Spotify was replaced by Spotify Family.
	if sum.Stopped.Count != 2 || sum.Stopped.Yearly.String() != "219.96 EUR" || sum.Stopped.Subscriptions[0].Name != "Copilot" {
		t.Fatalf("stopped = %+v", sum.Stopped)
	}
}

func TestSnoozeNeverComesBackTooLate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// Today is 5 Nov; it renews on the 7th.
	d := f.create(t, CreateSubscriptionInput{Name: "Netflix", Price: "10.00", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-07"})
	chg := d.Upcoming[0].ID
	if err := f.svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.Snooze(ctx, f.owner, SnoozeInput{ChargeID: chg, Kind: KindReminder, Days: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.On.String() != "2026-11-07" || !res.Clamped {
		t.Fatalf("a reminder snoozed past the charge date must come back on it: %+v", res)
	}
	if !res.Planned {
		t.Fatal("the reminder of the day is already planned; a second one must not be added")
	}
	// 10:00 in Madrid is past 09:00: tomorrow at 09:00 local time.
	res, err = f.svc.Snooze(ctx, f.owner, SnoozeInput{ChargeID: chg, Kind: KindConfirm, Days: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.At.In(mustLoc(t)).Format("2006-01-02 15:04"); got != "2026-11-06 09:00" {
		t.Fatalf("comes back at %s", got)
	}
	if _, err := f.svc.Snooze(ctx, f.owner, SnoozeInput{ChargeID: chg, Kind: KindDecision, Days: 1}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("no notice period, no decision to snooze: %v", err)
	}
	if _, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: chg, Status: "paid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Snooze(ctx, f.owner, SnoozeInput{ChargeID: chg, Kind: KindReminder, Days: 1}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a paid charge has nothing to remind: %v", err)
	}
}

func TestDismissedDuplicateIsNotReportedAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, CreateSubscriptionInput{Name: "Spotify Family", Price: "17.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20"})
	f.create(t, CreateSubscriptionInput{Name: "Spotify", Price: "10.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-22"})
	a, _ := f.svc.Audit(ctx, f.owner, struct{}{})
	if len(a.Duplicates) != 1 || a.Duplicates[0].Key == "" {
		t.Fatalf("duplicates = %+v", a.Duplicates)
	}
	key := a.Duplicates[0].Key
	if _, err := f.svc.DismissDuplicate(ctx, f.owner, DismissDuplicateInput{Key: key, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if a, _ = f.svc.Audit(ctx, f.owner, struct{}{}); len(a.Duplicates) != 1 {
		t.Fatal("a dry run must not dismiss")
	}
	if _, err := f.svc.DismissDuplicate(ctx, f.owner, DismissDuplicateInput{Key: key}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.DismissDuplicate(ctx, f.owner, DismissDuplicateInput{Key: key}); err != nil {
		t.Fatalf("saying it twice is fine: %v", err)
	}
	if a, _ = f.svc.Audit(ctx, f.owner, struct{}{}); len(a.Duplicates) != 0 {
		t.Fatalf("still reported: %+v", a.Duplicates)
	}
	// A third Spotify is a new group, and is reported again.
	f.create(t, CreateSubscriptionInput{Name: "Spotify Duo", Price: "14.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-22"})
	if a, _ = f.svc.Audit(ctx, f.owner, struct{}{}); len(a.Duplicates) != 1 || a.Duplicates[0].Key == key {
		t.Fatalf("the new group = %+v", a.Duplicates)
	}
}

func TestConfirmAsks(t *testing.T) {
	p := domain.DefaultReminderPolicy()
	d, _ := parseDate("2026-11-05")
	got := p.ConfirmAsks(d)
	if len(got) != 3 || got[0].String() != "2026-11-06" || got[2].String() != "2026-11-08" {
		t.Fatalf("asks = %v", got)
	}
	p.ConfirmAfterDays = -1
	if len(p.ConfirmAsks(d)) != 0 {
		t.Fatal("no confirmation, no asks")
	}
	if err := domain.ValidateConfirmRepeat(0, 1); err == nil {
		t.Fatal("asking zero times is not a setting")
	}
}

func mustLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func parseDate(s string) (engine.Date, error) { return engine.ParseDate(s) }
