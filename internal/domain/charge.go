package domain

import (
	"fmt"
	"time"

	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
)

// ChargeStatus is the state of one concrete charge. "Delayed" is not a
// status: it is a pending charge whose DueDate differs from OriginalDate.
type ChargeStatus string

const (
	ChargePending ChargeStatus = "pending"
	ChargePaid    ChargeStatus = "paid"
	ChargeFailed  ChargeStatus = "failed"
	ChargeSkipped ChargeStatus = "skipped"
)

// Charge is one occurrence of a subscription's schedule (ADR 0003: charges
// are events with state). It is identified by (SubscriptionID,
// OriginalDate); DueDate may move ("this charge only").
type Charge struct {
	ID             string       `json:"id"`
	SubscriptionID string       `json:"subscriptionId"`
	OriginalDate   engine.Date  `json:"originalDate"`
	DueDate        engine.Date  `json:"dueDate"`
	Amount         money.Money  `json:"amount"`
	ChargedAmount  money.Money  `json:"chargedAmount,omitzero"` // what the bank actually took, any currency
	Status         ChargeStatus `json:"status"`
	ResolvedAt     *time.Time   `json:"resolvedAt,omitempty"`
	Note           string       `json:"note,omitempty"`
	UpdatedAt      time.Time    `json:"updatedAt"`
	Version        int          `json:"version"`
}

// Delayed reports whether the charge was moved away from its scheduled date.
func (c Charge) Delayed() bool { return c.DueDate != c.OriginalDate }

// Open reports whether the charge still needs to happen or be confirmed.
func (c Charge) Open() bool { return c.Status == ChargePending || c.Status == ChargeFailed }

// CanTransition reports whether a charge may go from one status to another.
// Every transition back to pending exists so that undo is always possible.
func CanTransition(from, to ChargeStatus) bool {
	switch from {
	case ChargePending:
		return to == ChargePaid || to == ChargeFailed || to == ChargeSkipped
	case ChargeFailed:
		return to == ChargePaid || to == ChargeSkipped || to == ChargePending
	case ChargePaid, ChargeSkipped:
		return to == ChargePending
	}
	return false
}

// Transition moves the charge to a new status.
func (c *Charge) Transition(to ChargeStatus, at time.Time) error {
	if !CanTransition(c.Status, to) {
		return invalid("a %s charge cannot become %s", c.Status, to)
	}
	c.Status = to
	if to == ChargePending {
		c.ResolvedAt = nil
	} else {
		t := at
		c.ResolvedAt = &t
	}
	return nil
}

// EditScope says whether an edit applies to one charge or to the series.
type EditScope string

const (
	ScopeThisCharge   EditScope = "this"
	ScopeThisAndAfter EditScope = "following"
)

// Field identifies what an edit changes, for scope defaults.
type Field string

const (
	FieldDate          Field = "date"
	FieldAmount        Field = "amount"
	FieldPaymentMethod Field = "paymentMethod"
)

// DefaultScope returns the pre-selected scope for an edit (the user is always
// asked; this only saves a tap in the common case): a moved date is usually
// a one-off delay, while price and payment-method changes are permanent.
func DefaultScope(f Field) EditScope {
	switch f {
	case FieldDate:
		return ScopeThisCharge
	case FieldAmount, FieldPaymentMethod:
		return ScopeThisAndAfter
	}
	panic(fmt.Sprintf("domain: unknown field %q", f))
}
