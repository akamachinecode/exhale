package app

import (
	"fmt"
	"strings"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
)

// Billing rules beyond "every N units": which day of the month a monthly or
// yearly cycle charges on, and what happens to charges that fall on a
// weekend. Both live in the engine's rule (stored in the schedule JSON).

func parseDayRule(s string) (engine.DayRule, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "day":
		return engine.DayOfMonth, nil
	case "weekday":
		return engine.NthWeekday, nil
	case "last-weekday", "last weekday", "last":
		return engine.LastWeekday, nil
	}
	return "", fmt.Errorf("%w: day rule must be day, weekday or last-weekday, not %q", domain.ErrValidation, s)
}

func parseShift(s string) (engine.Shift, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none":
		return engine.NoShift, nil
	case "next":
		return engine.NextBusinessDay, nil
	case "previous":
		return engine.PreviousBusinessDay, nil
	}
	return "", fmt.Errorf("%w: weekend shift must be none, next or previous, not %q", domain.ErrValidation, s)
}

// newRule builds the rule of a new subscription from the API's words.
func newRule(interval int, unit, dayRule, shift string, first engine.Date) (engine.Rule, error) {
	r := engine.Rule{Interval: interval, Unit: engine.Unit(unit), Anchor: first}
	var err error
	if r.DayRule, err = parseDayRule(dayRule); err != nil {
		return r, err
	}
	if r.Shift, err = parseShift(shift); err != nil {
		return r, err
	}
	if err := r.Validate(); err != nil {
		return r, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	return r, nil
}

// changedRule is the rule an edit asks for from the charge due on `at` on:
// the current one with the new interval, unit, day rule or weekend shift,
// anchored on the new next charge or on the date `at` was scheduled for (so
// a charge moved off a weekend does not move the billing day).
func changedRule(s engine.Schedule, at engine.Date, in UpdateSubscriptionInput) (engine.Rule, error) {
	rule := s.Current(at)
	set(&rule.Interval, in.Interval)
	if in.Unit != nil {
		rule.Unit = engine.Unit(*in.Unit)
	}
	anchor, ok := s.NominalOf(at)
	if !ok {
		anchor = at
	}
	if in.NextCharge != nil && *in.NextCharge != "" {
		var err error
		if anchor, err = engine.ParseDate(*in.NextCharge); err != nil {
			return rule, fmt.Errorf("%w: next charge: %v", domain.ErrValidation, err)
		}
	}
	rule = rule.Reanchor(anchor)
	if rule.Unit != engine.Month && rule.Unit != engine.Year {
		rule.DayRule = engine.DayOfMonth // only months have weekdays of the month
	}
	var err error
	if in.DayRule != nil {
		if rule.DayRule, err = parseDayRule(*in.DayRule); err != nil {
			return rule, err
		}
	}
	if in.WeekendShift != nil {
		if rule.Shift, err = parseShift(*in.WeekendShift); err != nil {
			return rule, err
		}
	}
	if err := rule.Validate(); err != nil {
		return rule, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	return rule, nil
}

// DayRuleName and ShiftName are the API's words for a rule's options.
func DayRuleName(r engine.Rule) string {
	if r.DayRule == engine.DayOfMonth {
		return "day"
	}
	return string(r.DayRule)
}

func ShiftName(r engine.Rule) string {
	if r.Shift == engine.NoShift {
		return "none"
	}
	return string(r.Shift)
}

var ordinals = map[int]string{1: "first", 2: "second", 3: "third", 4: "fourth", -1: "last"}

// billingDay describes when in its cycle a rule charges, in English for
// agents: "on the third Wednesday, moved to the next business day on
// weekends". It is empty for plain day-of-month rules without a shift, which
// the cadence and the next charge already say.
func billingDay(r engine.Rule) string {
	var parts []string
	if r.DayRule != engine.DayOfMonth {
		parts = append(parts, fmt.Sprintf("on the %s %s", ordinals[r.Week()], r.Anchor.Weekday()))
	}
	switch r.Shift {
	case engine.NextBusinessDay:
		parts = append(parts, "moved to the next business day when it falls on a weekend")
	case engine.PreviousBusinessDay:
		parts = append(parts, "moved to the previous business day when it falls on a weekend")
	case engine.NoShift:
	}
	return strings.Join(parts, ", ")
}
