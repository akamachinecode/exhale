package app

import (
	"context"
	"slices"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/money"
)

// Breakdown is what active subscriptions cost per month or per year, in the
// main currency, grouped by category, payment method or subscription. The
// assistant shows it as a screen (ADR 0019 §4); it is not an op of its
// own: get_summary answers the same question for agents.
type Breakdown struct {
	Period      string         `json:"period" enum:"month,year"`
	By          string         `json:"by" enum:"category,payment_method,subscription"`
	Total       money.Money    `json:"total" doc:"in the main currency"`
	Rows        []BreakdownRow `json:"rows" doc:"largest first"`
	Converted   bool           `json:"converted" doc:"other currencies were converted at the latest ECB reference rate"`
	Unconverted []string       `json:"unconverted" doc:"currencies without a known rate, left out"`
}

// BreakdownRow is one group. Label is empty for subscriptions without a
// category or a payment method: the screen names that in its language.
type BreakdownRow struct {
	Label          string      `json:"label"`
	SubscriptionID string      `json:"subscriptionId,omitempty" doc:"when the breakdown is by subscription"`
	Amount         money.Money `json:"amount"`
	Count          int         `json:"count"`
}

// SpendBreakdown computes a Breakdown of the active subscriptions the actor
// can see; ids, when given, keep only those subscriptions.
func (s *Service) SpendBreakdown(ctx context.Context, actor Actor, period, by string, ids []string) (Breakdown, error) {
	if period != "year" {
		period = "month"
	}
	switch by {
	case "category", "payment_method", "subscription":
	default:
		return Breakdown{}, domain.Invalid("unknown breakdown %q; use category, payment_method or subscription", by)
	}
	list, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{Status: string(domain.StatusActive)})
	if err != nil {
		return Breakdown{}, err
	}
	h, err := s.household(ctx, s.st.Q, actor.HouseholdID)
	if err != nil {
		return Breakdown{}, err
	}
	keep := map[string]bool{}
	for _, id := range ids {
		keep[id] = true
	}
	conv := s.converter(h.Currency, s.today(ctx, s.st.Q, actor).String())
	zero := money.Money{Currency: h.Currency}
	out := Breakdown{Period: period, By: by, Total: zero, Rows: []BreakdownRow{}, Unconverted: []string{}}
	groups := map[string]*BreakdownRow{}
	var order []string
	missing := map[string]bool{}
	for _, v := range list.Subscriptions {
		if len(keep) > 0 && !keep[v.ID] {
			continue
		}
		cost := v.MonthlyCost
		if period == "year" {
			cost = v.YearlyCost
		}
		main, ok := conv.toMain(ctx, cost)
		if !ok {
			missing[cost.Currency] = true
			continue
		}
		if cost.Currency != h.Currency {
			out.Converted = true
		}
		out.Total, _ = out.Total.Add(main)
		key, label, subID := v.CategoryName, v.CategoryName, ""
		switch by {
		case "payment_method":
			key, label = v.PaymentMethod, v.PaymentMethod
		case "subscription":
			key, label, subID = v.ID, v.Name, v.ID
		}
		g := groups[key]
		if g == nil {
			g = &BreakdownRow{Label: label, SubscriptionID: subID, Amount: zero}
			groups[key] = g
			order = append(order, key)
		}
		g.Amount, _ = g.Amount.Add(main)
		g.Count++
	}
	for _, k := range order {
		out.Rows = append(out.Rows, *groups[k])
	}
	slices.SortStableFunc(out.Rows, func(a, b BreakdownRow) int {
		switch {
		case a.Amount.Minor > b.Amount.Minor:
			return -1
		case a.Amount.Minor < b.Amount.Minor:
			return 1
		}
		return 0
	})
	out.Unconverted = sortedKeys(missing)
	return out, nil
}
