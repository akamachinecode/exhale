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
	// vSnooze brings the message back later: snz|chg|days|r|d|c, where the
	// last part is the message (r reminder, d keep-or-cancel decision, c
	// was-it-charged). It never comes back past its last useful day
	// (app.SnoozeDay). Any channel with buttons can send it, e.g. web push.
	vSnooze = "snz"
)

// snoozeKinds maps the last part of a snooze button to the outbox kind.
var snoozeKinds = map[string]string{"r": app.KindReminder, "d": app.KindDecision, "c": app.KindConfirm}

// SnoozeData is the button data that snoozes the message of kind (an
// outbox kind) about chargeID for days.
func SnoozeData(chargeID, kind string, days int) string {
	for k, v := range snoozeKinds {
		if v == kind {
			return data(vSnooze, chargeID, fmt.Sprint(days), k)
		}
	}
	return ""
}

// snoozeRow offers "remind me tomorrow" and "in 3 days" on a reminder or a
// keep-or-cancel question, without the buttons that could only come back
// too late.
func snoozeRow(kind string, c app.ChargeView, today engine.Date, locale string) []Button {
	row := []Button{}
	tomorrow, _, ok := app.SnoozeDay(kind, c, today, 1)
	if !ok {
		return nil
	}
	row = append(row, Button{Label: tr(locale, "💤 Remind me tomorrow"), Data: SnoozeData(c.ID, kind, 1)})
	later, clamped, ok := app.SnoozeDay(kind, c, today, 3)
	switch {
	case !ok || later == tomorrow:
	case !clamped:
		row = append(row, Button{Label: tr(locale, "💤 In 3 days"), Data: SnoozeData(c.ID, kind, 3)})
	case kind == app.KindDecision:
		row = append(row, Button{Label: tr(locale, "💤 On the last day"), Data: SnoozeData(c.ID, kind, 3)})
	default:
		row = append(row, Button{Label: tr(locale, "💤 On the day"), Data: SnoozeData(c.ID, kind, 3)})
	}
	return row
}

// withoutSnooze drops the snooze buttons of a message.
func withoutSnooze(m Message) Message {
	rows := [][]Button{}
	for _, row := range m.Rows {
		if len(row) > 0 && strings.HasPrefix(row[0].Data, vSnooze+"|") {
			continue
		}
		rows = append(rows, row)
	}
	m.Rows = rows
	return m
}

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
		{{Label: tr(locale, "📅 Change date"), Data: data(vDateMenu, id, c.DueDate.String()[:7])}, {Label: tr(locale, "✏️ Amount"), Data: data(vAmount, id)}},
		{{Label: tr(locale, "💳 Payment method"), Data: data(vMethod, id)}, {Label: tr(locale, "⏭ Skip this one"), Data: data(vSkip, id)}},
	}
	switch {
	case c.Status != domain.ChargePending && c.Status != domain.ChargeFailed:
		m.Title = statusTitle(c, sub.Name, locale)
		m.Rows = [][]Button{{{Label: tr(locale, "↩︎ Reopen"), Data: data(vReopen, id)}}}
	case c.Kind == app.ChargeExpiry && (kind == app.KindConfirm || c.DueDate.Before(today)):
		m.Title = tr(locale, "❓ Did you renew %s?", sub.Name)
		m.Rows = [][]Button{
			{{Label: tr(locale, "✅ Renewed"), Data: data(vPaid, id)}, {Label: tr(locale, "⏳ Not yet"), Data: data(vNotYet, id)}, {Label: tr(locale, "⌛ It expired"), Data: data(vExpire, id)}},
			{{Label: tr(locale, "📅 New date"), Data: data(vDateMenu, id, c.DueDate.String()[:7])}},
		}
	case kind == app.KindConfirm || c.DueDate.Before(today) || c.Status == domain.ChargeFailed:
		m.Title = tr(locale, "❓ Was %s charged?", sub.Name)
		if c.Status == domain.ChargeFailed {
			m.Title = tr(locale, "⚠️ %s failed — did it go through later?", sub.Name)
		}
		m.Rows = [][]Button{
			{{Label: tr(locale, "✅ Paid"), Data: data(vPaid, id)}, {Label: tr(locale, "⏳ Not yet"), Data: data(vNotYet, id)}, {Label: tr(locale, "❌ Failed"), Data: data(vFailed, id)}},
			{{Label: tr(locale, "📅 New date"), Data: data(vDateMenu, id, c.DueDate.String()[:7])}, {Label: tr(locale, "⏭ Skip"), Data: data(vSkip, id)}},
			{{Label: tr(locale, "💶 Paid, another amount"), Data: data(vPaidAmount, id)}},
		}
	case c.Kind == app.ChargeExpiry:
		m.Title = tr(locale, "⌛ %s expires %s", sub.Name, when(days, locale))
		m.Rows = [][]Button{
			{{Label: tr(locale, "✅ Renewed"), Data: data(vPaid, id)}, {Label: tr(locale, "📅 Change date"), Data: data(vDateMenu, id, c.DueDate.String()[:7])}},
			{{Label: tr(locale, "✏️ Amount"), Data: data(vAmount, id)}, {Label: tr(locale, "💳 Payment method"), Data: data(vMethod, id)}},
			{{Label: tr(locale, "⌛ Let it expire"), Data: data(vExpire, id)}},
		}
	case kind == app.KindDecision && !c.CancelBy.IsZero() && c.Decision == "":
		left := today.DaysUntil(c.CancelBy)
		m.Title = tr(locale, "⏰ Last day to cancel %s: %s", sub.Name, when(left, locale))
		if left > 1 {
			m.Title = tr(locale, "⏰ %d days left to cancel %s", left, sub.Name)
		}
		m.Lines = append(m.Lines, tr(locale, "After that it renews for %s.", c.Amount.Format(locale)))
		m.Rows = [][]Button{{{Label: tr(locale, "👍 Keep it"), Data: data(vKeep, id)}, {Label: tr(locale, "❌ Cancel"), Data: data(vCancel, id)}}}
	case c.Kind == app.ChargeTrial:
		m.Title = tr(locale, "⏳ %s free trial ends %s", sub.Name, when(days, locale))
		m.Lines = append([]string{tr(locale, "Then %s %s.", c.Amount.Format(locale), cadenceText(sub.Cadence, locale))}, m.Lines[1:]...)
		m.Rows = append([][]Button{{{Label: tr(locale, "👍 Keep it"), Data: data(vKeep, id)}, {Label: tr(locale, "❌ Cancel before it charges"), Data: data(vCancel, id)}}}, edit...)
	default:
		m.Title = tr(locale, "🔔 %s renews %s", sub.Name, when(days, locale))
		m.Rows = [][]Button{
			{{Label: tr(locale, "✅ Already paid"), Data: data(vPaid, id)}, {Label: tr(locale, "📅 Change date"), Data: data(vDateMenu, id, c.DueDate.String()[:7])}},
			{{Label: tr(locale, "💳 Payment method"), Data: data(vMethod, id)}, {Label: tr(locale, "✏️ Amount"), Data: data(vAmount, id)}},
			{{Label: tr(locale, "⏭ Skip this one"), Data: data(vSkip, id)}, {Label: tr(locale, "❌ Cancel subscription"), Data: data(vCancel, id)}},
		}
		if sub.AskUsage {
			// A cheap, honest "is it worth it?": one tap, asked rarely.
			m.Lines = append(m.Lines, tr(locale, "Still using it?"))
			m.Rows = append(m.Rows, []Button{{Label: tr(locale, "👍 Yes"), Data: data(vUse, id, "1")}, {Label: tr(locale, "👎 Not really"), Data: data(vUse, id, "0")}})
		}
	}
	if c.Decision == "keep" && c.Status == domain.ChargePending {
		m.Footer = tr(locale, "You chose to keep it.")
	}
	m.Event = chargeEvent(kind, r, today)
	if snoozable := (kind == app.KindReminder && m.Event.Kind == EventReminder) || (kind == app.KindDecision && m.Event.Kind == EventDecision); snoozable {
		if row := snoozeRow(kind, c, today, locale); row != nil {
			m.Rows = append(m.Rows, row)
		}
	}
	return m
}

