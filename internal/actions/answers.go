package actions

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
)

// Reminders that end in an answer: snoozing, how often "was it charged?"
// repeats, and the messages Exhale sends on its own about a service paid
// twice or a card about to expire. The buttons of those messages are app
// routes, so they work in any message, not only in the app message.
const (
	actDupOK    = "dupok" // dupok|group key   (that's fine: never ask again)
	scrMoveSubs = "mv"    // mv|pm_id          (to which payment method?)
	actMoveSubs = "mvo"   // mvo|pm_id|to_id   (move them all in one step)
	scrRepeat   = "rep"   // how often "was it charged?" repeats
	actRepeat   = "reps"  // reps|times|days
)

// outboxMessage renders what an outbox item says. Items that are not about a
// charge return app.ErrObsolete when there is nothing left to say.
func (e *Engine) outboxMessage(ctx context.Context, actor app.Actor, item app.OutboxItem) (Message, error) {
	var (
		m   Message
		ok  bool
		err error
	)
	switch item.Kind {
	case app.KindDuplicate:
		m, ok, err = e.duplicateMessage(ctx, actor, item.Ref)
	case app.KindCardExpiry:
		m, ok, err = e.cardMessage(ctx, actor, item.Ref)
	default:
		return e.Render(ctx, actor, item.Kind, item.ChargeID)
	}
	if err == nil && !ok {
		err = app.ErrObsolete
	}
	return m, err
}

// answers handles the routes of this file. ok is false for other routes.
func (e *Engine) answers(ctx context.Context, actor app.Actor, p []string, today engine.Date, locale string) (rep Reply, live string, ok bool, err error) {
	arg := func(i int) string {
		if i < len(p) {
			return p[i]
		}
		return ""
	}
	view := func(m Message, err error, toast, r string) (Reply, string, bool, error) {
		if err != nil {
			return Reply{}, "", true, err
		}
		return Reply{Edit: &m, Toast: toast}, r, true, nil
	}
	switch arg(1) {
	case actDupOK:
		if _, err := e.svc.DismissDuplicate(ctx, actor, app.DismissDuplicateInput{Key: arg(2)}); err != nil {
			return Reply{}, "", true, err
		}
		m := Message{Title: tr(locale, "👍 Fine, I won't mention it again."), Rows: [][]Button{homeRow(locale)}}
		return view(m, nil, tr(locale, "Noted"), "")
	case scrMoveSubs:
		m, err := e.moveScreen(ctx, actor, arg(2), locale)
		return view(m, err, "", "")
	case actMoveSubs:
		res, err := e.svc.ReassignPaymentMethod(ctx, actor, app.ReassignPaymentMethodInput{FromID: arg(2), ToID: arg(3)})
		if err != nil {
			return Reply{}, "", true, err
		}
		m, err := e.methodScreen(ctx, actor, arg(3), today)
		return view(m, err, tr(locale, "Moved %d %s 💳", res.Moved, plural(locale, res.Moved, "subscription", "subscriptions")), route(scrMethod, arg(3)))
	case scrRepeat:
		m, err := e.repeatScreen(ctx, actor, locale)
		return view(m, err, "", "")
	case actRepeat:
		times, _ := strconv.Atoi(arg(2))
		every, _ := strconv.Atoi(arg(3))
		if _, err := e.svc.UpdateHousehold(ctx, actor, app.UpdateHouseholdInput{ConfirmTimes: &times, ConfirmEveryDays: &every}); err != nil {
			return Reply{}, "", true, err
		}
		m, err := e.repeatScreen(ctx, actor, locale)
		return view(m, err, tr(locale, "Saved"), "")
	}
	return Reply{}, "", false, nil
}

// snoozedText says when a snoozed message comes back.
func snoozedText(res app.SnoozeResult, today engine.Date, locale string) string {
	switch {
	case res.Clamped && res.Kind == app.KindDecision:
		return tr(locale, "💤 OK — I'll remind you on %s, the last day to cancel.", longDate(res.On, locale))
	case res.Clamped:
		return tr(locale, "💤 OK — I'll remind you on %s, the day it is due.", longDate(res.On, locale))
	case res.On == today.AddDays(1):
		return tr(locale, "💤 OK — I'll remind you tomorrow.")
	}
	return tr(locale, "💤 OK — I'll remind you on %s.", longDate(res.On, locale))
}

