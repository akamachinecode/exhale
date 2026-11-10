package actions

import (
	"strconv"
	"strings"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/assistant"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
)

// screenMessage draws a screen the assistant chose to show (ADR 0019 §4)
// with the parts of the app's own screens. Everything in it was read from
// the database by Exhale; each button opens a record in the in-chat app.
func screenMessage(s assistant.Screen, today engine.Date, locale string) Message {
	switch s.Kind {
	case assistant.ScreenSubscriptions:
		m := Message{Title: tr(locale, "📋 Subscriptions (%d)", len(s.Subscriptions))}
		for _, v := range s.Subscriptions {
			label := tr(locale, "%s · %s/mo", v.Name, v.MonthlyCost.Format(locale))
			switch v.Status {
			case domain.StatusActive:
				if !v.NextCharge.IsZero() {
					label += " · " + shortDate(v.NextCharge, locale)
				}
			case domain.StatusPaused:
				label = "⏸ " + label
			case domain.StatusCancelled:
				label = "✖ " + v.Name
			}
			m.Rows = append(m.Rows, []Button{{Label: label, Data: route(scrSub, v.ID)}})
		}
		return m
	case assistant.ScreenCharges:
		c := s.Charges
		m := Message{Title: tr(locale, "📅 Charges from %s to %s", shortDate(c.From, locale), shortDate(c.To, locale))}
		if len(c.Charges) == 0 {
			m.Lines = []string{tr(locale, "No charges in this period.")}
		}
		for _, ch := range c.Charges {
			m.Rows = append(m.Rows, []Button{chargeButton(ch, today, locale)})
		}
		if s.More > 0 {
			m.Footer = tr(locale, "%d more did not fit.", s.More)
		}
		return m
	case assistant.ScreenTotal:
		return totalMessage(*s.Total, locale)
	}
	return Message{}
}

func totalMessage(b app.Breakdown, locale string) Message {
	m := Message{Title: tr(locale, "💶 %s per month", b.Total.Format(locale))}
	if b.Period == "year" {
		m.Title = tr(locale, "💶 %s per year", b.Total.Format(locale))
	}
	sec := Section{Heading: tr(locale, "By category")}
	none := tr(locale, "No category")
	switch b.By {
	case "payment_method":
		sec.Heading, none = tr(locale, "By payment method"), tr(locale, "No payment method")
	case "subscription":
		sec.Heading = tr(locale, "By subscription")
	}
	for _, r := range b.Rows {
		label := r.Label
		if label == "" {
			label = none
		}
		sec.Table = append(sec.Table, []string{truncate(label, 18), r.Amount.Format(locale), strconv.Itoa(r.Count)})
		if r.SubscriptionID != "" {
			m.Rows = append(m.Rows, []Button{{Label: label + " · " + r.Amount.Format(locale), Data: route(scrSub, r.SubscriptionID)}})
		}
	}
	if len(sec.Table) > 0 {
		m.Sections = []Section{sec}
	}
	if b.Converted {
		m.Footer = tr(locale, "Other currencies converted at ECB rates.")
	}
	if len(b.Unconverted) > 0 {
		m.Lines = append(m.Lines, tr(locale, "Left out, no exchange rate: %s", strings.Join(b.Unconverted, ", ")))
	}
	return m
}
