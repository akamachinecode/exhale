package app

import (
	"context"
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store/db"
)

func TestPriceHistoryComesFromTheLedger(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Netflix", Price: "13.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-06"})
	// November is paid at the old price; the bank takes more in December.
	if _, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: d.Upcoming[0].ID, Status: "paid"}); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: d.Upcoming[1].ID, Status: "paid", ChargedAmount: "15.99"})
	if err != nil {
		t.Fatal(err)
	}
	if res.PriceChange == nil || res.PriceChange.Change != "+14.3" || res.PriceChange.YearlyDelta.String() != "24.00 EUR" {
		t.Fatalf("price change hint = %+v", res.PriceChange)
	}
	// The member confirms it is the new price.
	price := "15.99"
	got, err := f.svc.UpdateSubscription(ctx, f.owner, UpdateSubscriptionInput{ID: d.Subscription.ID, Price: &price})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PriceHistory) != 2 || got.PriceHistory[1].Change != "+14.3" || got.PriceHistory[1].Price.String() != "15.99 EUR" {
		t.Fatalf("price history = %+v", got.PriceHistory)
	}
	if got.Paid.Count != 2 || got.Paid.Total.String() != "29.98 EUR" || got.Paid.Since.String() != "2026-11-06" {
		t.Fatalf("paid totals = %+v", got.Paid)
	}
	audit, err := f.svc.Audit(ctx, f.owner, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.PriceIncreases) != 1 || audit.PriceIncreases[0].YearlyDelta.String() != "24.00 EUR" {
		t.Fatalf("audit price increases = %+v", audit.PriceIncreases)
	}
}

func TestSpendHistorySeparatesPaidFromExpected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Spotify", Price: "10.00", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-06"})
	f.create(t, CreateSubscriptionInput{Name: "Domain", Price: "120.00", Currency: "EUR", Unit: "year", FirstCharge: "2027-03-10"})
	if _, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: d.Upcoming[0].ID, Status: "paid"}); err != nil {
		t.Fatal(err)
	}
	h, err := f.svc.SpendHistory(ctx, f.owner, SpendingHistoryInput{Months: 6})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]MonthSpend{}
	for _, m := range h.Months {
		by[m.Month] = m
	}
	if len(h.Months) != 12 || by["2026-11"].Paid.String() != "10.00 EUR" || by["2026-11"].Expected.Minor != 0 {
		t.Fatalf("november = %+v (months %d)", by["2026-11"], len(h.Months))
	}
	if by["2026-12"].Expected.String() != "10.00 EUR" || by["2027-03"].Expected.String() != "130.00 EUR" {
		t.Fatalf("december = %+v march = %+v", by["2026-12"], by["2027-03"])
	}
	// April is beyond the materialized horizon: it is projected from the schedule.
	if by["2027-04"].Expected.String() != "10.00 EUR" {
		t.Fatalf("april = %+v", by["2027-04"])
	}
	if h.HeaviestMonth != "2027-03" || h.TotalPaid.String() != "10.00 EUR" || len(h.BySubscription) != 1 {
		t.Fatalf("history = %+v", h)
	}
}

