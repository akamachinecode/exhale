package app

import (
	"context"
	"fmt"
	"strings"
)

// AgentContext is the ready-made briefing an AI agent reads first (ADR 0018).
type AgentContext struct {
	Markdown string `json:"markdown" jsonschema:"human-readable briefing of the household, its subscriptions and how Exhale works"`
}

// BuildContext writes a live household briefing for AI agents: who lives
// here, what is subscribed, what is due, what needs attention, and the rules
// of the domain.
func (s *Service) BuildContext(ctx context.Context, actor Actor, _ struct{}) (AgentContext, error) {
	info, err := s.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return AgentContext{}, err
	}
	subs, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{})
	if err != nil {
		return AgentContext{}, err
	}
	sum, err := s.Summary(ctx, actor, struct{}{})
	if err != nil {
		return AgentContext{}, err
	}
	att, err := s.NeedsAttention(ctx, actor, struct{}{})
	if err != nil {
		return AgentContext{}, err
	}
	up, err := s.ListCharges(ctx, actor, ListChargesInput{})
	if err != nil {
		return AgentContext{}, err
	}
	var b strings.Builder
	h := info.Household
	fmt.Fprintf(&b, "# Exhale household: %s\n\n", h.Name)
	fmt.Fprintf(&b, "- Today: %s (time zone %s)\n- Display currency (totals convert into it): %s\n- Language: %s\n- You are acting as: %s (scope: %s)\n", info.Today, h.Timezone, h.Currency, h.Locale, actor.Label, actor.Scope)
	b.WriteString("- Members: ")
	for i, m := range info.Members {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s (%s, %s)", m.Name, m.Role, m.ID)
	}
	b.WriteString("\n\n## Spend\n\n")
	fmt.Fprintf(&b, "%d active subscriptions.", sum.Active)
	for i, m := range sum.Monthly {
		fmt.Fprintf(&b, " %s/month (%s/year).", m, sum.Yearly[i])
	}
	if sum.Converted {
		fmt.Fprintf(&b, " Total ≈ %s/month in %s at ECB rates of %s.", sum.MonthlyTotal, sum.MainCurrency, sum.RatesDate)
	}
	b.WriteString("\n\n## Subscriptions\n\n| id | name | price | cadence | next charge | status | payment method | category | notes |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, v := range subs.Subscriptions {
		cadence := v.Cadence
		if v.BillingDay != "" {
			cadence += ", " + v.BillingDay
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", v.ID, v.Name, v.Price, cadence, v.NextCharge, v.Status, v.PaymentMethod, v.CategoryName, strings.Join(termNotes(v), "; "))
	}
	b.WriteString("\n## Next 30 days\n\n")
	if len(up.Charges) == 0 {
		b.WriteString("No charges.\n")
	}
	for _, c := range up.Charges {
		delayed := ""
		if c.Delayed {
			delayed = fmt.Sprintf(" (moved from %s)", c.OriginalDate)
		}
		fmt.Fprintf(&b, "- %s · %s · %s · %s%s · charge %s\n", c.DueDate, c.SubscriptionName, c.Amount, c.Status, delayed, c.ID)
	}
	b.WriteString("\n## Needs attention\n\n")
	if len(att.Unconfirmed) == 0 && len(att.ExpiringPaymentMethods) == 0 {
		b.WriteString("Nothing.\n")
	}
	for _, c := range att.Unconfirmed {
		fmt.Fprintf(&b, "- Unconfirmed: %s %s was due %s (charge %s). Ask whether it was paid.\n", c.SubscriptionName, c.Amount, c.DueDate, c.ID)
	}
	for _, p := range att.ExpiringPaymentMethods {
		fmt.Fprintf(&b, "- Payment method %s expires on %s.\n", p.Label(), p.ExpiresOn)
	}
	b.WriteString(domainRules)
	return AgentContext{Markdown: b.String()}, nil
}

// termNotes summarises what makes a subscription's dates special.
func termNotes(v SubscriptionView) []string {
	var n []string
	if v.InTrial {
		n = append(n, fmt.Sprintf("free trial until %s", v.TrialEndsOn))
	}
	if v.Expires {
		n = append(n, "expires unless renewed")
	}
	if !v.CancelBy.IsZero() {
		n = append(n, fmt.Sprintf("cancel by %s (%d days' notice)", v.CancelBy, v.NoticeDays))
	}
	if v.NextPrice != nil {
		n = append(n, fmt.Sprintf("price becomes %s on %s", v.NextPrice.Price, v.NextPrice.From))
	}
	if len(v.Tags) > 0 {
		n = append(n, "tags: "+strings.Join(v.Tags, ", "))
	}
	if p := v.PlanSwitch; p != nil && v.AltPrice != nil && p.Saving.Minor > 0 {
		n = append(n, fmt.Sprintf("also offered at %s per %s: switching saves %s a year", v.AltPrice.Price, v.AltPrice.Unit, p.Saving))
	}
	return n
}

const domainRules = `
## How Exhale works (read before changing anything)

- A **subscription** has a price and a billing schedule; each occurrence is a **charge** with its own state: pending, paid, failed or skipped. A pending charge whose date was moved is "delayed".
- Charges are computed from the schedule's **anchor**, never from the previous charge, so dates never drift. A charge on day 31 falls on the last day of shorter months and returns to the 31st afterwards.
- Edits to a charge have a **scope**: "this" changes only that charge; "following" changes it and every later one (new schedule version). Defaults: date → this (usually a one-off delay); amount and payment method → following (usually permanent).
- Cancelling "at period end" keeps the service until the day before the next charge. Exhale only tracks subscriptions: cancelling here does not cancel with the provider, so tell the user to do it (cancelUrl, if set).
- The date that matters is not always the charge: a **free trial** (trialEndsOn) turns into the first paid charge; a **notice period** (noticeDays) puts the last day to cancel before the charge (cancelBy); **renewal=manual** means it expires on that date unless someone renews it.
- Money amounts are decimal strings in the subscription's currency ("12.99"). Dates are YYYY-MM-DD in the household time zone.
- Every write accepts **dryRun: true** to preview the exact result without saving. Prefer previewing, show the user the result, then apply.
- Every write returns an **auditId**; the undo tool reverts it. The user is told about changes made by agents and can undo them.
- Never invent ids: read them from this briefing or from the list tools. Ids returned by a dry run are not saved.
- To change several subscriptions at once use **bulk_update_subscriptions** (dryRun first); pass all the auditIds it returns to undo to revert the whole change. Tags are free labels: search them with list_subscriptions (query or tag). Totals stay by category.
`
