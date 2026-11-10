package actions

import (
	"context"
	"strings"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/engine"
)

// Billing rules in chat: the day in the month a subscription is charged on
// (day 15, the third Wednesday, the last Friday) and what happens on
// weekends. Shown next to the price, and editable from the edit screen.

var weekdaysLong = map[string][7]string{
	"en": {"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"},
	"es": {"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"},
}

// currentRule is the rule the subscription follows from now on.
func currentRule(s app.SubscriptionView) engine.Rule {
	return s.Schedule.Versions[len(s.Schedule.Versions)-1].Rule
}

// dayRuleText names a billing day: "day 15", "the third Wednesday".
func dayRuleText(r engine.Rule, locale string) string {
	wd := weekdaysLong[lang(locale)][r.Anchor.Weekday()]
	switch r.Week() {
	case 1:
		return tr(locale, "the first %s", wd)
	case 2:
		return tr(locale, "the second %s", wd)
	case 3:
		return tr(locale, "the third %s", wd)
	case 4:
		return tr(locale, "the fourth %s", wd)
	case -1:
		return tr(locale, "the last %s", wd)
	}
	return tr(locale, "day %d", r.Anchor.Day())
}

func shiftText(s engine.Shift, locale string) string {
	switch s {
	case engine.NextBusinessDay:
		return tr(locale, "weekends move to the next business day")
	case engine.PreviousBusinessDay:
		return tr(locale, "weekends move to the previous business day")
	case engine.NoShift:
	}
	return ""
}

// billingText is what is said after the price ("· the third Wednesday ·
// weekends move to the next business day"); empty for a plain day of the
// month that stays on weekends.
func billingText(s app.SubscriptionView, locale string) string {
	r := currentRule(s)
	var parts []string
	if r.DayRule != engine.DayOfMonth {
		parts = append(parts, dayRuleText(r, locale))
	}
	if r.Shift != engine.NoShift {
		parts = append(parts, shiftText(r.Shift, locale))
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}

// billingStep shows the billing day screen (scrEditBill) or applies a
// choice from it (actEditBill: a day rule or a weekend shift).
func (e *Engine) billingStep(ctx context.Context, actor app.Actor, p []string, locale string) (Reply, error) {
	id := p[2]
	toast := ""
	if p[1] == actEditBill && len(p) > 3 {
		in := app.UpdateSubscriptionInput{ID: id}
		choice := p[3]
		switch choice {
		case "none", "next", "previous":
			in.WeekendShift = &choice
		default:
			in.DayRule = &choice
		}
		if _, err := e.svc.UpdateSubscription(ctx, actor, in); err != nil {
			return Reply{}, err
		}
		toast = tr(locale, "Saved")
	}
	d, err := e.svc.GetSubscription(ctx, actor, app.GetSubscriptionInput{ID: id})
	if err != nil {
		return Reply{}, err
	}
	s := d.Subscription
	rule := currentRule(s)
	m := Message{Title: tr(locale, "📆 Billing day for %s", s.Name)}
	var due []string
	for _, c := range d.Upcoming[:min(3, len(d.Upcoming))] {
		due = append(due, shortDate(c.DueDate, locale))
	}
	monthly := rule.Unit == engine.Month || rule.Unit == engine.Year
	if monthly {
		m.Lines = append(m.Lines, tr(locale, "Choose the day of the month it is charged on, and what happens when that day falls on a weekend."))
	} else {
		m.Lines = append(m.Lines, tr(locale, "Choose what happens when a charge falls on a weekend."))
	}
	if len(due) > 0 {
		m.Lines = append(m.Lines, tr(locale, "Next charges: %s", strings.Join(due, ", ")))
	}
	mark := func(on bool, label string) string {
		if on {
			return "● " + label
		}
		return label
	}
	// The choices of day come from the date the next charge is scheduled
	// on, before any weekend shift.
	if anchor := s.NextScheduled; monthly && !anchor.IsZero() {
		opts := []engine.Rule{{Anchor: anchor}}
		if anchor.Day() <= 28 {
			opts = append(opts, engine.Rule{Anchor: anchor, DayRule: engine.NthWeekday})
		}
		if anchor.Day()+7 > engine.DaysIn(anchor.Year(), anchor.Month()) {
			opts = append(opts, engine.Rule{Anchor: anchor, DayRule: engine.LastWeekday})
		}
		for _, o := range opts {
			name := "day"
			if o.DayRule != engine.DayOfMonth {
				name = string(o.DayRule)
			}
			m.Rows = append(m.Rows, []Button{{Label: mark(o.DayRule == rule.DayRule, dayRuleText(o, locale)), Data: route(actEditBill, id, name)}})
		}
	}
	if rule.Unit != engine.Day || rule.Interval >= 3 {
		m.Rows = append(m.Rows, []Button{
			{Label: mark(rule.Shift == engine.NoShift, tr(locale, "Keep the date")), Data: route(actEditBill, id, "none")},
			{Label: mark(rule.Shift == engine.NextBusinessDay, tr(locale, "→ Monday")), Data: route(actEditBill, id, "next")},
			{Label: mark(rule.Shift == engine.PreviousBusinessDay, tr(locale, "← Friday")), Data: route(actEditBill, id, "previous")},
		})
	}
	m.Rows = append(m.Rows, []Button{{Label: tr(locale, "‹ Back"), Data: route(scrEdit, id)}})
	return Reply{Edit: &m, Toast: toast}, nil
}