// cancelPage is the link button to where a subscription is cancelled.
func cancelPage(sub app.SubscriptionView, locale string) []Button {
	if sub.CancelURL == "" {
		return nil
	}
	return []Button{{Label: tr(locale, "🔗 Open the cancel page"), URL: sub.CancelURL}}
}

func statusTitle(c app.ChargeView, name, locale string) string {
	switch c.Status {
	case domain.ChargePaid:
		return tr(locale, "✅ %s paid", name)
	case domain.ChargeSkipped:
		return tr(locale, "⏭ %s skipped", name)
	case domain.ChargeFailed:
		return tr(locale, "❌ %s failed", name)
	default:
		return name
	}
}

func details(c app.ChargeView, sub app.SubscriptionView, locale string) []string {
	lines := []string{fmt.Sprintf("💶 %s · %s", c.Amount.Format(locale), longDate(c.DueDate, locale))}
	if c.Delayed {
		lines = append(lines, tr(locale, "↪︎ moved from %s", longDate(c.OriginalDate, locale)))
	}
	if sub.PaymentMethod != "" {
		lines = append(lines, "💳 "+sub.PaymentMethod)
	}
	if sub.CategoryName != "" {
		lines = append(lines, "🏷 "+sub.CategoryName)
	}
	if !c.CancelBy.IsZero() && !c.CancelPassed && c.Status == domain.ChargePending {
		lines = append(lines, tr(locale, "⏰ Cancel by %s", longDate(c.CancelBy, locale)))
	}
	if !c.ChargedAmount.IsZero() && c.ChargedAmount != c.Amount {
		lines = append(lines, tr(locale, "🏦 charged %s", c.ChargedAmount.Format(locale)))
	}
	return lines
}

// calendar renders a month of day buttons for choosing a new date.
func calendar(chargeID string, month engine.Date, selected engine.Date, today engine.Date, locale string) [][]Button {
	rows := monthGrid(month, selected, today, locale,
		func(m string) string { return data(vDateMenu, chargeID, m) },
		func(d string) string { return data(vDatePick, chargeID, d) })
	return append(rows, []Button{{Label: tr(locale, "↩︎ Back"), Data: data(vBack, chargeID)}})
}

// monthGrid renders a month as rows of day buttons, Monday first. nav builds
// the data of the month arrows ("2026-12"), pick the data of a day.
func monthGrid(month, selected, today engine.Date, locale string, nav, pick func(string) string) [][]Button {
	first := engine.MustDate(month.Year(), month.Month(), 1)
	prev := first.AddDays(-1)
	next := first.AddDays(engine.DaysIn(first.Year(), first.Month()))
	rows := [][]Button{{
		{Label: "‹", Data: nav(prev.String()[:7])},
		{Label: monthTitle(first, locale), Data: nav(first.String()[:7])},
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
