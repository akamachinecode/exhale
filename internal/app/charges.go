package app

import (
	"context"
	"fmt"

	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// ChargeResult is returned by every charge edit.
type ChargeResult struct {
	Charge       ChargeView       `json:"charge"`
	Subscription SubscriptionView `json:"subscription"`
	AuditID      string           `json:"auditId,omitempty" jsonschema:"pass to undo to revert this change"`
	DryRun       bool             `json:"dryRun,omitempty"`
	// PriceChange is set when a charge was paid at a different amount than
	// expected, in the same currency: probably a new price.
	PriceChange *PriceChangeHint `json:"priceChange,omitempty" jsonschema:"the bank took a different amount than expected. Ask the user whether this is the new price; if so call update_subscription with price"`
}

// PriceChangeHint says a charge was paid at another amount.
type PriceChangeHint struct {
	Expected    money.Money `json:"expected"`
	Charged     money.Money `json:"charged"`
	Change      string      `json:"changePercent"`
	YearlyDelta money.Money `json:"yearlyDelta" jsonschema:"difference per year if this is the new price, in the subscription currency"`
}

// ChangeChargeDateInput moves a charge.
type ChangeChargeDateInput struct {
	ChargeID string `json:"chargeId" jsonschema:"charge id (chg_…)"`
	NewDate  string `json:"newDate" jsonschema:"new due date, YYYY-MM-DD"`
	Scope    string `json:"scope,omitempty" jsonschema:"this = only this charge (default: a one-off delay); following = this and every later charge" enum:"this,following"`
	DryRun   bool   `json:"dryRun,omitempty"`
}

// ChangeChargeDate moves one charge ("this") or re-anchors the schedule from
// this charge on ("following").
func (s *Service) ChangeChargeDate(ctx context.Context, actor Actor, in ChangeChargeDateInput) (ChargeResult, error) {
	newDate, err := engine.ParseDate(in.NewDate)
	if err != nil {
		return ChargeResult{}, domain.BadDate("new date", in.NewDate)
	}
	scope := domain.EditScope(in.Scope)
	if scope == "" {
		scope = domain.DefaultScope(domain.FieldDate)
	}
	var out ChargeResult
	err = s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		ch, sub, err := s.charge(ctx, q, actor, in.ChargeID)
		if err != nil {
			return err
		}
		if !ch.Open() {
			return domain.Invalid("charge %s is %s; reopen it first", ch.ID, domain.Word(ch.Status))
		}
		before := ch
		resultID := ch.ID
		switch scope {
		case domain.ScopeThisCharge:
			ch.DueDate = newDate
			if err := s.saveCharge(ctx, q, ch); err != nil {
				return err
			}
		case domain.ScopeThisAndAfter:
			beforeSub := sub
			rule := sub.Schedule.Current(ch.OriginalDate)
			sub.Schedule, err = sub.Schedule.ChangeFrom(ch.OriginalDate, newDate, rule.Interval, rule.Unit)
			if err != nil {
				return domain.BadRule(rule.Interval, string(rule.Unit), err)
			}
			if err := s.saveSubscription(ctx, q, sub); err != nil {
				return err
			}
			cut := ch.OriginalDate
			if newDate.Before(cut) {
				cut = newDate
			}
			if err := q.DeleteFuturePendingCharges(ctx, db.DeleteFuturePendingChargesParams{SubscriptionID: sub.ID, OriginalDate: cut.String()}); err != nil {
				return err
			}
			sub.Version++
			if err := s.materialize(ctx, q, sub, cut); err != nil {
				return err
			}
			if _, err := c.record(q, "subscription.reschedule", "subscription", sub.ID, beforeSub, sub); err != nil {
				return err
			}
			r, err := q.GetChargeByOriginalDate(ctx, db.GetChargeByOriginalDateParams{SubscriptionID: sub.ID, OriginalDate: newDate.String()})
			if err == nil {
				resultID = r.ID
			}
		default:
			return domain.Invalid("unknown scope %q", in.Scope)
		}
		if scope == domain.ScopeThisCharge {
			if out.AuditID, err = c.record(q, "charge.move", "charge", ch.ID, before, ch); err != nil {
				return err
			}
		} else {
			out.AuditID = c.audits[len(c.audits)-1].ID
		}
		if err := q.CancelOutboxForCharge(ctx, store.Ptr(before.ID)); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.ChargeUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID, ChargeID: resultID})
		return s.fillResult(ctx, q, actor, resultID, &out)
	})
	out.DryRun = in.DryRun
	return out, err
}

