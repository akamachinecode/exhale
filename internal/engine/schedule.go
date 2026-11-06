package engine

import (
	"errors"
	"fmt"
)

// Version is a rule in effect from a given date (inclusive) until the From of
// the next version (exclusive).
type Version struct {
	From Date `json:"from"`
	Rule Rule `json:"rule"`
}

// Schedule is the full billing timeline of a subscription: one or more rule
// versions plus optional limits. Past charges keep the rule they were created
// under; "this and following" edits append a new version.
type Schedule struct {
	Versions []Version `json:"versions"`
	// Until, if set, is the last date on which a charge may happen
	// (inclusive). Used for end dates and cancellation at period end.
	Until Date `json:"until,omitzero"`
	// MaxCharges, if > 0, limits the total number of charges (contracts with
	// a fixed number of payments).
	MaxCharges int `json:"maxCharges,omitempty"`
}

// ErrInvalidSchedule is returned for schedules that cannot be evaluated.
var ErrInvalidSchedule = errors.New("engine: invalid schedule")

// NewSchedule returns a schedule with a single rule, in effect from its
// anchor.
func NewSchedule(r Rule) (Schedule, error) {
	s := Schedule{Versions: []Version{{From: r.Anchor, Rule: r}}}
	return s, s.Validate()
}

// Validate checks every version and their ordering.
func (s Schedule) Validate() error {
	if len(s.Versions) == 0 {
		return fmt.Errorf("%w: no rule versions", ErrInvalidSchedule)
	}
	for i, v := range s.Versions {
		if err := v.Rule.Validate(); err != nil {
			return fmt.Errorf("version %d: %w", i, err)
		}
		if v.Rule.Anchor.Before(v.From) {
			return fmt.Errorf("%w: version %d anchor %s is before its start %s", ErrInvalidSchedule, i, v.Rule.Anchor, v.From)
		}
		if i > 0 && !v.From.After(s.Versions[i-1].From) {
			return fmt.Errorf("%w: version %d starts %s, not after %s", ErrInvalidSchedule, i, v.From, s.Versions[i-1].From)
		}
	}
	if s.MaxCharges < 0 {
		return fmt.Errorf("%w: negative max charges", ErrInvalidSchedule)
	}
	if !s.Until.IsZero() && s.Until.Before(s.First()) {
		return fmt.Errorf("%w: until %s is before the first charge %s", ErrInvalidSchedule, s.Until, s.First())
	}
	return nil
}

// First returns the first charge date.
func (s Schedule) First() Date { return s.Versions[0].Rule.Anchor }

// Between returns all charge dates d with from <= d <= to, in order.
func (s Schedule) Between(from, to Date) []Date {
	var out []Date
	s.walk(from, func(d Date) bool {
		if d.After(to) {
			return false
		}
		out = append(out, d)
		return true
	})
	return out
}

// NextOnOrAfter returns the first charge date on or after d, if any.
func (s Schedule) NextOnOrAfter(d Date) (Date, bool) {
	var next Date
	found := false
	s.walk(d, func(x Date) bool {
		next, found = x, true
		return false
	})
	return next, found
}

// Upcoming returns at most n charge dates on or after d.
func (s Schedule) Upcoming(d Date, n int) []Date {
	out := make([]Date, 0, n)
	if n <= 0 {
		return out
	}
	s.walk(d, func(x Date) bool {
		out = append(out, x)
		return len(out) < n
	})
	return out
}

// walk calls yield for every charge date on or after from, in order, until
// yield returns false or the schedule ends.
func (s Schedule) walk(from Date, yield func(Date) bool) {
	count := 0
	for i, v := range s.Versions {
		var end Date // exclusive end of this version
		if i+1 < len(s.Versions) {
			end = s.Versions[i+1].From
		}
		start := 0
		// With a charge limit every earlier charge must be counted, so the
		// walk starts at the anchor; otherwise jump straight to `from`.
		if s.MaxCharges == 0 && from.After(v.Rule.Anchor) {
			_, start = v.Rule.NextOnOrAfter(from)
		}
		for n := start; ; n++ {
			d := v.Rule.Occurrence(n)
			if !end.IsZero() && !d.Before(end) {
				break
			}
			if !s.Until.IsZero() && d.After(s.Until) {
				return
			}
			count++
			if s.MaxCharges > 0 && count > s.MaxCharges {
				return
			}
			if d.Before(from) {
				continue
			}
			if !yield(d) {
				return
			}
		}
	}
}

// ChangeFrom implements "this and following" edits. The charge that was due
// on `at` (and every later one) is replaced by a new rule anchored on
// newAnchor. Charges before min(at, newAnchor) keep their previous rule.
func (s Schedule) ChangeFrom(at, newAnchor Date, interval int, unit Unit) (Schedule, error) {
	from := at
	if newAnchor.Before(from) {
		from = newAnchor
	}
	out := Schedule{Until: s.Until, MaxCharges: s.MaxCharges}
	for _, v := range s.Versions {
		if v.From.Before(from) {
			out.Versions = append(out.Versions, v)
		}
	}
	out.Versions = append(out.Versions, Version{From: from, Rule: Rule{Interval: interval, Unit: unit, Anchor: newAnchor}})
	return out, out.Validate()
}

// Current returns the rule in effect on date d.
func (s Schedule) Current(d Date) Rule {
	cur := s.Versions[0].Rule
	for _, v := range s.Versions[1:] {
		if v.From.After(d) {
			break
		}
		cur = v.Rule
	}
	return cur
}
