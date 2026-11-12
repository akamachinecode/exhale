package app

import (
	"context"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
)

// A glance: what the menu bar icon shows (ADR 0032). Amounts come written
// for the member's language, so a small client does not format money.

// GlanceInput says how much to show.
type GlanceInput struct {
	Limit int `query:"limit" json:"limit,omitempty" minimum:"0" maximum:"20" jsonschema:"how many charges, default 5"`
}

// Glance is what comes next.
type Glance struct {
	Language string         `json:"language" jsonschema:"the member's language, for the client's own words"`
	Today    engine.Date    `json:"today"`
	Charges  []GlanceCharge `json:"charges" jsonschema:"the next open charges of the next 30 days, soonest first"`
	Totals   []string       `json:"totals" jsonschema:"what the next 30 days cost, one written amount per currency"`
	Awake    KeepAwakeInfo  `json:"keepAwake"`
	Overdue  int            `json:"overdue" jsonschema:"charges whose date passed without being confirmed"`
}

// GlanceCharge is one charge, written out.
type GlanceCharge struct {
	SubscriptionID string      `json:"subscriptionId"`
	Name           string      `json:"name"`
	Amount         string      `json:"amount" jsonschema:"written for the member's language, e.g. 12,99 €"`
	Date           engine.Date `json:"date"`
	DaysUntil      int         `json:"daysUntil"`
	Kind           string      `json:"kind" enum:"renewal,trial,expiry"`
}

// Glance returns the next charges, what the next 30 days cost and whether
// this computer is kept awake.
func (s *Service) Glance(ctx context.Context, actor Actor, in GlanceInput) (Glance, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 5
	}
	hh, err := s.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return Glance{}, err
	}
	lang := hh.LanguageOf(actor.MemberID)
	list, err := s.ListCharges(ctx, actor, ListChargesInput{})
	if err != nil {
		return Glance{}, err
	}
	g := Glance{Language: lang, Today: hh.Today, Charges: []GlanceCharge{}, Totals: []string{}}
	sums := map[string]money.Money{}
	var order []string
	for _, c := range list.Charges {
		if c.Status != domain.ChargePending {
			continue
		}
		if len(g.Charges) < limit {
			g.Charges = append(g.Charges, GlanceCharge{
				SubscriptionID: c.SubscriptionID, Name: c.SubscriptionName, Amount: c.Amount.Format(lang),
				Date: c.DueDate, DaysUntil: c.DaysUntil, Kind: c.Kind,
			})
		}
		cur := c.Amount.Currency
		if sum, ok := sums[cur]; ok {
			if total, err := sum.Add(c.Amount); err == nil {
				sums[cur] = total
			}
		} else {
			sums[cur] = c.Amount
			order = append(order, cur)
		}
	}
	for _, cur := range order {
		g.Totals = append(g.Totals, sums[cur].Format(lang))
	}
	// Charges of the last 90 days still waiting to be confirmed.
	past, err := s.ListCharges(ctx, actor, ListChargesInput{From: hh.Today.AddDays(-90).String(), To: hh.Today.AddDays(-1).String()})
	if err != nil {
		return Glance{}, err
	}
	for _, c := range past.Charges {
		if c.Status == domain.ChargePending {
			g.Overdue++
		}
	}
	g.Awake, err = s.GetKeepAwake(ctx, actor, struct{}{})
	return g, err
}