// --- the same service paid twice -------------------------------------------------------

func (e *Engine) duplicateMessage(ctx context.Context, actor app.Actor, key string) (Message, bool, error) {
	n, ok, err := e.svc.Duplicate(ctx, actor, key)
	if err != nil || !ok {
		return Message{}, ok, err
	}
	_, locale := e.context(ctx, actor)
	subs := n.Subscriptions
	name := subs[0].Name
	mine := 0
	others := []string{}
	for _, v := range subs {
		payer := v.PayerID
		if payer == "" {
			payer = v.CreatedBy
		}
		switch {
		case payer == actor.MemberID:
			mine++
		case v.PayerName != "" && !contains(others, v.PayerName):
			others = append(others, v.PayerName)
		}
	}
	m := Message{}
	switch {
	case len(others) == 0:
		m.Title = tr(locale, "👯 You pay for %s %d times", name, len(subs))
	case mine > 0:
		m.Title = tr(locale, "👯 You and %s both pay for %s", strings.Join(others, tr(locale, " and ")), name)
	default:
		m.Title = tr(locale, "👯 %s is paid more than once", name)
	}
	costs := make([]string, len(subs))
	same := true
	for i, v := range subs {
		costs[i] = v.MonthlyCost.Format(locale)
		same = same && v.MonthlyCost == subs[0].MonthlyCost
	}
	if same {
		m.Lines = []string{tr(locale, "💶 %d × %s a month", len(subs), costs[0])}
	} else {
		m.Lines = []string{tr(locale, "💶 %s a month", strings.Join(costs, " + "))}
	}
	m.Lines = append(m.Lines, tr(locale, "If it is on purpose, say so and I won't mention it again."))
	row := []Button{}
	for i, v := range subs {
		if i == 6 {
			break
		}
		label := "🔎 " + v.Name
		if v.PayerName != "" {
			label += " · " + v.PayerName
		}
		row = append(row, Button{Label: label, Data: route(scrSub, v.ID)})
		if len(row) == 2 {
			m.Rows = append(m.Rows, row)
			row = []Button{}
		}
	}
	if len(row) > 0 {
		m.Rows = append(m.Rows, row)
	}
	m.Rows = append(m.Rows, []Button{{Label: tr(locale, "👍 That's fine"), Data: route(actDupOK, key)}})
	m.Event = &Event{Kind: EventDuplicate, Subscription: name, Amount: subs[0].MonthlyCost.Decimal(), Currency: subs[0].MonthlyCost.Currency, Path: "/checkup"}
	return m, true, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// --- a card about to expire ------------------------------------------------------------

func (e *Engine) cardMessage(ctx context.Context, actor app.Actor, pmID string) (Message, bool, error) {
	n, ok, err := e.svc.Card(ctx, actor, pmID)
	if err != nil || !ok {
		return Message{}, ok, err
	}
	_, locale := e.context(ctx, actor)
	pm := n.PaymentMethod
	count := len(n.Subscriptions)
	m := Message{
		Title: tr(locale, "💳 %s expires %s", pm.Label(), when(n.DaysLeft, locale)),
		Lines: []string{tr(locale, "Move what it pays to another card before the charges fail.")},
	}
	s := Section{Heading: tr(locale, "It pays %d %s", count, plural(locale, count, "subscription", "subscriptions"))}
	for i, v := range n.Subscriptions {
		if i == maxListed {
			s.Lines = append(s.Lines, tr(locale, "…and %d more", count-maxListed))
			break
		}
		s.Lines = append(s.Lines, fmt.Sprintf("%s · %s", v.Name, v.MonthlyCost.Format(locale)))
	}
	m.Sections = []Section{s}
	if len(n.Others) > 0 {
		m.Rows = append(m.Rows, []Button{{Label: tr(locale, "🔀 Move them to another card"), Data: route(scrMoveSubs, pm.ID)}})
	}
	m.Rows = append(m.Rows, []Button{{Label: tr(locale, "📅 It was renewed: new expiry"), Data: route(actPMEdit, pm.ID, "exp")}})
	m.Event = &Event{Kind: EventCardExpiring, PaymentMethod: pm.Label(), DueDate: pm.ExpiresOn.String(), DaysUntil: n.DaysLeft, Path: "/checkup"}
	return m, true, nil
}

// moveScreen asks where the subscriptions of a payment method go.
func (e *Engine) moveScreen(ctx context.Context, actor app.Actor, id, locale string) (Message, error) {
	pm, others, err := e.method(ctx, actor, id)
	if err != nil {
		return Message{}, err
	}
	use, err := e.methodUse(ctx, actor)
	if err != nil {
		return Message{}, err
	}
	n := len(use[id])
	m := Message{Title: tr(locale, "🔀 Move %d %s from %s", n, plural(locale, n, "subscription", "subscriptions"), pm.Label())}
	if len(others) == 0 {
		m.Lines = []string{tr(locale, "Add the new card first, then come back.")}
		m.Rows = [][]Button{{{Label: tr(locale, "➕ Add a payment method"), Data: route(actPMNew)}}}
	} else {
		m.Lines = []string{tr(locale, "To which one? They all move in one step.")}
		for _, o := range others {
			m.Rows = append(m.Rows, []Button{{Label: o.Label(), Data: route(actMoveSubs, id, o.ID)}})
		}
	}
	m.Rows = append(m.Rows, []Button{{Label: "‹ " + pm.Label(), Data: route(scrMethod, id)}, {Label: tr(locale, "🏠 Home"), Data: RouteHome}})
	return m, nil
}

// --- repeat until handled ------------------------------------------------------------------

// repeatText describes how often an unanswered "was it charged?" comes back.
func repeatText(times, every int, locale string) string {
	switch {
	case times <= 1:
		return tr(locale, "once, no repeats")
	case every == 1:
		return tr(locale, "daily, up to %d times", times)
	case every == 7:
		return tr(locale, "weekly, up to %d times", times)
	}
	return tr(locale, "every %d days, up to %d times", every, times)
}

func (e *Engine) repeatScreen(ctx context.Context, actor app.Actor, locale string) (Message, error) {
	info, err := e.svc.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return Message{}, err
	}
	h := info.Household
	m := Message{
		Title: tr(locale, "🔁 Ask again"),
		Lines: []string{
			tr(locale, "When nobody answers \"was it charged?\", Exhale asks again."),
			tr(locale, "Now: %s.", repeatText(h.ConfirmTimes, h.ConfirmEveryDays, locale)),
		},
	}
	mark := func(on bool, label string) string {
		if on {
			return "● " + label
		}
		return label
	}
	times := []Button{}
	for _, n := range []int{1, 2, 3, 5} {
		label := tr(locale, "%d times", n)
		if n == 1 {
			label = tr(locale, "Once")
		}
		times = append(times, Button{Label: mark(h.ConfirmTimes == n, label), Data: route(actRepeat, strconv.Itoa(n), strconv.Itoa(max(h.ConfirmEveryDays, 1)))})
	}
	every := []Button{}
	for _, d := range []int{1, 2, 3, 7} {
		label := tr(locale, "Every %d days", d)
		switch d {
		case 1:
			label = tr(locale, "Daily")
		case 7:
			label = tr(locale, "Weekly")
		}
		every = append(every, Button{Label: mark(h.ConfirmEveryDays == d, label), Data: route(actRepeat, strconv.Itoa(max(h.ConfirmTimes, 1)), strconv.Itoa(d))})
	}
	m.Rows = [][]Button{times, every, {{Label: tr(locale, "‹ Settings"), Data: route(scrSettings)}, {Label: tr(locale, "🏠 Home"), Data: RouteHome}}}
	if h.ConfirmTimes <= 1 {
		m.Rows = [][]Button{times, m.Rows[2]} // asked once: how often does not apply
	}
	return m, nil
}

// methodMoveRow is the "move its subscriptions" button of a payment method
// that pays something and has somewhere to move to.
func methodMoveRow(pm domain.PaymentMethod, uses, others int, locale string) []Button {
	if uses == 0 || others == 0 {
		return nil
	}
	return []Button{{Label: tr(locale, "🔀 Move its subscriptions"), Data: route(scrMoveSubs, pm.ID)}}
}
