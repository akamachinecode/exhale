package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/domain"
)

func TestTrialDefaultsFirstChargeAndMarksTheCharge(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, CreateSubscriptionInput{Name: "Notion AI", Price: "10.00", Currency: "USD", Unit: "month",
		TermsInput: TermsInput{TrialEndsOn: "2026-11-19"}})
	if !d.Subscription.InTrial || d.Subscription.NextCharge.String() != "2026-11-19" {
		t.Fatalf("trial view = %+v", d.Subscription)
	}
	if d.Upcoming[0].Kind != ChargeTrial || d.Upcoming[1].Kind != ChargeRenewal {
		t.Fatalf("kinds = %s, %s", d.Upcoming[0].Kind, d.Upcoming[1].Kind)
	}
	if _, err := f.svc.CreateSubscription(context.Background(), f.owner, CreateSubscriptionInput{Name: "X", Price: "1", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-10", TermsInput: TermsInput{TrialEndsOn: "2026-11-19"}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a first charge before the trial ends must be refused, got %v", err)
	}
}

func TestNoticePeriodPlansDecisionReminders(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Gym", Price: "39.00", Currency: "EUR", Unit: "year", FirstCharge: "2026-12-20",
		TermsInput: TermsInput{NoticeDays: 30, CancelURL: "https://gym.example/cancel"}})
	if got := d.Subscription.CancelBy.String(); got != "2026-11-20" {
		t.Fatalf("cancel by = %s", got)
	}
	if err := f.svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := f.svc.st.Q.ListOutbox(ctx, 50)
	due := map[string]string{}
	for _, r := range rows {
		if r.Kind == KindDecision {
			due[r.DedupeKey[strings.LastIndex(r.DedupeKey, ":")+1:]] = r.DueAt[:10] + " " + r.Status
		}
	}
	if due["7"] != "2026-11-13 pending" || due["0"] != "2026-11-20 pending" {
		t.Fatalf("decision reminders = %v", due)
	}
	// Keeping it stops the questions; the normal renewal reminders stay.
	res, err := f.svc.DecideRenewal(ctx, f.owner, DecideRenewalInput{ChargeID: d.Upcoming[0].ID, Decision: "keep"})
	if err != nil || res.Charge.Decision != "keep" {
		t.Fatalf("decide: %v %+v", err, res.Charge)
	}
	if err := f.svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ = f.svc.st.Q.ListOutbox(ctx, 50)
	reminders := 0
	for _, r := range rows {
		if r.Kind == KindDecision && r.Status == "pending" {
			t.Fatalf("decision reminder still pending after keeping: %s", r.DedupeKey)
		}
		if r.Kind == KindReminder && r.Status == "pending" {
			reminders++
		}
	}
	if reminders == 0 {
		t.Fatal("renewal reminders must survive the decision")
	}
	// Undo brings the question back.
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditID: res.AuditID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: d.Subscription.ID}); got.Upcoming[0].Decision != "" {
		t.Fatal("undo did not clear the decision")
	}
}

func TestRecordingASubscriptionThatAlreadyEnded(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, CreateSubscriptionInput{Name: "Spotify", Price: "13.59", Currency: "USD", Unit: "month",
		FirstCharge: "2023-11-28", EndDate: "2026-02-08", Status: "cancelled", TermsInput: TermsInput{StartedOn: "2023-11-28"}})
	s := d.Subscription
	if s.Status != domain.StatusCancelled || s.CancelledUntil.String() != "2026-02-08" || len(d.Upcoming) != 0 || len(d.History) != 0 {
		t.Fatalf("ended subscription = %+v upcoming=%d history=%d", s, len(d.Upcoming), len(d.History))
	}
	if s.StartedOn.String() != "2023-11-28" {
		t.Fatalf("started on = %s", s.StartedOn)
	}
}

func TestUnknownReferencesFailWithAClearMessage(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.CreateSubscription(context.Background(), f.owner, CreateSubscriptionInput{Name: "X", Price: "1", Currency: "EUR", Unit: "month", FirstCharge: "2026-12-01", PaymentMethodID: "pm_fromadryrun"})
	if !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "payment method pm_fromadryrun does not exist") {
		t.Fatalf("err = %v", err)
	}
	d := f.create(t, CreateSubscriptionInput{Name: "Y", Price: "1", Currency: "EUR", Unit: "month", FirstCharge: "2026-12-01"})
	bad := "cat_nope"
	if _, err := f.svc.UpdateSubscription(context.Background(), f.owner, UpdateSubscriptionInput{ID: d.Subscription.ID, CategoryID: &bad}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v", err)
	}
}

func TestPriceChangeAnnouncedInAdvance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Netflix", Price: "13.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-12"})
	price, from := "15.99", "2027-01-01"
	got, err := f.svc.UpdateSubscription(ctx, f.owner, UpdateSubscriptionInput{ID: d.Subscription.ID, Price: &price, PriceFrom: &from})
	if err != nil {
		t.Fatal(err)
	}
	if got.Subscription.Price.String() != "13.99 EUR" || got.Subscription.NextPrice == nil {
		t.Fatalf("the current price must stay until the date: %+v", got.Subscription)
	}
	amounts := []string{}
	for _, c := range got.Upcoming[:3] {
		amounts = append(amounts, c.DueDate.String()+"="+c.Amount.String())
	}
	if want := "2026-11-12=13.99 EUR 2026-12-12=13.99 EUR 2027-01-12=15.99 EUR"; strings.Join(amounts, " ") != want {
		t.Fatalf("amounts = %v", amounts)
	}
	// When the day arrives the plan promotes the new price.
	f.now = f.now.AddDate(0, 2, 0)
	if err := f.svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: d.Subscription.ID})
	if after.Subscription.Price.String() != "15.99 EUR" || after.Subscription.NextPrice != nil {
		t.Fatalf("after the date: %+v", after.Subscription)
	}
}

func TestManualRenewalIsNeverAutoConfirmed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "example.com", Price: "12.00", Currency: "EUR", Unit: "year", FirstCharge: "2026-11-05",
		AutoConfirm: true, TermsInput: TermsInput{Renewal: "manual"}})
	if !d.Subscription.Expires || d.Upcoming[0].Kind != ChargeExpiry {
		t.Fatalf("view = %+v", d.Subscription)
	}
	if err := f.svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.GetSubscription(ctx, f.owner, GetSubscriptionInput{ID: d.Subscription.ID})
	if got.Upcoming[0].Status != domain.ChargePending {
		t.Fatalf("a domain does not renew by itself; status = %s", got.Upcoming[0].Status)
	}
}