// ChangeChargeAmountInput changes what a charge costs.
type ChangeChargeAmountInput struct {
	ChargeID string `json:"chargeId"`
	Amount   string `json:"amount" jsonschema:"new amount as a decimal string, in the subscription currency"`
	Scope    string `json:"scope,omitempty" jsonschema:"following = this and later charges (default: price changes are usually permanent); this = only this charge" enum:"this,following"`
	DryRun   bool   `json:"dryRun,omitempty"`
}

// ChangeChargeAmount changes one charge's amount or the price from this
// charge on.
func (s *Service) ChangeChargeAmount(ctx context.Context, actor Actor, in ChangeChargeAmountInput) (ChargeResult, error) {
	scope := domain.EditScope(in.Scope)
	if scope == "" {
		scope = domain.DefaultScope(domain.FieldAmount)
	}
	var out ChargeResult
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		ch, sub, err := s.charge(ctx, q, actor, in.ChargeID)
		if err != nil {
			return err
		}
		amount, err := money.Parse(in.Amount, ch.Amount.Currency)
		if err != nil {
			return domain.BadPrice(in.Amount, ch.Amount.Currency)
		}
		before := ch
		ch.Amount = amount
		if err := s.saveCharge(ctx, q, ch); err != nil {
			return err
		}
		if out.AuditID, err = c.record(q, "charge.amount", "charge", ch.ID, before, ch); err != nil {
			return err
		}
		if scope == domain.ScopeThisAndAfter {
			beforeSub := sub
			sub.Price = amount
			if err := s.saveSubscription(ctx, q, sub); err != nil {
				return err
			}
			if err := q.DeleteFuturePendingCharges(ctx, db.DeleteFuturePendingChargesParams{SubscriptionID: sub.ID, OriginalDate: ch.OriginalDate.AddDays(1).String()}); err != nil {
				return err
			}
			sub.Version++
			if err := s.materialize(ctx, q, sub, ch.OriginalDate.AddDays(1)); err != nil {
				return err
			}
			if out.AuditID, err = c.record(q, "subscription.reprice", "subscription", sub.ID, beforeSub, sub); err != nil {
				return err
			}
		}
		c.emit(bus.Event{Type: bus.ChargeUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID, ChargeID: ch.ID})
		return s.fillResult(ctx, q, actor, ch.ID, &out)
	})
	out.DryRun = in.DryRun
	return out, err
}

// SetChargeStatusInput resolves a charge.
type SetChargeStatusInput struct {
	ChargeID      string `json:"chargeId"`
	Status        string `json:"status" jsonschema:"paid, failed, skipped, or pending to reopen" enum:"paid,failed,skipped,pending"`
	ChargedAmount string `json:"chargedAmount,omitempty" jsonschema:"what the bank actually took, decimal string (optional)"`
	ChargedIn     string `json:"chargedCurrency,omitempty" jsonschema:"currency of chargedAmount, e.g. EUR when a USD service is paid from a EUR card"`
	Note          string `json:"note,omitempty"`
	DryRun        bool   `json:"dryRun,omitempty"`
}

// SetChargeStatus marks a charge paid, failed, skipped, or reopens it.
func (s *Service) SetChargeStatus(ctx context.Context, actor Actor, in SetChargeStatusInput) (ChargeResult, error) {
	var out ChargeResult
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		ch, sub, err := s.charge(ctx, q, actor, in.ChargeID)
		if err != nil {
			return err
		}
		before := ch
		if err := ch.Transition(domain.ChargeStatus(in.Status), s.now()); err != nil {
			return err
		}
		if in.ChargedAmount != "" {
			cur := in.ChargedIn
			if cur == "" {
				cur = ch.Amount.Currency
			}
			if ch.ChargedAmount, err = money.Parse(in.ChargedAmount, cur); err != nil {
				return domain.Invalid("charged amount: %v", domain.BadPrice(in.ChargedAmount, cur))
			}
		}
		if in.Status == string(domain.ChargePending) {
			ch.ChargedAmount = money.Money{}
		}
		if in.Note != "" {
			ch.Note = in.Note
		}
		if err := s.saveCharge(ctx, q, ch); err != nil {
			return err
		}
		if ch.Status != domain.ChargePending {
			if err := q.CancelOutboxForCharge(ctx, store.Ptr(ch.ID)); err != nil {
				return err
			}
		}
		if out.AuditID, err = c.record(q, "charge."+in.Status, "charge", ch.ID, before, ch); err != nil {
			return err
		}
		out.PriceChange = priceChangeHint(ch, sub)
		c.emit(bus.Event{Type: bus.ChargeUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID, ChargeID: ch.ID})
		return s.fillResult(ctx, q, actor, ch.ID, &out)
	})
	out.DryRun = in.DryRun
	return out, err
}

