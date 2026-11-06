package engine

import (
	"errors"
	"fmt"
	"time"
)

// Unit is the period unit of a billing rule.
type Unit string

const (
	Day   Unit = "day"
	Week  Unit = "week"
	Month Unit = "month"
	Year  Unit = "year"
)

// Valid reports whether u is a known unit.
func (u Unit) Valid() bool {
	switch u {
	case Day, Week, Month, Year:
		return true
	}
	return false
}

// ErrInvalidRule is returned for rules that cannot produce charges.
var ErrInvalidRule = errors.New("engine: invalid rule")

// Rule is a billing recurrence: a charge every Interval Units, starting at
// Anchor.
//
// Billing semantics (as Stripe, Apple and Google bill):
//   - every occurrence is computed from the anchor, never from the previous
//     occurrence, so there is no drift;
//   - when the anchor day does not exist in a month, the charge falls on the
//     last day of that month (31 Jan → 28/29 Feb → 31 Mar).
type Rule struct {
	Interval int  `json:"interval"`
	Unit     Unit `json:"unit"`
	Anchor   Date `json:"anchor"`
}

// Validate reports whether the rule can produce charges.
func (r Rule) Validate() error {
	switch {
	case r.Interval < 1 || r.Interval > 1000:
		return fmt.Errorf("%w: interval must be between 1 and 1000, got %d", ErrInvalidRule, r.Interval)
	case !r.Unit.Valid():
		return fmt.Errorf("%w: unknown unit %q", ErrInvalidRule, r.Unit)
	case r.Anchor.IsZero():
		return fmt.Errorf("%w: missing anchor date", ErrInvalidRule)
	}
	return nil
}

// Occurrence returns the n-th charge date of the rule (n = 0 is the anchor).
// It panics if n < 0 or if the rule is invalid; call Validate first.
func (r Rule) Occurrence(n int) Date {
	if n < 0 {
		panic("engine: negative occurrence index")
	}
	k := n * r.Interval
	switch r.Unit {
	case Day:
		return r.Anchor.AddDays(k)
	case Week:
		return r.Anchor.AddDays(7 * k)
	case Month:
		return addMonthsClamped(r.Anchor, k)
	case Year:
		return addMonthsClamped(r.Anchor, 12*k)
	}
	panic(fmt.Sprintf("engine: unknown unit %q", r.Unit))
}

// NextOnOrAfter returns the first charge date on or after d and its index.
func (r Rule) NextOnOrAfter(d Date) (Date, int) {
	if !d.After(r.Anchor) {
		return r.Anchor, 0
	}
	n := r.estimate(d)
	// The estimate may overshoot or undershoot by a little; walk to the exact
	// boundary. Occurrences are strictly increasing, so this terminates.
	for n > 0 && !r.Occurrence(n-1).Before(d) {
		n--
	}
	for r.Occurrence(n).Before(d) {
		n++
	}
	return r.Occurrence(n), n
}

// estimate returns an index close to the first occurrence on or after d.
func (r Rule) estimate(d Date) int {
	switch r.Unit {
	case Day:
		return r.Anchor.DaysUntil(d) / r.Interval
	case Week:
		return r.Anchor.DaysUntil(d) / (7 * r.Interval)
	case Month:
		return monthsBetween(r.Anchor, d) / r.Interval
	case Year:
		return monthsBetween(r.Anchor, d) / (12 * r.Interval)
	}
	return 0
}

// addMonthsClamped adds k months to the anchor, clamping the day to the last
// day of the target month.
func addMonthsClamped(anchor Date, k int) Date {
	total := int(anchor.m-1) + k
	y := anchor.y + floorDiv(total, 12)
	m := time.Month(floorMod(total, 12) + 1)
	d := min(anchor.d, DaysIn(y, m))
	return Date{y, m, d}
}

func monthsBetween(a, b Date) int {
	return (b.y-a.y)*12 + int(b.m) - int(a.m)
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func floorMod(a, b int) int { return a - floorDiv(a, b)*b }
