package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/domain"
)

// An App Store receipt is dated the day Apple charged (15 November) and
// shows the next renewal (9 December), while Exhale expected the charge on
// the 20th. These tests follow that receipt.

func appStoreFixture(t *testing.T) (*fixture, SubscriptionDetail) {
	t.Helper()
	f := newFixture(t)
	d := f.create(t, CreateSubscriptionInput{Name: "ChatGPT", Price: "19.99", Currency: "USD", Unit: "month", FirstCharge: "2026-11-20"})
	f.now = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	return f, d
}

func TestPaidOnAnotherDayMovesTheChargeAndUndoes(t *testing.T) {
	f, d := appStoreFixture(t)
	ctx := context.Background()
	nov := d.Upcoming[0]
	res, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: nov.ID, Status: "paid", ChargedOn: "2026-11-15"})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Charge
	if c.Status != domain.ChargePaid || c.DueDate.String() != "2026-11-15" || c.OriginalDate.String() != "2026-11-20" || !c.Delayed {
		t.Fatalf("paid charge = %+v", c)
	}
	after, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: d.Subscription.ID})
	if after.Subscription.NextCharge.String() != "2026-12-20" {
		t.Fatalf("next charge = %s, the schedule must not change", after.Subscription.NextCharge)
	}
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditID: res.AuditID}); err != nil {
		t.Fatal(err)
	}
	after, _ = f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: d.Subscription.ID})
	if u := after.Upcoming[0]; u.Status != domain.ChargePending || u.DueDate.String() != "2026-11-20" {
		t.Fatalf("undo did not restore: %+v", u)
	}
}

func TestPaidOnRefusesFutureDaysAndOtherStatuses(t *testing.T) {
	f, d := appStoreFixture(t)
	ctx := context.Background()
	id := d.Upcoming[0].ID
	for _, in := range []SetChargeStatusInput{
		{ChargeID: id, Status: "paid", ChargedOn: "2026-11-18"},
		{ChargeID: id, Status: "failed", ChargedOn: "2026-11-15"},
		{ChargeID: id, Status: "paid", ChargedOn: "15/11/2026"},
	} {
		if _, err := f.svc.SetChargeStatus(ctx, f.owner, in); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%+v: got %v, want a validation error", in, err)
		}
	}
	if _, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: id, Status: "paid", ChargedOn: "2026-11-17"}); err != nil {
		t.Fatalf("today must be accepted: %v", err)
	}
}

func TestNewBillingDayAfterPaidOnKeepsThePayment(t *testing.T) {
	f, d := appStoreFixture(t)
	ctx := context.Background()
	if _, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: d.Upcoming[0].ID, Status: "paid", ChargedOn: "2026-11-15"}); err != nil {
		t.Fatal(err)
	}
	next := "2026-12-09"
	after, err := f.svc.UpdateSubscription(ctx, f.owner, UpdateSubscriptionInput{ID: d.Subscription.ID, NextCharge: &next})
	if err != nil {
		t.Fatal(err)
	}
	if after.SkippedCycle != nil {
		t.Fatalf("nothing is skipped once November is paid: %+v", after.SkippedCycle)
	}
	if after.Subscription.NextCharge.String() != "2026-12-09" || after.Upcoming[1].DueDate.String() != "2027-01-09" {
		t.Fatalf("next = %s, upcoming = %+v", after.Subscription.NextCharge, after.Upcoming)
	}
	if len(after.History) != 1 || after.History[0].DueDate.String() != "2026-11-15" || after.History[0].Status != domain.ChargePaid {
		t.Fatalf("November's payment must stay: %+v", after.History)
	}
}

func TestNewBillingDayWarnsWhenACycleGetsNoCharge(t *testing.T) {
	f, d := appStoreFixture(t)
	ctx := context.Background()
	nov := d.Upcoming[0].ID
	// Moving November's open charge to the next renewal date leaves
	// November without a charge.
	res, err := f.svc.ChangeChargeDate(ctx, f.owner, ChangeChargeDateInput{ChargeID: nov, NewDate: "2026-12-09", Scope: "following", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if s := res.SkippedCycle; s == nil || s.Removed.String() != "2026-11-20" || s.Next.String() != "2026-12-09" {
		t.Fatalf("skipped cycle = %+v", s)
	}
	next := "2026-12-09"
	upd, err := f.svc.UpdateSubscription(ctx, f.owner, UpdateSubscriptionInput{ID: d.Subscription.ID, NextCharge: &next, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if s := upd.SkippedCycle; s == nil || s.Removed.String() != "2026-11-20" || s.Next.String() != "2026-12-09" {
		t.Fatalf("edit: skipped cycle = %+v", s)
	}
	// A few days later, or earlier, is a new billing day, not a lost cycle.
	for _, day := range []string{"2026-11-25", "2026-12-04", "2026-11-18"} {
		res, err := f.svc.ChangeChargeDate(ctx, f.owner, ChangeChargeDateInput{ChargeID: nov, NewDate: day, Scope: "following", DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		if res.SkippedCycle != nil {
			t.Fatalf("%s: unexpected skipped cycle %+v", day, res.SkippedCycle)
		}
	}
	// A one-off delay keeps the charge: never a skipped cycle.
	res, err = f.svc.ChangeChargeDate(ctx, f.owner, ChangeChargeDateInput{ChargeID: nov, NewDate: "2026-12-09", DryRun: true})
	if err != nil || res.SkippedCycle != nil {
		t.Fatalf("this charge only: %+v, %v", res.SkippedCycle, err)
	}
}

func TestChangePaidDateKeepsRenewalAndStatus(t *testing.T) {
	f, d := appStoreFixture(t)
	ctx := context.Background()
	id := d.Upcoming[0].ID
	paid, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: id, Status: "paid"})
	if err != nil {
		t.Fatal(err)
	}
	if paid.Charge.PaidOn.String() != "2026-11-20" {
		// Paid ahead of the date: paidOn is the due date until corrected.
		t.Fatalf("paidOn = %s", paid.Charge.PaidOn)
	}
	if _, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: id, Status: "paid"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("paying twice with nothing to correct must fail, got %v", err)
	}
	res, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: id, Status: "paid", ChargedOn: "2026-11-15"})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Charge
	if c.Status != domain.ChargePaid || c.OriginalDate.String() != "2026-11-20" || c.PaidOn.String() != "2026-11-15" || c.ResolvedAt == nil || !c.ResolvedAt.Equal(*paid.Charge.ResolvedAt) {
		t.Fatalf("corrected charge = %+v", c)
	}
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditID: res.AuditID}); err != nil {
		t.Fatal(err)
	}
	after, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: d.Subscription.ID})
	for _, h := range append(after.History, after.Upcoming...) {
		if h.ID == id && (h.PaidOn.String() != "2026-11-20" || h.Status != domain.ChargePaid) {
			t.Fatalf("undo: %+v", h)
		}
	}
}
