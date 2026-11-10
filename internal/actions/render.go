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
	vPaid       = "paid"
	vNotYet     = "notyet"
	vFailed     = "failed"
	vSkip       = "skip"
	vReopen     = "reopen"
	vDateMenu   = "date"   // date|chg|YYYY-MM  (calendar page)
	vDatePick   = "pick"   // pick|chg|YYYY-MM-DD
	vDateScope  = "dscope" // dscope|chg|YYYY-MM-DD|this|following
	vAmount     = "amount"
	vMethod     = "pm"     // pm|chg            (menu)
	vMethodSet  = "pmset"  // pmset|chg|pm_id
	vCancel     = "cancel" // cancel|chg        (confirmation)
	vCancelOK   = "cancelok"
	vBack       = "back"
	vUndo       = "undo"   // undo|chg|aud_id
	vKeep       = "keep"   // keep|chg     (keep this renewal: stop asking)
	vExpire     = "expire" // expire|chg   (manual renewal: let it expire)
	vPaidAmount = "pa"     // pa|chg       (paid, but another amount: type it)
	vNewPrice   = "np"     // np|chg|12.99 (that amount is the new price)
	vUse        = "use"    // use|chg|1|0  (still using it?)
)

func data(parts ...string) string { return strings.Join(parts, "|") }

// chargeMessage renders the main message of a charge for a reminder, a
// "keep or cancel?" question or a confirmation, with the buttons that fit
// its state and what the date means (a renewal, the end of a free trial or
// something that expires unless renewed).
func chargeMessage(kind string, r app.ChargeResult, today engine.Date, locale string) Message {
	c := r.Charge
	sub := r.Subscription
	m := Message{Lines: details(c, sub, locale)}
	id := c.ID
	days := today.DaysUntil(c.DueDate)
	edit := [][]Button{
		{{Label: "📅 Change date", Data: data(vDateMenu, id, c.DueDate.String()[:7])}, {Label: "✏️ Amount", Data: data(vAmount, id)}},
		{{Label: "💳 Payment method", Data: data(vMethod, id)}, {Label: "⏭ Skip this one", Data: data(vSkip, id)}},
	}
	switch {
	case c.Status != domain.ChargePending && c.Status != domain.ChargeFailed:
		m.Title = statusTitle(c, sub.Name)
		m.Rows = [][]Button{{{Label: "↩︎ Reopen", Data: data(vReopen, id)}}}
	case c.Kind == app.ChargeExpiry && (kind == app.KindConfirm || c.DueDate.Before(today)):
		m.Title = fmt.Sprintf("❓ Did you renew %s?", sub.Name)
		m.Rows = [][]Button{
			{{Label: "✅ Renewed", Data: data(vPaid, id)}, {Label: "⏳ Not yet", Data: data(vNotYet, id)}, {Label: "⌛ It expired", Data: data(vExpire, id)}},
			{{Label: "📅 New date", Data: data(vDateMenu, id, c.DueDate.String()[:7])}},
		}
	case kind == app.KindConfirm || c.DueDate.Before(today) || c.Status == domain.ChargeFailed:
		m.Title = fmt.Sprintf("❓ Was %s charged?", sub.Name)
		if c.Status == domain.ChargeFailed {
			m.Title = fmt.Sprintf("⚠️ %s failed — did it go through later?", sub.Name)
		}
		m.Rows = [][]Button{
			{{Label: "✅ Paid", Data: data(vPaid, id)}, {Label: "⏳ Not yet", Data: data(vNotYet, id)}, {Label: "❌ Failed", Data: data(vFailed, id)}},
			{{Label: "📅 New date", Data: data(vDateMenu, id, c.DueDate.String()[:7])}, {Label: "⏭ Skip", Data: data(vSkip, id)}},
			{{Label: "💶 Paid, another amount", Data: data(vPaidAmount, id)}},
		}
	case c.Kind == app.ChargeExpiry:
		m.Title = fmt.Sprintf("⌛ %s expires %s", sub.Name, when(days))
		m.Rows = [][]Button{
			{{Label: "✅ Renewed", Data: data(vPaid, id)}, {Label: "📅 Change date", Data: data(vDateMenu, id, c.DueDate.String()[:7])}},
			{{Label: "✏️ Amount", Data: data(vAmount, id)}, {Label: "💳 Payment method", Data: data(vMethod, id)}},
			{{Label: "⌛ Let it expire", Data: data(vExpire, id)}},
		}
	case kind == app.KindDecision && !c.CancelBy.IsZero() && c.Decision == "":
		left := today.DaysUntil(c.CancelBy)
		m.Title = fmt.Sprintf("⏰ Last day to cancel %s: %s", sub.Name, strings.TrimPrefix(when(left), "in "))
		if left > 1 {
			m.Title = fmt.Sprintf("⏰ %d days left to cancel %s", left, sub.Name)
		}
		m.Lines = append(m.Lines, fmt.Sprintf("After that it renews for %s.", c.Amount.Format(locale)))
		m.Rows = [][]Button{{{Label: "👍 Keep it", Data: data(vKeep, id)}, {Label: "❌ Cancel", Data: data(vCancel, id)}}}
	case c.Kind == app.ChargeTrial:
		m.Title = fmt.Sprintf("⏳ %s free trial ends %s", sub.Name, when(days))
		m.Lines = append([]string{fmt.Sprintf("Then %s %s.", c.Amount.Format(locale), sub.Cadence)}, m.Lines[1:]...)
		m.Rows = append([][]Button{{{Label: "👍 Keep it", Data: data(vKeep, id)}, {Label: "❌ Cancel before it charges", Data: data(vCancel, id)}}}, edit...)
	default:
		m.Title = fmt.Sprintf("🔔 %s renews %s", sub.Name, when(days))
		m.Rows = [][]Button{
			{{Label: "✅ Already paid", Data: data(vPaid, id)}, {Label: "📅 Change date", Data: data(vDateMenu, id, c.DueDate.String()[:7])}},
			{{Label: "💳 Payment method", Data: data(vMethod, id)}, {Label: "✏️ Amount", Data: data(vAmount, id)}},
			{{Label: "⏭ Skip this one", Data: data(vSkip, id)}, {Label: "❌ Cancel subscription", Data: data(vCancel, id)}},
		}
		if sub.AskUsage {
			// A cheap, honest "is it worth it?": one tap, asked rarely.
			m.Lines = append(m.Lines, "Still using it?")
			m.Rows = append(m.Rows, []Button{{Label: "👍 Yes", Data: data(vUse, id, "1")}, {Label: "👎 Not really", Data: data(vUse, id, "0")}})
		}
	}
	if c.Decision == "keep" && c.Status == domain.ChargePending {
		m.Footer = "You chose to keep it."
	}
	return m
}