func TestBalancesSplitSharedCosts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ana, err := f.svc.st.Q.ListMembers(ctx, f.owner.HouseholdID)
	if err != nil || len(ana) != 1 {
		t.Fatal(err)
	}
	anaID := domain.NewID(domain.PrefixMember)
	if err := f.svc.st.Q.CreateMember(ctx, memberParams(anaID, f.owner.HouseholdID, "Ana")); err != nil {
		t.Fatal(err)
	}
	d := f.create(t, CreateSubscriptionInput{Name: "Spotify Family", Price: "17.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-06",
		TermsInput: TermsInput{Shares: []domain.Share{{MemberID: f.owner.MemberID, Weight: 1}, {MemberID: anaID, Weight: 1}}}})
	f.create(t, CreateSubscriptionInput{Name: "Alex only", Price: "5.00", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-07"})
	if _, err := f.svc.SetChargeStatus(ctx, f.owner, SetChargeStatusInput{ChargeID: d.Upcoming[0].ID, Status: "paid"}); err != nil {
		t.Fatal(err)
	}
	b, err := f.svc.MonthBalances(ctx, f.owner, BalancesInput{Month: "2026-11"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Transfers) != 1 || b.Transfers[0].Text != "Ana pays Alex 9.00 EUR" {
		t.Fatalf("transfers = %+v", b.Transfers)
	}
	total := int64(0)
	for _, m := range b.Members {
		total += m.Share.Minor
	}
	if total != 1799 || len(b.Charges) != 1 {
		t.Fatalf("shares must add up to the charge: %d, charges %d", total, len(b.Charges))
	}
}

func TestAuditFindsWhatMatters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	card, err := f.svc.SavePaymentMethod(ctx, f.owner, SavePaymentMethodInput{Name: "Visa", Kind: "card", Last4: "4421", ExpiresOn: "2026-11-30"})
	if err != nil {
		t.Fatal(err)
	}
	f.create(t, CreateSubscriptionInput{Name: "Spotify Family", Price: "17.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20", PaymentMethodID: card.ID})
	f.create(t, CreateSubscriptionInput{Name: "Spotify", Price: "10.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-22"})
	f.create(t, CreateSubscriptionInput{Name: "Notion AI", Price: "10.00", Currency: "USD", Unit: "month", TermsInput: TermsInput{TrialEndsOn: "2026-11-12"}})
	f.create(t, CreateSubscriptionInput{Name: "Gym", Price: "39.00", Currency: "EUR", Unit: "year", FirstCharge: "2026-12-20", TermsInput: TermsInput{NoticeDays: 30}})
	f.create(t, CreateSubscriptionInput{Name: "example.com", Price: "12.00", Currency: "EUR", Unit: "year", FirstCharge: "2026-12-01", TermsInput: TermsInput{Renewal: "manual"}})
	f.create(t, CreateSubscriptionInput{Name: "iCloud+", Price: "35.88", Currency: "EUR", Unit: "year", FirstCharge: "2026-11-25"})
	a, err := f.svc.Audit(ctx, f.owner, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	if len(a.Duplicates) == 1 && len(a.Duplicates[0].Subscriptions) == 2 {
		got = append(got, "duplicates")
	}
	if len(a.TrialsEnding) == 1 && a.TrialsEnding[0].SubscriptionName == "Notion AI" {
		got = append(got, "trial")
	}
	if len(a.CancelDeadlines) == 1 && a.CancelDeadlines[0].CancelBy.String() == "2026-11-20" {
		got = append(got, "deadline")
	}
	if len(a.Expiring) == 1 && a.Expiring[0].SubscriptionName == "example.com" {
		got = append(got, "expiring")
	}
	if len(a.BigCharges) == 1 && a.BigCharges[0].SubscriptionName == "iCloud+" {
		got = append(got, "big")
	}
	if len(a.CardsExpiring) == 1 && len(a.CardsExpiring[0].Subscriptions) == 1 {
		got = append(got, "card")
	}
	if want := "duplicates trial deadline expiring big card"; strings.Join(got, " ") != want {
		t.Fatalf("audit found %q, want %q\n%+v", strings.Join(got, " "), want, a)
	}
	att, _ := f.svc.NeedsAttention(ctx, f.owner, struct{}{})
	if len(att.Deadlines) != 1 || att.Deadlines[0].SubscriptionName != "Notion AI" {
		t.Fatalf("attention deadlines = %+v", att.Deadlines)
	}
}

func TestSummaryPlansAhead(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, CreateSubscriptionInput{Name: "Spotify", Price: "10.00", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20"})
	f.create(t, CreateSubscriptionInput{Name: "Insurance", Price: "240.00", Currency: "EUR", Unit: "year", FirstCharge: "2027-06-01"})
	f.create(t, CreateSubscriptionInput{Name: "Late", Price: "7.00", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-29"})
	payday := 28
	if _, err := f.svc.UpdateHousehold(ctx, f.owner, UpdateHouseholdInput{Payday: &payday}); err != nil {
		t.Fatal(err)
	}
	s, err := f.svc.Summary(ctx, f.owner, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if s.SetAside.String() != "20.00 EUR" {
		t.Fatalf("set aside = %s", s.SetAside)
	}
	if s.BeforePayday == nil || s.BeforePayday.Payday.String() != "2026-11-28" || s.BeforePayday.Due.String() != "10.00 EUR" {
		t.Fatalf("before payday = %+v", s.BeforePayday)
	}
}

func memberParams(id, household, name string) db.CreateMemberParams {
	return db.CreateMemberParams{ID: id, HouseholdID: household, Name: name, Role: "member", CreatedAt: "2026-11-05T00:00:00Z"}
}
