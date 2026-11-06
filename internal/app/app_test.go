package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
)

type fixture struct {
	svc   *Service
	owner Actor
	bus   *bus.Bus
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fixture{bus: bus.New(), now: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)}
	f.svc = New(st, f.bus, nil, func() time.Time { return f.now })
	f.owner, err = f.svc.Bootstrap(ctx, BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) create(t *testing.T, in CreateSubscriptionInput) SubscriptionDetail {
	t.Helper()
	d, err := f.svc.CreateSubscription(context.Background(), f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCreateMaterializesUpcomingCharges(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, CreateSubscriptionInput{Name: "Spotify Family", Price: "17.99", Currency: "EUR", Interval: 1, Unit: "month", FirstCharge: "2025-01-31"})
	if d.Subscription.NextCharge.String() != "2026-11-30" {
		t.Fatalf("next charge = %s", d.Subscription.NextCharge)
	}
	if len(d.Upcoming) < 3 || d.Upcoming[1].DueDate.String() != "2026-12-31" || d.Upcoming[2].DueDate.String() != "2027-01-31" {
		t.Fatalf("upcoming = %+v", d.Upcoming)
	}
	if len(d.History) != 0 {
		t.Fatalf("past charges must not be invented, got %d", len(d.History))
	}
	if d.Subscription.MonthlyCost.String() != "17.99 EUR" || d.Subscription.Cadence != "every 1 month" {
		t.Fatalf("view = %+v", d.Subscription)
	}
}

func TestChangeDateThisChargeOnlyThenUndo(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Netflix", Price: "19.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-12"})
	nov := d.Upcoming[0]
	res, err := f.svc.ChangeChargeDate(ctx, f.owner, ChangeChargeDateInput{ChargeID: nov.ID, NewDate: "2026-11-14"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Charge.Delayed || res.Charge.DueDate.String() != "2026-11-14" || res.Charge.OriginalDate.String() != "2026-11-12" {
		t.Fatalf("moved charge = %+v", res.Charge)
	}
	after, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: d.Subscription.ID})
	if after.Upcoming[1].DueDate.String() != "2026-12-12" {
		t.Fatalf("next month must keep its date, got %s", after.Upcoming[1].DueDate)
	}
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditID: res.AuditID}); err != nil {
		t.Fatal(err)
	}
	after, _ = f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: d.Subscription.ID})
	if after.Upcoming[0].DueDate.String() != "2026-11-12" || after.Upcoming[0].Delayed {
		t.Fatalf("undo did not restore: %+v", after.Upcoming[0])
	}
}

func TestChangeDateThisAndFollowing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Gym", Price: "29.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-08"})
	res, err := f.svc.ChangeChargeDate(ctx, f.owner, ChangeChargeDateInput{ChargeID: d.Upcoming[0].ID, NewDate: "2026-11-15", Scope: "following"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Charge.DueDate.String() != "2026-11-15" || res.Charge.Delayed {
		t.Fatalf("re-anchored charge = %+v", res.Charge)
	}
	after, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: d.Subscription.ID})
	for i, want := range []string{"2026-11-15", "2026-12-15", "2027-01-15"} {
		if got := after.Upcoming[i].DueDate.String(); got != want {
			t.Fatalf("upcoming[%d] = %s, want %s", i, got, want)
		}
	}
}

func TestMarkPaidWithChargedAmountAndDryRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "GitHub Copilot", Price: "10.00", Currency: "USD", Unit: "month", FirstCharge: "2026-11-20"})
	id := d.Upcoming[0].ID
	prev, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: id, Status: "paid", ChargedAmount: "9.24", ChargedIn: "EUR", DryRun: true})
	if err != nil || prev.Charge.Status != domain.ChargePaid || !prev.DryRun {
		t.Fatalf("dry run = %+v, %v", prev, err)
	}
	now, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: d.Subscription.ID})
	if now.Upcoming[0].Status != domain.ChargePending {
		t.Fatal("dry run must not save")
	}
	res, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: id, Status: "paid", ChargedAmount: "9.24", ChargedIn: "EUR"})
	if err != nil || res.Charge.ChargedAmount.String() != "9.24 EUR" {
		t.Fatalf("paid = %+v, %v", res.Charge, err)
	}
	if _, err := f.svc.ChangeChargeDate(ctx, f.owner, ChangeChargeDateInput{ChargeID: id, NewDate: "2026-11-25"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("moving a paid charge must fail, got %v", err)
	}
}

func TestReadOnlyActorCannotWrite(t *testing.T) {
	f := newFixture(t)
	ro := f.owner
	ro.Kind, ro.Scope, ro.Label = ActorAgent, ScopeRead, "test agent"
	_, err := f.svc.CreateSubscription(context.Background(), ro, CreateSubscriptionInput{Name: "X", Price: "1", Currency: "EUR", Unit: "month", FirstCharge: "2026-12-01"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v", err)
	}
}

func TestCancelAtPeriodEndAndContext(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Revolut Premium", Price: "9.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-10-20"})
	res, err := f.svc.SetSubscriptionStatus(ctx, f.owner, SetSubscriptionStatusInput{ID: d.Subscription.ID, Action: "cancel_at_period_end"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Subscription.CancelledUntil.String() != "2026-11-19" || len(res.Upcoming) != 0 {
		t.Fatalf("cancel = until %s, upcoming %d", res.Subscription.CancelledUntil, len(res.Upcoming))
	}
	events, stop := f.bus.Subscribe(4)
	defer stop()
	f.create(t, CreateSubscriptionInput{Name: "iCloud+", Price: "2.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-14"})
	if e := <-events; e.Type != bus.SubscriptionCreated {
		t.Fatalf("event = %+v", e)
	}
	c, err := f.svc.BuildContext(ctx, f.owner, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"iCloud+", "2026-11-14", "Today: 2026-11-05", "dryRun"} {
		if !contains(c.Markdown, want) {
			t.Errorf("context lacks %q", want)
		}
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