// cancelPage is the link button to where a subscription is cancelled.
func cancelPage(sub app.SubscriptionView) []Button {
	if sub.CancelURL == "" {
		return nil
	}
	return []Button{{Label: "🔗 Open the cancel page", URL: sub.CancelURL}}
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
	if !c.CancelBy.IsZero() && !c.CancelPassed && c.Status == domain.ChargePending {
		lines = append(lines, "⏰ Cancel by "+longDate(c.CancelBy))
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
	rows := monthGrid(month, selected, today,
		func(m string) string { return data(vDateMenu, chargeID, m) },
		func(d string) string { return data(vDatePick, chargeID, d) })
	return append(rows, []Button{{Label: "↩︎ Back", Data: data(vBack, chargeID)}})
}

// monthGrid renders a month as rows of day buttons, Monday first. nav builds
// the data of the month arrows ("2026-12"), pick the data of a day.
func monthGrid(month, selected, today engine.Date, nav, pick func(string) string) [][]Button {
	first := engine.MustDate(month.Year(), month.Month(), 1)
	prev := first.AddDays(-1)
	next := first.AddDays(engine.DaysIn(first.Year(), first.Month()))
	rows := [][]Button{{
		{Label: "‹", Data: nav(prev.String()[:7])},
		{Label: first.At(12, 0, time.UTC).Format("January 2006"), Data: nav(first.String()[:7])},
		{Label: "›", Data: nav(next.String()[:7])},
	}}
	week := []Button{}
	offset := (int(first.At(12, 0, time.UTC).Weekday()) + 6) % 7
	for range offset {
		week = append(week, Button{Label: " ", Data: nav(first.String()[:7])})
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
		week = append(week, Button{Label: label, Data: pick(day.String())})
		if len(week) == 7 {
			rows = append(rows, week)
			week = []Button{}
		}
	}
	if len(week) > 0 {
		for len(week) < 7 {
			week = append(week, Button{Label: " ", Data: nav(first.String()[:7])})
		}
		rows = append(rows, week)
	}
	return rows
}
