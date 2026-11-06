package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// ListSubscriptionsInput filters subscriptions.
type ListSubscriptionsInput struct {
	Status string `json:"status,omitempty" jsonschema:"active, paused, cancelled or empty for all" enum:"active,paused,cancelled,"`
	Query  string `json:"query,omitempty" jsonschema:"case-insensitive name filter"`
}

// SubscriptionList is a list of subscriptions.
type SubscriptionList struct {
	Subscriptions []SubscriptionView `json:"subscriptions"`
}

// ListSubscriptions returns the subscriptions visible to the actor.
func (s *Service) ListSubscriptions(ctx context.Context, actor Actor, in ListSubscriptionsInput) (SubscriptionList, error) {
	q := s.st.Q
	rows, err := q.ListSubscriptions(ctx, actor.HouseholdID)
	if err != nil {
		return SubscriptionList{}, err
	}
	today := s.today(ctx, q, actor)
	out := SubscriptionList{Subscriptions: []SubscriptionView{}}
	for _, r := range rows {
		sub, err := store.SubscriptionFromRow(r)
		if err != nil {
			return out, err
		}
		if !sub.VisibleTo(actor.MemberID) && actor.Kind != ActorSystem {
			continue
		}
		if in.Status != "" && string(sub.Status) != in.Status {
			continue
		}
		if in.Query != "" && !strings.Contains(strings.ToLower(sub.Name), strings.ToLower(in.Query)) {
			continue
		}
		out.Subscriptions = append(out.Subscriptions, s.view(ctx, q, sub, today))
	}
	return out, nil
}

// GetSubscriptionInput identifies a subscription.
type GetSubscriptionInput struct {
	ID string `json:"id" jsonschema:"subscription id (sub_…)"`
}

// GetSubscription returns a subscription with upcoming and past charges.
func (s *Service) GetSubscription(ctx context.Context, actor Actor, in GetSubscriptionInput) (SubscriptionDetail, error) {
	return s.detail(ctx, s.st.Q, actor, in.ID)
}

// ListChargesInput selects charges by due date.
type ListChargesInput struct {
	From string `json:"from,omitempty" jsonschema:"first due date, YYYY-MM-DD (default today)"`
	To   string `json:"to,omitempty" jsonschema:"last due date, YYYY-MM-DD (default from + 30 days)"`
}

// ChargeList is a list of charges.
type ChargeList struct {
	From    engine.Date  `json:"from"`
	To      engine.Date  `json:"to"`
	Charges []ChargeView `json:"charges"`
}

// ListCharges returns charges due in a date range, e.g. the next 30 days.
func (s *Service) ListCharges(ctx context.Context, actor Actor, in ListChargesInput) (ChargeList, error) {
	q := s.st.Q
	today := s.today(ctx, q, actor)
	from, to := today, engine.Date{}
	var err error
	if in.From != "" {
		if from, err = engine.ParseDate(in.From); err != nil {
			return ChargeList{}, fmt.Errorf("%w: from: %v", domain.ErrValidation, err)
		}
	}
	if to = from.AddDays(30); in.To != "" {
		if to, err = engine.ParseDate(in.To); err != nil {
			return ChargeList{}, fmt.Errorf("%w: to: %v", domain.ErrValidation, err)
		}
	}
	rows, err := q.ListChargesBetween(ctx, db.ListChargesBetweenParams{HouseholdID: actor.HouseholdID, FromDate: from.String(), ToDate: to.String()})
	if err != nil {
		return ChargeList{}, err
	}
	return ChargeList{From: from, To: to, Charges: s.chargeViews(ctx, q, actor, rows, today)}, nil
}

// AttentionList is what needs the user's attention.
type AttentionList struct {
	Unconfirmed            []ChargeView           `json:"unconfirmed" jsonschema:"charges whose due date passed and that are not marked paid, failed or skipped"`
	ExpiringPaymentMethods []domain.PaymentMethod `json:"expiringPaymentMethods" jsonschema:"payment methods expiring within 45 days"`
}

// NeedsAttention lists unconfirmed past charges and expiring payment methods.
func (s *Service) NeedsAttention(ctx context.Context, actor Actor, _ struct{}) (AttentionList, error) {
	q := s.st.Q
	today := s.today(ctx, q, actor)
	rows, err := q.ListOpenChargesDueBefore(ctx, db.ListOpenChargesDueBeforeParams{HouseholdID: actor.HouseholdID, Before: today.String()})
	if err != nil {
		return AttentionList{}, err
	}
	out := AttentionList{Unconfirmed: s.chargeViews(ctx, q, actor, rows, today), ExpiringPaymentMethods: []domain.PaymentMethod{}}
	pms, err := q.ListPaymentMethods(ctx, actor.HouseholdID)
	if err != nil {
		return out, err
	}
	for _, r := range pms {
		pm := store.PaymentMethodFromRow(r)
		if !pm.ExpiresOn.IsZero() && today.DaysUntil(pm.ExpiresOn) <= 45 {
			out.ExpiringPaymentMethods = append(out.ExpiringPaymentMethods, pm)
		}
	}
	return out, nil
}

