package actions

import (
	"fmt"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
)

// Callback verbs. Data is "verb|chargeID|arg…" and always fits in 64 bytes.
const (
	vPaid      = "paid"
	vNotYet    = "notyet"
	vFailed    = "failed"
	vSkip      = "skip"
	vReopen    = "reopen"
	vDateMenu  = "date"   // date|chg|YYYY-MM  (calendar page)
	vDatePick  = "pick"   // pick|chg|YYYY-MM-DD
	vDateScope = "dscope" // dscope|chg|YYYY-MM-DD|this|following
	vAmount    = "amount"
	vMethod    = "pm"     // pm|chg            (menu)
	vMethodSet = "pmset"  // pmset|chg|pm_id
	vCancel    = "cancel" // cancel|chg        (confirmation)
	vCancelOK  = "cancelok"
	vBack      = "back"
	vUndo      = "undo" // undo|chg|aud_id
)

func data(parts ...string) string { return strings.Join(parts, "|") }

// chargeMessage renders the main message of a charge for a reminder or a
// confirmation question, with the buttons that fit its state.
func chargeMessage(kind string, r app.ChargeResult, today engine.Date, locale string) Message {
	c := r.Charge
	sub := r.Subscription
	m := Message{Lines: details(c, sub, locale)}
	id := c.ID
	switch {
	case c.Status != domain.ChargePending && c.Status != domain.ChargeFailed:
		m.Title = statusTitle(c, sub.Name)
		m.Rows = [][]Button{{{Label: "↩︎ Reopen", Data: data(vReopen, id)}}}
	case kind == app.KindConfirm || c.DueDate.Before(today) || c.Status == domain.ChargeFailed:
		m.Title = fmt.Sprintf("❓ Was %s charged?", sub.Name)
		if c.Status == domain.ChargeFailed {
			m.Title = fmt.Sprintf("⚠️ %s failed — did it go through later?", sub.Name)
		}
		m.Rows = [][]Button{
			{{Label: "✅ Paid", Data: data(vPaid, id)}, {Label: "⏳ Not yet", Data: data(vNotYet, id)}, {Label: "❌ Failed", Data: data(vFailed, id)}},
			{{Label: "📅 New date", Data: data(vDateMenu, id, c.DueDate.String()[:7])}, {Label: "⏭ Skip", Data: data(vSkip, id)}},
		}
	default:
		m.Title = fmt.Sprintf("🔔 %s renews %s", sub.Name, when(today.DaysUntil(c.DueDate)))
		m.Rows = [][]Button{
			{{Label: "✅ Already paid", Data: data(vPaid, id)}, {Label: "📅 Change date", Data: data(vDateMenu, id, c.DueDate.String()[:7])}},
			{{Label: "💳 Payment method", Data: data(vMethod, id)}, {Label: "✏️ Amount", Data: data(vAmount, id)}},
			{{Label: "⏭ Skip this one", Data: data(vSkip, id)}, {Label: "❌ Cancel subscription", Data: data(vCancel, id)}},
		}
	}
	return m
}

func statusTitle(c app.ChargeView, name string) string {
	switch c.Status {
	case domain.ChargePaid:
		return fmt.Sprintf("✅ %s paid", name)
	case domain.ChargeSkipped:
		return fmt.Sprintf("⏭ %s skipped", name)
	case domain.ChargeFailed:
		return fmt.Sprintf("❌ %s failed", name)
	default:
		return name
	}
}

func details(c app.ChargeView, sub app.SubscriptionView, locale string) []string {
	lines := []string{fmt.Sprintf("💶 %s · %s", c.Amount.Format(locale), longDate(c.DueDate))}
	if c.Delayed {
		lines = append(lines, "↪︎ moved from "+longDate(c.OriginalDate))
	}
	if sub.PaymentMethod != "" {
		lines = append(lines, "💳 "+sub.PaymentMethod)
	}
	if sub.CategoryName != "" {
		lines = append(lines, "🏷 "+sub.CategoryName)
	}
	if !c.ChargedAmount.IsZero() && c.ChargedAmount != c.Amount {
		lines = append(lines, "🏦 charged "+c.ChargedAmount.Format(locale))
	}
	return lines
}

func when(days int) string {
	switch {
	case days == 0:
		return "today"
	case days == 1:
		return "tomorrow"
	case days < 0:
		return fmt.Sprintf("%d days ago", -days)
	}
	return fmt.Sprintf("in %d days", days)
}

func longDate(d engine.Date) string {
	return d.At(12, 0, time.UTC).Format("Mon, 2 Jan 2006")
}

// calendar renders a month of day buttons for choosing a new date.
func calendar(chargeID string, month engine.Date, selected engine.Date, today engine.Date) [][]Button {
	first := engine.MustDate(month.Year(), month.Month(), 1)
	prev := first.AddDays(-1)
	next := first.AddDays(engine.DaysIn(first.Year(), first.Month()))
	rows := [][]Button{{
		{Label: "‹", Data: data(vDateMenu, chargeID, prev.String()[:7])},
		{Label: first.At(12, 0, time.UTC).Format("January 2006"), Data: data(vDateMenu, chargeID, first.String()[:7])},
		{Label: "›", Data: data(vDateMenu, chargeID, next.String()[:7])},
	}}
	week := []Button{}
	offset := (int(first.At(12, 0, time.UTC).Weekday()) + 6) % 7 // Monday first
	for range offset {
		week = append(week, Button{Label: " ", Data: data(vDateMenu, chargeID, first.String()[:7])})
	}
	for d := 1; d <= engine.DaysIn(first.Year(), first.Month()); d++ {
		day := engine.MustDate(first.Year(), first.Month(), d)
		label := fmt.Sprint(d)
		switch day {
		case selected:
			label = "•" + label
		case today:
			label = "(" + label + ")"
		}
		week = append(week, Button{Label: label, Data: data(vDatePick, chargeID, day.String())})
		if len(week) == 7 {
			rows = append(rows, week)
			week = []Button{}
		}
	}
	if len(week) > 0 {
		for len(week) < 7 {
			week = append(week, Button{Label: " ", Data: data(vDateMenu, chargeID, first.String()[:7])})
		}
		rows = append(rows, week)
	}
	return append(rows, []Button{{Label: "↩︎ Back", Data: data(vBack, chargeID)}})
}
