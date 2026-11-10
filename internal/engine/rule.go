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

// DayRule says which day of the month a monthly or yearly rule charges on.
// It is read from the anchor, so a rule has a single source of truth.
type DayRule string

const (
	// DayOfMonth charges on the anchor's day number, or on the last day of
	// shorter months (the default).
	DayOfMonth DayRule = ""
	// NthWeekday charges on the anchor's weekday in the anchor's week of the
	// month: anchored on the third Wednesday, it is always the third
	// Wednesday. Only the 1st to 4th exist in every month.
	NthWeekday DayRule = "weekday"
	// LastWeekday charges on the last of the anchor's weekday in the month
	// ("the last Friday"). The anchor must be in the last 7 days of its month.
	LastWeekday DayRule = "last-weekday"
)

// Shift moves a charge that falls on a weekend to a business day. Only
// Saturdays and Sundays count: there are no holiday calendars.
type Shift string

const (
	NoShift             Shift = ""
	NextBusinessDay     Shift = "next"
	PreviousBusinessDay Shift = "previous"
)

// Apply returns d moved off the weekend in the direction of the shift.
func (s Shift) Apply(d Date) Date {
	switch wd := d.Weekday(); {
	case s == NextBusinessDay && wd == time.Saturday:
		return d.AddDays(2)
	case s == NextBusinessDay && wd == time.Sunday:
		return d.AddDays(1)
	case s == PreviousBusinessDay && wd == time.Saturday:
		return d.AddDays(-1)
	case s == PreviousBusinessDay && wd == time.Sunday:
		return d.AddDays(-2)
	}
	return d
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
//
// Monthly and yearly rules can instead follow a weekday of the month (the
// third Wednesday, the last Friday; see DayRule). Any rule can move charges
// that fall on a weekend to the next or previous business day (Shift). The
// shift moves the due date only: the next charge is still computed from the
// anchor, so a shifted charge never moves the ones after it.
type Rule struct {
	Interval int     `json:"interval"`
	Unit     Unit    `json:"unit"`
	Anchor   Date    `json:"anchor"`
	DayRule  DayRule `json:"dayRule,omitempty"`
	Shift    Shift   `json:"weekendShift,omitempty"`
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
	switch r.DayRule {
	case DayOfMonth:
	case NthWeekday, LastWeekday:
		if r.Unit != Month && r.Unit != Year {
			return fmt.Errorf("%w: a weekday of the month needs a monthly or yearly cycle, not %q", ErrInvalidRule, r.Unit)
		}
		if r.DayRule == NthWeekday && r.Anchor.Day() > 28 {
			return fmt.Errorf("%w: %s is the fifth %s of its month, which most months do not have; use the last %s instead", ErrInvalidRule, r.Anchor, r.Anchor.Weekday(), r.Anchor.Weekday())
		}
		if r.DayRule == LastWeekday && r.Anchor.Day()+7 <= DaysIn(r.Anchor.Year(), r.Anchor.Month()) {
			return fmt.Errorf("%w: %s is not the last %s of its month", ErrInvalidRule, r.Anchor, r.Anchor.Weekday())
		}
	default:
		return fmt.Errorf("%w: unknown day rule %q", ErrInvalidRule, r.DayRule)
	}
	switch r.Shift {
	case NoShift, NextBusinessDay, PreviousBusinessDay:
	default:
		return fmt.Errorf("%w: unknown weekend shift %q", ErrInvalidRule, r.Shift)
	}
	// A shift moves a date by up to two days; closer charges could land on
	// the same business day.
	if r.Shift != NoShift && r.Unit == Day && r.Interval < 3 {
		return fmt.Errorf("%w: moving weekend charges needs at least 3 days between charges", ErrInvalidRule)
	}
	return nil
}

// Week returns which weekday of the month the rule charges on: 1 to 4 for
// NthWeekday, -1 for LastWeekday and 0 for DayOfMonth.
func (r Rule) Week() int {
	switch r.DayRule {
	case NthWeekday:
		return (r.Anchor.Day()-1)/7 + 1
	case LastWeekday:
		return -1
	case DayOfMonth:
	}
	return 0
}

// Reanchor returns the same kind of rule anchored on a new date, for "from
// now on" changes. A weekday rule keeps following a weekday of the month: the
// new anchor's (the last one when it is in the last week and the rule was
// "last", or when it is a fifth weekday).
func (r Rule) Reanchor(anchor Date) Rule {
	r.Anchor = anchor
	if r.DayRule == DayOfMonth {
		return r
	}
	inLastWeek := anchor.Day()+7 > DaysIn(anchor.Year(), anchor.Month())
	if anchor.Day() > 28 || (inLastWeek && r.DayRule == LastWeekday) {
		r.DayRule = LastWeekday
	} else {
		r.DayRule = NthWeekday
	}
	return r
}

// Occurrence returns the n-th charge date of the rule (n = 0 is the first
// charge): the nominal date moved off the weekend when the rule says so.
// It panics if n < 0 or if the rule is invalid; call Validate first.
func (r Rule) Occurrence(n int) Date {
	return r.Shift.Apply(r.Nominal(n))
}

// Nominal returns the n-th date the rule is anchored on, before any weekend
// shift (n = 0 is the anchor). Every nominal date is computed from the
// anchor, never from the previous charge.
func (r Rule) Nominal(n int) Date {
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
		return r.addMonths(k)
	case Year:
		return r.addMonths(12 * k)
	}
	panic(fmt.Sprintf("engine: unknown unit %q", r.Unit))
}

func (r Rule) addMonths(k int) Date {
	if r.DayRule == DayOfMonth {
		return addMonthsClamped(r.Anchor, k)
	}
	first := addMonthsClamped(Date{r.Anchor.y, r.Anchor.m, 1}, k)
	return WeekdayOfMonth(first.y, first.m, r.Anchor.Weekday(), r.Week())
}

// WeekdayOfMonth returns the n-th weekday wd of a month (n = 1 to 4), or the
// last one when n is -1.
func WeekdayOfMonth(y int, m time.Month, wd time.Weekday, n int) Date {
	if n == -1 {
		last := DaysIn(y, m)
		back := (int(Date{y, m, last}.Weekday()) - int(wd) + 7) % 7
		return Date{y, m, last - back}
	}
	ahead := (int(wd) - int(Date{y, m, 1}.Weekday()) + 7) % 7
	return Date{y, m, 1 + ahead + 7*(n-1)}
}

// NextOnOrAfter returns the first charge date on or after d and its index.
func (r Rule) NextOnOrAfter(d Date) (Date, int) {
	if first := r.Occurrence(0); !d.After(first) {
		return first, 0
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