// DecideRenewalInput records what the member decided about a renewal.
type DecideRenewalInput struct {
	ChargeID string `json:"chargeId"`
	Decision string `json:"decision" jsonschema:"keep: stop asking before the last day to cancel; undecided: ask again" enum:"keep,undecided"`
	DryRun   bool   `json:"dryRun,omitempty"`
}

// DecideRenewal records that the member wants to keep a renewal (or takes
// that back), so the "keep or cancel?" reminders stop. To cancel instead,
// use SetSubscriptionStatus.
func (s *Service) DecideRenewal(ctx context.Context, actor Actor, in DecideRenewalInput) (ChargeResult, error) {
	var out ChargeResult
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		ch, sub, err := s.charge(ctx, q, actor, in.ChargeID)
		if err != nil {
			return err
		}
		before := ch
		switch in.Decision {
		case "keep":
			ch.Decision = "keep"
		case "undecided":
			ch.Decision = ""
		default:
			return domain.Invalid("decision must be keep or undecided")
		}
		if err := s.saveCharge(ctx, q, ch); err != nil {
			return err
		}
		if ch.Decision != "" {
			if err := q.CancelOutboxKindForCharge(ctx, db.CancelOutboxKindForChargeParams{ChargeID: store.Ptr(ch.ID), Kind: KindDecision}); err != nil {
				return err
			}
		}
		if out.AuditID, err = c.record(q, "charge.decide", "charge", ch.ID, before, ch); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.ChargeUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID, ChargeID: ch.ID})
		return s.fillResult(ctx, q, actor, ch.ID, &out)
	})
	out.DryRun = in.DryRun
	return out, err
}

// priceChangeHint compares what was charged with what was expected.
func priceChangeHint(ch domain.Charge, sub domain.Subscription) *PriceChangeHint {
	got := ch.ChargedAmount
	if ch.Status != domain.ChargePaid || got.IsZero() || got.Currency != ch.Amount.Currency || got == ch.Amount || ch.Amount.Minor == 0 || got == sub.Price {
		return nil
	}
	delta := money.Money{Minor: got.Minor - ch.Amount.Minor, Currency: got.Currency}
	perMonth, _ := delta.Scale(monthlyFactor(sub.Schedule.Current(ch.OriginalDate)))
	yearly, _ := perMonth.Scale("12")
	return &PriceChangeHint{Expected: ch.Amount, Charged: got, Change: fmt.Sprintf("%+.1f", float64(delta.Minor)*100/float64(ch.Amount.Minor)), YearlyDelta: yearly}
}

func (s *Service) saveCharge(ctx context.Context, q *db.Queries, ch domain.Charge) error {
	n, err := q.UpdateCharge(ctx, store.ChargeUpdate(ch, s.now()))
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.Conflict("charge %s changed meanwhile", ch.ID)
	}
	return nil
}

func (s *Service) fillResult(ctx context.Context, q *db.Queries, actor Actor, chargeID string, out *ChargeResult) error {
	r, err := q.GetCharge(ctx, chargeID)
	if err != nil {
		return store.NotFound(err, "charge %s: not found", chargeID)
	}
	sub, err := s.subscription(ctx, q, actor, r.SubscriptionID)
	if err != nil {
		return err
	}
	today := s.today(ctx, q, actor)
	out.Subscription = s.view(ctx, q, sub, today)
	out.Charge = s.chargeView(store.ChargeFromRow(r), out.Subscription, today)
	return nil
}