func (s *Service) chargeViews(ctx context.Context, q *db.Queries, actor Actor, rows []db.Charge, today engine.Date) []ChargeView {
	views := map[string]*SubscriptionView{}
	out := []ChargeView{}
	for _, r := range rows {
		v, ok := views[r.SubscriptionID]
		if !ok {
			sub, err := s.subscription(ctx, q, actor, r.SubscriptionID)
			if err != nil {
				views[r.SubscriptionID] = nil
				continue
			}
			sv := s.view(ctx, q, sub, today)
			v = &sv
			views[r.SubscriptionID] = v
		}
		if v == nil {
			continue // not visible to this actor
		}
		out = append(out, s.chargeView(store.ChargeFromRow(r), *v, today))
	}
	return out
}

// PreviewScheduleInput describes a billing rule to preview.
type PreviewScheduleInput struct {
	Interval    int    `json:"interval" minimum:"1"`
	Unit        string `json:"unit" enum:"day,week,month,year"`
	FirstCharge string `json:"firstCharge" jsonschema:"YYYY-MM-DD"`
	Count       int    `json:"count,omitempty" jsonschema:"how many dates, default 12" maximum:"60"`
}

// SchedulePreview lists the computed charge dates.
type SchedulePreview struct {
	Dates []engine.Date `json:"dates"`
}

// PreviewSchedule computes charge dates with the billing engine. The web
// calls it while a recurrence is being edited, so dates are never computed
// in two places (ADR 0013).
func (s *Service) PreviewSchedule(_ context.Context, _ Actor, in PreviewScheduleInput) (SchedulePreview, error) {
	first, err := engine.ParseDate(in.FirstCharge)
	if err != nil {
		return SchedulePreview{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	if in.Interval == 0 {
		in.Interval = 1
	}
	sched, err := engine.NewSchedule(engine.Rule{Interval: in.Interval, Unit: engine.Unit(in.Unit), Anchor: first})
	if err != nil {
		return SchedulePreview{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	n := in.Count
	if n <= 0 || n > 60 {
		n = 12
	}
	return SchedulePreview{Dates: sched.Upcoming(first, n)}, nil
}

// SpendingSummary totals recurring spend.
type SpendingSummary struct {
	MainCurrency string          `json:"mainCurrency"`
	Monthly      []money.Money   `json:"monthlyByCurrency" jsonschema:"active subscriptions normalized per month, one entry per currency"`
	Yearly       []money.Money   `json:"yearlyByCurrency"`
	ByCategory   []CategorySpend `json:"byCategory" jsonschema:"monthly spend per category (per currency)"`
	Active       int             `json:"activeSubscriptions"`
	NextCharges  []ChargeView    `json:"next7Days"`
}

// CategorySpend is the monthly spend of one category in one currency.
type CategorySpend struct {
	Category string      `json:"category"`
	Monthly  money.Money `json:"monthly"`
}

// Summary returns recurring spend per month and year, per currency and per
// category, plus the charges of the next 7 days.
func (s *Service) Summary(ctx context.Context, actor Actor, _ struct{}) (SpendingSummary, error) {
	list, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{Status: string(domain.StatusActive)})
	if err != nil {
		return SpendingSummary{}, err
	}
	h, err := s.household(ctx, s.st.Q, actor.HouseholdID)
	if err != nil {
		return SpendingSummary{}, err
	}
	monthly := map[string]money.Money{}
	byCat := map[[2]string]money.Money{}
	for _, v := range list.Subscriptions {
		cur := v.MonthlyCost.Currency
		monthly[cur], _ = addOrInit(monthly[cur], v.MonthlyCost)
		cat := v.CategoryName
		if cat == "" {
			cat = "Uncategorized"
		}
		k := [2]string{cat, cur}
		byCat[k], _ = addOrInit(byCat[k], v.MonthlyCost)
	}
	out := SpendingSummary{MainCurrency: h.Currency, Active: len(list.Subscriptions), Monthly: []money.Money{}, Yearly: []money.Money{}, ByCategory: []CategorySpend{}}
	for _, cur := range sortedKeys(monthly) {
		out.Monthly = append(out.Monthly, monthly[cur])
		y, _ := monthly[cur].Scale("12")
		out.Yearly = append(out.Yearly, y)
	}
	for k, m := range byCat {
		out.ByCategory = append(out.ByCategory, CategorySpend{Category: k[0], Monthly: m})
	}
	slices.SortFunc(out.ByCategory, func(a, b CategorySpend) int { return int(b.Monthly.Minor - a.Monthly.Minor) })
	today := s.today(ctx, s.st.Q, actor)
	next, err := s.ListCharges(ctx, actor, ListChargesInput{From: today.String(), To: today.AddDays(7).String()})
	if err != nil {
		return out, err
	}
	out.NextCharges = next.Charges
	return out, nil
}

func addOrInit(acc, m money.Money) (money.Money, error) {
	if acc.IsZero() {
		return m, nil
	}
	return acc.Add(m)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
