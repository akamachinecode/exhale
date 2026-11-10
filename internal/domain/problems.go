package domain

import (
	"errors"
	"strings"

	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
)

// The engine and money packages are pure and know nothing of languages.
// These turn their failures into sentences people can read, from the input
// the caller has at hand.

// BadDate says that s, given for the field what, is not a date.
func BadDate(what Word, s string) error {
	return Invalid("%s: %q is not a date like 2026-01-31", what, s)
}

// BadPrice says why amount in currency could not be read.
func BadPrice(amount, currency string) error {
	if _, err := money.New(0, currency); err != nil {
		return Invalid("unknown currency %q", strings.ToUpper(currency))
	}
	return Invalid("%q is not an amount", amount)
}

// BadRule says why a billing cycle of interval × unit was refused by the
// engine (err).
func BadRule(interval int, unit string, err error) error {
	if errors.Is(err, engine.ErrInvalidRule) {
		switch engine.Unit(unit) {
		case engine.Day, engine.Week, engine.Month, engine.Year:
		default:
			return Invalid("unknown unit %q; use day, week, month or year", unit)
		}
		if interval < 1 || interval > 1000 {
			return Invalid("the interval must be between 1 and 1000, got %d", interval)
		}
	}
	return Invalid("the billing schedule is not valid (%v)", err)
}

// BadBillingRule says why the engine refused r, a rule that may use a
// weekday of the month or a weekend shift (err from r.Validate).
func BadBillingRule(r engine.Rule, err error) error {
	if errors.Is(err, engine.ErrInvalidRule) {
		weekday := r.DayRule == engine.NthWeekday || r.DayRule == engine.LastWeekday
		switch {
		case weekday && r.Unit != engine.Month && r.Unit != engine.Year:
			return Invalid("a weekday of the month needs a monthly or yearly cycle")
		case r.DayRule == engine.NthWeekday && r.Anchor.Day() > 28:
			return Invalid("%s is the fifth weekday of its month, which most months do not have: choose the last one instead", r.Anchor.String())
		case r.DayRule == engine.LastWeekday && r.Anchor.Day()+7 <= engine.DaysIn(r.Anchor.Year(), r.Anchor.Month()):
			return Invalid("%s is not the last weekday of its month", r.Anchor.String())
		case r.Shift != engine.NoShift && r.Unit == engine.Day && r.Interval < 3:
			return Invalid("moving weekend charges needs at least 3 days between charges")
		}
	}
	return BadRule(r.Interval, string(r.Unit), err)
}
