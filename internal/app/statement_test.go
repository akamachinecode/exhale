package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/money"
)

// TestMatchStatement: two months of a card statement confirm the charges
// Exhale expected. A charge already paid is reported as such, another
// amount is a possible new price, a payment too far from the due date or
// from another merchant matches nothing, and an expected charge of the same
// card that is not in the statement is listed.
func TestMatchStatement(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.now = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	card, err := f.svc.SavePaymentMethod(ctx, f.owner, SavePaymentMethodInput{Name: "Visa", Kind: "card"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.svc.SavePaymentMethod(ctx, f.owner, SavePaymentMethodInput{Name: "Revolut", Kind: "card"})
	if err != nil {
		t.Fatal(err)
	}
	netflix := f.create(t, CreateSubscriptionInput{Name: "Netflix", Price: "12.99", Currency: "EUR", FirstCharge: "2026-09-06", PaymentMethodID: card.ID})
	f.create(t, CreateSubscriptionInput{Name: "Spotify Premium", Price: "17.99", Currency: "EUR", FirstCharge: "2026-09-28", PaymentMethodID: card.ID})
	gym := f.create(t, CreateSubscriptionInput{Name: "Gym", Price: "30.00", Currency: "EUR", FirstCharge: "2026-10-01", PaymentMethodID: card.ID})
	f.create(t, CreateSubscriptionInput{Name: "Phone", Price: "15.00", Currency: "EUR", FirstCharge: "2026-10-10", PaymentMethodID: other.ID})
	f.create(t, CreateSubscriptionInput{Name: "Coffee Club", Price: "5.00", Currency: "EUR", Interval: 1, Unit: "week", FirstCharge: "2026-10-05"})
	if _, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: netflix.Upcoming[0].ID, Status: "paid"}); err != nil {
		t.Fatal(err)
	}
	f.now = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

	res, err := f.svc.MatchStatement(ctx, f.owner, MatchStatementInput{Transactions: []StatementLine{
		{Date: "2026-09-06", Description: "NETFLIX.COM 866-579-7172", Amount: "12.99"},
		{Date: "2026-09-29", Description: "SPOTIFY P6A2B3C4D", Amount: "17.99"},
		{Date: "2026-10-07", Description: "NETFLIX.COM 866-579-7172", Amount: "15.99"},
		{Date: "2026-10-15", Description: "SPOTIFY P6A2B3C4D", Amount: "17.99"}, // 13 days early: not the charge
		{Date: "2026-10-12", Description: "COFFEE CLUB", Amount: "5.00"},
		{Date: "2026-10-14", Description: "MERCADONA AV DIAGONAL", Amount: "41.16"},
		{Date: "2026-10-20", Description: "SALARY", Amount: "-2450.00"}, // money in: ignored
		{Date: "2026-10-28", Description: "Spotify", Amount: "17.99", Currency: "eur"},
		{Date: "2026-10-31", Description: "NETFLIX REFUND", Amount: "0"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.From.String() != "2026-09-06" || res.To.String() != "2026-10-31" {
		t.Errorf("period %s – %s", res.From, res.To)
	}
	type want struct{ sub, due, action, paid string }
	wants := []want{
		{"Netflix", "2026-09-06", MatchAlreadyPaid, "12.99"},
		{"Spotify Premium", "2026-09-28", MatchMarkPaid, "17.99"},
		{"Netflix", "2026-10-06", MatchMarkPaid, "15.99"},
		{"Coffee Club", "2026-10-12", MatchMarkPaid, "5.00"},
		{"Spotify Premium", "2026-10-28", MatchMarkPaid, "17.99"},
	}
	if len(res.Matches) != len(wants) {
		for _, m := range res.Matches {
			t.Logf("%s %s %s %s", m.Charge.SubscriptionName, m.Charge.DueDate, m.Action, m.Paid.Decimal())
		}
		t.Fatalf("%d matches, want %d", len(res.Matches), len(wants))
	}
	for i, w := range wants {
		m := res.Matches[i]
		if m.Charge.SubscriptionName != w.sub || m.Charge.DueDate.String() != w.due || m.Action != w.action || m.Paid.Decimal() != w.paid {
			t.Errorf("match %d: %s %s %s %s, want %+v", i, m.Charge.SubscriptionName, m.Charge.DueDate, m.Action, m.Paid.Decimal(), w)
		}
	}
	rise := res.Matches[2]
	if rise.PriceChange == nil || rise.PriceChange.Expected != money.MustParse("12.99", "EUR") || rise.PriceChange.Charged != money.MustParse("15.99", "EUR") {
		t.Errorf("a different amount is a possible new price: %+v", rise.PriceChange)
	}
	if res.Matches[1].PriceChange != nil {
		t.Error("the same amount is not a new price")
	}
	// Gym (same card) was expected on 1 October and is not there. The
	// phone is paid with another card: this statement cannot show it. The
	// weekly club's other three weeks have no payment method: expected too.
	missing := map[string]int{}
	for _, c := range res.Missing {
		missing[c.SubscriptionName]++
		if c.SubscriptionID == gym.Subscription.ID && c.DueDate.String() != "2026-10-01" {
			t.Errorf("gym charge %s", c.DueDate)
		}
	}
	if missing["Gym"] != 1 || missing["Phone"] != 0 || missing["Netflix"] != 0 || missing["Coffee Club"] != 3 || missing["Spotify Premium"] != 0 {
		t.Errorf("missing: %v", missing)
	}
	if res.Unmatched != 2 {
		t.Errorf("unmatched %d, want 2 (the early Spotify and the shop)", res.Unmatched)
	}

	// Applying a match is an ordinary set_charge_status.
	m := res.Matches[2]
	paid, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: m.Charge.ID, Status: "paid", ChargedAmount: m.Paid.Decimal(), ChargedIn: m.Paid.Currency})
	if err != nil || paid.Charge.Status != domain.ChargePaid || paid.PriceChange == nil {
		t.Fatalf("apply: %+v %v", paid, err)
	}
	again, err := f.svc.MatchStatement(ctx, f.owner, MatchStatementInput{Transactions: []StatementLine{{Date: "2026-10-07", Description: "NETFLIX.COM", Amount: "15.99"}}})
	if err != nil || len(again.Matches) != 1 || again.Matches[0].Action != MatchAlreadyPaid {
		t.Fatalf("matched twice: %+v %v", again, err)
	}

	for name, in := range map[string]MatchStatementInput{
		"bad date":   {Transactions: []StatementLine{{Date: "6 Oct", Description: "Netflix", Amount: "1"}}},
		"bad amount": {Transactions: []StatementLine{{Date: "2026-10-06", Description: "Netflix", Amount: "lots"}}},
		"to < from":  {Transactions: []StatementLine{{Date: "2026-10-06", Description: "Netflix", Amount: "1"}}, From: "2026-10-31", To: "2026-10-01"},
	} {
		if _, err := f.svc.MatchStatement(ctx, f.owner, in); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	empty, err := f.svc.MatchStatement(ctx, f.owner, MatchStatementInput{})
	if err != nil || len(empty.Matches) != 0 || empty.Missing == nil {
		t.Errorf("an empty statement: %+v %v", empty, err)
	}
}

// TestMatchStatementPrivacy: another member's private subscription is not
// matched nor listed.
func TestMatchStatementPrivacy(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.now = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	f.create(t, CreateSubscriptionInput{Name: "Dating app", Price: "9.99", Currency: "EUR", FirstCharge: "2026-10-03", Private: true})
	f.now = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	samID := domain.NewID(domain.PrefixMember)
	if err := f.svc.st.Q.CreateMember(ctx, memberParams(samID, f.owner.HouseholdID, "Sam")); err != nil {
		t.Fatal(err)
	}
	partner, err := f.svc.ActorForMember(ctx, samID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.MatchStatement(ctx, partner, MatchStatementInput{Transactions: []StatementLine{
		{Date: "2026-10-03", Description: "DATING APP", Amount: "9.99"},
		{Date: "2026-10-20", Description: "SHOP", Amount: "9.99"},
	}})
	if err != nil || len(res.Matches) != 0 || len(res.Missing) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}
