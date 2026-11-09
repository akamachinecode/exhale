package actions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// The app inside a message (ADR 0019): one message per chat that navigates
// between screens by editing itself and stays live as data changes.
//
// Routes double as callback data: "a|<screen>|<arg>…". View routes are
// stored on the message so any change can redraw it; confirmation and
// mid-flow screens store "" and are left alone until the user moves on.
const (
	appPrefix  = "a"
	scrHome    = "home"
	scrSubs    = "subs" // subs|page
	scrSub     = "sub"  // sub|sub_id
	scrNext    = "next"
	scrSummary = "sum"
	scrAttn    = "att"
	scrCharge  = "chg" // chg|chg_id[|aud_id]
	actCancel  = "cx"  // cx|sub_id       (confirmation, not live)
	actStatus  = "st"  // st|sub_id|pause|resume|cancel_at_period_end|cancel_now

	// RouteHome is the route of the home screen.
	RouteHome = appPrefix + "|" + scrHome

	subsPerPage = 8
	maxListed   = 20
)

func route(parts ...string) string { return data(append([]string{appPrefix}, parts...)...) }

// CommandRoute maps a chat command (e.g. "/subs") to the app screen it
// opens. ok is false for commands that are not app screens.
func CommandRoute(cmd string) (r string, ok bool) {
	cmd, _, _ = strings.Cut(strings.TrimPrefix(cmd, "/"), "@") // "/subs@exhale_bot"
	switch cmd {
	case "start", "home", "menu", "app":
		return RouteHome, true
	case "subs", "subscriptions":
		return route(scrSubs, "0"), true
	case "upcoming", "next":
		return route(scrNext), true
	case "summary", "stats":
		return route(scrSummary), true
	}
	return "", false
}

// Commands lists the app commands for a channel's command menu.
var Commands = []struct{ Command, Description string }{
	{"home", "Open Exhale: spend, next charges, everything"},
	{"subs", "Your subscriptions"},
	{"upcoming", "Charges in the next 30 days"},
	{"summary", "Spend per month, category and payment method"},
}

// OpenApp renders the screen at route for a new app message. After sending
// it, the channel adapter calls AppSent.
func (e *Engine) OpenApp(ctx context.Context, actor app.Actor, r string) (Message, error) {
	m, _, err := e.screen(ctx, actor, r)
	return m, err
}

// AppSent makes messageID the live app message of the chat. Earlier app
// messages are retired (their buttons removed) and their ids returned, so
// the adapter can unpin them.
func (e *Engine) AppSent(ctx context.Context, actor app.Actor, chat ChatRef, messageID, r string) ([]string, error) {
	q := e.svc.Store().Q
	old, err := q.ListAppMessagesForChat(ctx, db.ListAppMessagesForChatParams{Channel: chat.Channel, ChatID: chat.ChatID})
	if err != nil {
		return nil, err
	}
	var retired []string
	for _, m := range old {
		if m.MessageID == messageID {
			continue
		}
		if err := q.RetireAppMessage(ctx, m.ID); err != nil {
			return retired, err
		}
		retired = append(retired, m.MessageID)
		if ch, ok := e.channels[m.Channel]; ok {
			stub := Message{Title: "↓ Exhale moved to a newer message"}
			if err := ch.Edit(ctx, m.ChatID, m.MessageID, stub); err != nil {
				e.log.Debug("could not retire app message", "err", err)
			}
		}
	}
	return retired, q.SaveAppMessage(ctx, db.SaveAppMessageParams{
		ID: domain.NewID("msg"), Channel: chat.Channel, ChatID: chat.ChatID, MessageID: messageID,
		MemberID: store.Ptr(actor.MemberID), Route: liveRoute(r), CreatedAt: store.FormatTime(time.Now()),
	})
}

// liveRoute returns r if the screen should follow data changes, else "".
func liveRoute(r string) string {
	p := strings.Split(r, "|")
	if len(p) < 2 || p[0] != appPrefix {
		return ""
	}
	switch p[1] {
	case scrHome, scrSubs, scrSub, scrNext, scrSummary, scrAttn, scrCharge:
		return r
	}
	return ""
}

// isApp reports whether the button's message is an app message (live or
// retired).
func (e *Engine) isApp(ctx context.Context, chat ChatRef) bool {
	if chat.MessageID == "" {
		return false
	}
	m, err := e.svc.Store().Q.GetMessage(ctx, db.GetMessageParams{Channel: chat.Channel, ChatID: chat.ChatID, MessageID: chat.MessageID})
	return err == nil && strings.HasPrefix(m.Kind, "app")
}

func (e *Engine) setRoute(ctx context.Context, chat ChatRef, r string) {
	if chat.MessageID == "" {
		return
	}
	_ = e.svc.Store().Q.SetMessageRoute(ctx, db.SetMessageRouteParams{Route: liveRoute(r), Channel: chat.Channel, ChatID: chat.ChatID, MessageID: chat.MessageID})
}

// handleApp runs an app button: navigation or a subscription action.
func (e *Engine) handleApp(ctx context.Context, actor app.Actor, chat ChatRef, raw string) (Reply, error) {
	m, toast, err := e.screen(ctx, actor, raw)
	if err != nil {
		return e.fail(err)
	}
	next := raw
	if p := strings.Split(raw, "|"); len(p) > 2 && p[1] == actStatus {
		next = route(scrSub, p[2])
	}
	e.setRoute(ctx, chat, next)
	return Reply{Edit: &m, Toast: toast}, nil
}

// settled charge verbs end on the charge's main screen, which can stay live.
var settled = map[string]bool{vKeep: true, vPaid: true, vFailed: true, vSkip: true, vReopen: true, vBack: true, vDateScope: true, vMethodSet: true, vUndo: true, vNotYet: true}

// inApp adapts the reply of a charge button pressed inside an app message:
// settled screens become the app's charge screen (with navigation), and
// intermediate steps get a way home.
func (e *Engine) inApp(ctx context.Context, actor app.Actor, chat ChatRef, verb, chargeID string, rep Reply) Reply {
	if rep.Edit == nil {
		return rep
	}
	if settled[verb] {
		r := route(scrCharge, chargeID)
		if aud := undoAudit(*rep.Edit); aud != "" {
			r = route(scrCharge, chargeID, aud)
		}
		if m, _, err := e.screen(ctx, actor, r); err == nil {
			m.Footer = cmp.Or(rep.Edit.Footer, m.Footer)
			rep.Edit = &m
			e.setRoute(ctx, chat, r)
			return rep
		}
	}
	m := *rep.Edit
	m.Rows = append(slices.Clone(m.Rows), []Button{{Label: "🏠 Home", Data: RouteHome}})
	rep.Edit = &m
	e.setRoute(ctx, chat, "")
	return rep
}

func undoAudit(m Message) string {
	for _, row := range m.Rows {
		for _, b := range row {
			if p := strings.Split(b.Data, "|"); len(p) == 3 && p[0] == vUndo {
				return p[2]
			}
		}
	}
	return ""
}

// screen renders an app route. Action routes perform their write first and
// return a toast.
func (e *Engine) screen(ctx context.Context, actor app.Actor, r string) (Message, string, error) {
	p := strings.Split(r, "|")
	arg := func(i int) string {
		if i < len(p) {
			return p[i]
		}
		return ""
	}
	if arg(0) != appPrefix {
		return Message{}, "", fmt.Errorf("%w: not an app route", domain.ErrNotFound)
	}
	today, locale := e.context(ctx, actor)
	switch arg(1) {
	case scrHome, "":
		m, err := e.homeScreen(ctx, actor, today, locale)
		return m, "", err
	case scrSubs:
		page, _ := strconv.Atoi(arg(2))
		m, err := e.subsScreen(ctx, actor, page, locale)
		return m, "", err
	case scrSub:
		m, err := e.subScreen(ctx, actor, arg(2), locale)
		return m, "", err
	case scrNext:
		m, err := e.upcomingScreen(ctx, actor, today, locale)
		return m, "", err
	case scrSummary:
		m, err := e.summaryScreen(ctx, actor, locale)
		return m, "", err
	case scrAttn:
		m, err := e.attentionScreen(ctx, actor, locale)
		return m, "", err
	case scrCharge:
		res, err := e.chargeResult(ctx, actor, arg(2))
		if err != nil {
			return Message{}, "", err
		}
		m := chargeMessage("", res, today, locale)
		if arg(3) != "" {
			m.Rows = append(m.Rows, []Button{{Label: "↩︎ Undo", Data: data(vUndo, arg(2), arg(3))}})
		}
		m.Rows = append(m.Rows, []Button{{Label: "‹ " + res.Subscription.Name, Data: route(scrSub, res.Subscription.ID)}, {Label: "🏠 Home", Data: RouteHome}})
		return m, "", nil
	case actCancel:
		d, err := e.svc.GetSubscription(ctx, actor, app.GetSubscriptionInput{ID: arg(2)})
		if err != nil {
			return Message{}, "", err
		}
		m := Message{
			Title: "❌ Cancel " + d.Subscription.Name + "?",
			Lines: []string{"At period end: no more charges; you keep the service until the day before the next one.", cancelHint},
			Rows: [][]Button{
				{{Label: "At period end", Data: route(actStatus, arg(2), "cancel_at_period_end")}, {Label: "Right now", Data: route(actStatus, arg(2), "cancel_now")}},
			},
		}
		if link := cancelPage(d.Subscription); link != nil {
			m.Rows = append(m.Rows, link)
		}
		m.Rows = append(m.Rows, []Button{{Label: "↩︎ Keep it", Data: route(scrSub, arg(2))}})
		return m, "", nil
	case actStatus:
		if _, err := e.svc.SetSubscriptionStatus(ctx, actor, app.SetSubscriptionStatusInput{ID: arg(2), Action: arg(3)}); err != nil {
			return Message{}, "", err
		}
		m, err := e.subScreen(ctx, actor, arg(2), locale)
		toast := map[string]string{"pause": "Paused ⏸", "resume": "Resumed ▶️", "cancel_now": "Cancelled", "cancel_at_period_end": "Cancelled at period end"}[arg(3)]
		return m, toast, err
	}
	return Message{}, "", fmt.Errorf("%w: unknown screen %q", domain.ErrNotFound, arg(1))
}

func homeRow() []Button { return []Button{{Label: "🏠 Home", Data: RouteHome}} }

func (e *Engine) homeScreen(ctx context.Context, actor app.Actor, today engine.Date, locale string) (Message, error) {
	info, err := e.svc.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return Message{}, err
	}
	sum, err := e.svc.Summary(ctx, actor, struct{}{})
	if err != nil {
		return Message{}, err
	}
	attn, err := e.svc.NeedsAttention(ctx, actor, struct{}{})
	if err != nil {
		return Message{}, err
	}
	m := Message{
		Title: "🏠 " + info.Household.Name,
		Lines: []string{
			fmt.Sprintf("💶 %s / month · %s / year", sum.MonthlyTotal.Format(locale), sum.YearlyTotal.Format(locale)),
			fmt.Sprintf("📦 %d active %s", sum.Active, plural(sum.Active, "subscription", "subscriptions")),
		},
	}
	if len(sum.Unconverted) > 0 {
		m.Lines = append(m.Lines, "Not in the totals (no rate): "+strings.Join(sum.Unconverted, ", "))
	}
	for _, pm := range attn.ExpiringPaymentMethods {
		m.Lines = append(m.Lines, fmt.Sprintf("💳 %s expires %s", pm.Label(), shortDate(pm.ExpiresOn)))
	}
	next := Section{Heading: "Next 7 days"}
	open := 0
	for _, c := range sum.NextCharges {
		if c.Status != domain.ChargePending {
			continue
		}
		if open < 6 {
			m.Rows = append(m.Rows, []Button{chargeButton(c, today, locale)})
		}
		open++
	}
	switch {
	case open == 0:
		next.Lines = []string{"Nothing due. Breathe out."}
	case open > 6:
		next.Lines = []string{fmt.Sprintf("%d charges — the first 6 below, tap one to manage it.", open)}
	default:
		next.Lines = []string{fmt.Sprintf("%d %s — tap one to manage it.", open, plural(open, "charge", "charges"))}
	}
	m.Sections = append(m.Sections, next)
	if n := len(attn.Unconfirmed); n > 0 {
		m.Rows = append([][]Button{{{Label: fmt.Sprintf("⚠️ %d %s to confirm", n, plural(n, "charge", "charges")), Data: route(scrAttn)}}}, m.Rows...)
	}
	m.Rows = append(m.Rows,
		[]Button{{Label: "📋 Subscriptions", Data: route(scrSubs, "0")}, {Label: "📅 Upcoming", Data: route(scrNext)}},
		[]Button{{Label: "📊 Summary", Data: route(scrSummary)}, {Label: "🔄 Refresh", Data: RouteHome}},
	)
	return m, nil
}

func (e *Engine) subsScreen(ctx context.Context, actor app.Actor, page int, locale string) (Message, error) {
	list, err := e.svc.ListSubscriptions(ctx, actor, app.ListSubscriptionsInput{})
	if err != nil {
		return Message{}, err
	}
	subs := list.Subscriptions
	rank := map[domain.SubscriptionStatus]int{domain.StatusActive: 0, domain.StatusPaused: 1, domain.StatusCancelled: 2}
	slices.SortStableFunc(subs, func(a, b app.SubscriptionView) int {
		if c := cmp.Compare(rank[a.Status], rank[b.Status]); c != 0 {
			return c
		}
		if a.NextCharge.IsZero() != b.NextCharge.IsZero() {
			if a.NextCharge.IsZero() {
				return 1
			}
			return -1
		}
		if c := cmp.Compare(a.NextCharge.String(), b.NextCharge.String()); c != 0 {
			return c
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	pages := max(1, (len(subs)+subsPerPage-1)/subsPerPage)
	page = min(max(page, 0), pages-1)
	m := Message{Title: fmt.Sprintf("📋 Subscriptions (%d)", len(subs))}
	if len(subs) == 0 {
		m.Lines = []string{"No subscriptions yet. Add your first one in the web app."}
	} else {
		m.Lines = []string{"Tap one to open it."}
	}
	for _, s := range subs[page*subsPerPage : min(len(subs), (page+1)*subsPerPage)] {
		label := fmt.Sprintf("%s · %s/mo", s.Name, s.MonthlyCost.Format(locale))
		switch s.Status {
		case domain.StatusActive:
		case domain.StatusPaused:
			label = "⏸ " + label
		case domain.StatusCancelled:
			label = "✖ " + s.Name
		}
		m.Rows = append(m.Rows, []Button{{Label: label, Data: route(scrSub, s.ID)}})
	}
	if pages > 1 {
		m.Rows = append(m.Rows, []Button{
			{Label: "‹", Data: route(scrSubs, strconv.Itoa(max(page-1, 0)))},
			{Label: fmt.Sprintf("%d / %d", page+1, pages), Data: route(scrSubs, strconv.Itoa(page))},
			{Label: "›", Data: route(scrSubs, strconv.Itoa(min(page+1, pages-1)))},
		})
	}
	m.Rows = append(m.Rows, homeRow())
	return m, nil
}

func (e *Engine) subScreen(ctx context.Context, actor app.Actor, id, locale string) (Message, error) {
	d, err := e.svc.GetSubscription(ctx, actor, app.GetSubscriptionInput{ID: id})
	if err != nil {
		return Message{}, err
	}
	s := d.Subscription
	m := Message{Title: s.Name, Lines: []string{fmt.Sprintf("💶 %s · %s", s.Price.Format(locale), s.Cadence)}}
	switch s.Status {
	case domain.StatusActive:
	case domain.StatusPaused:
		m.Title = "⏸ " + s.Name + " (paused)"
	case domain.StatusCancelled:
		m.Title = "✖ " + s.Name + " (cancelled)"
		if !s.CancelledUntil.IsZero() {
			m.Lines = append(m.Lines, "Service until "+longDate(s.CancelledUntil))
		}
	}
	switch {
	case s.NextCharge.IsZero():
	case s.Expires:
		m.Lines = append(m.Lines, "⌛ Expires "+longDate(s.NextCharge))
	case s.InTrial:
		m.Lines = append(m.Lines, "⏳ Free trial until "+longDate(s.NextCharge))
	default:
		m.Lines = append(m.Lines, "📅 Next charge "+longDate(s.NextCharge))
	}
	if !s.CancelBy.IsZero() {
		m.Lines = append(m.Lines, "⏰ Cancel by "+longDate(s.CancelBy))
	}
	if s.PaymentMethod != "" {
		m.Lines = append(m.Lines, "💳 "+s.PaymentMethod)
	}
	if s.CategoryName != "" {
		m.Lines = append(m.Lines, "🏷 "+s.CategoryName)
	}
	if s.PayerName != "" {
		m.Lines = append(m.Lines, "👤 "+s.PayerName)
	}
	if s.Account != "" {
		m.Lines = append(m.Lines, "🔑 "+s.Account)
	}
	if !s.StartedOn.IsZero() {
		m.Lines = append(m.Lines, "🌱 Since "+longDate(s.StartedOn))
	}
	if len(d.History) > 0 {
		h := d.History[0]
		m.Sections = append(m.Sections, Section{Heading: "Last charge", Lines: []string{fmt.Sprintf("%s · %s · %s", shortDate(h.DueDate), h.Amount.Format(locale), h.Status)}})
	}
	var next *app.ChargeView
	for i := range d.Upcoming {
		if d.Upcoming[i].Status == domain.ChargePending {
			next = &d.Upcoming[i]
			break
		}
	}
	if next != nil {
		m.Rows = append(m.Rows, []Button{{Label: "🔔 Next charge · " + shortDate(next.DueDate), Data: route(scrCharge, next.ID)}})
	}
	switch s.Status {
	case domain.StatusActive:
		m.Rows = append(m.Rows, []Button{{Label: "⏸ Pause", Data: route(actStatus, s.ID, "pause")}, {Label: "❌ Cancel", Data: route(actCancel, s.ID)}})
	case domain.StatusPaused:
		m.Rows = append(m.Rows, []Button{{Label: "▶️ Resume", Data: route(actStatus, s.ID, "resume")}, {Label: "❌ Cancel", Data: route(actCancel, s.ID)}})
	case domain.StatusCancelled: // nothing left to do from chat
	}
	if link := cancelPage(s); link != nil {
		link[0].Label = "🔗 Manage or cancel page"
		m.Rows = append(m.Rows, link)
	}
	m.Rows = append(m.Rows, []Button{{Label: "‹ Subscriptions", Data: route(scrSubs, "0")}, {Label: "🏠 Home", Data: RouteHome}})
	return m, nil
}

func (e *Engine) upcomingScreen(ctx context.Context, actor app.Actor, today engine.Date, locale string) (Message, error) {
	list, err := e.svc.ListCharges(ctx, actor, app.ListChargesInput{})
	if err != nil {
		return Message{}, err
	}
	m := Message{Title: "📅 Next 30 days"}
	switch n := len(list.Charges); {
	case n == 0:
		m.Lines = []string{"Nothing due. Breathe out."}
	case n > maxListed:
		m.Lines = []string{fmt.Sprintf("%d charges — the first %d below; the web app has them all.", n, maxListed)}
	default:
		m.Lines = []string{fmt.Sprintf("%d %s — tap one to manage it.", n, plural(n, "charge", "charges"))}
	}
	for i, c := range list.Charges {
		if i == maxListed {
			break
		}
		m.Rows = append(m.Rows, []Button{chargeButton(c, today, locale)})
	}
	m.Rows = append(m.Rows, homeRow())
	return m, nil
}

func (e *Engine) summaryScreen(ctx context.Context, actor app.Actor, locale string) (Message, error) {
	sum, err := e.svc.Summary(ctx, actor, struct{}{})
	if err != nil {
		return Message{}, err
	}
	m := Message{
		Title: "📊 Summary",
		Lines: []string{
			fmt.Sprintf("💶 %s / month · %s / year", sum.MonthlyTotal.Format(locale), sum.YearlyTotal.Format(locale)),
			fmt.Sprintf("📦 %d active %s", sum.Active, plural(sum.Active, "subscription", "subscriptions")),
		},
	}
	if sum.Converted && sum.RatesDate != "" {
		m.Lines = append(m.Lines, "Converted to "+sum.MainCurrency+" at ECB rates of "+sum.RatesDate)
	}
	if len(sum.ByCategory) > 0 {
		s := Section{Heading: "Per month by category"}
		for _, c := range sum.ByCategory {
			s.Table = append(s.Table, []string{truncate(c.Category, 16), c.Monthly.Format(locale), strconv.Itoa(c.Count)})
		}
		m.Sections = append(m.Sections, s)
	}
	if len(sum.ByMethod) > 0 {
		s := Section{Heading: "Per month by payment method"}
		for _, pm := range sum.ByMethod {
			s.Table = append(s.Table, []string{truncate(pm.PaymentMethod, 16), pm.Monthly.Format(locale), strconv.Itoa(pm.Count)})
		}
		m.Sections = append(m.Sections, s)
	}
	m.Rows = [][]Button{homeRow()}
	return m, nil
}

func (e *Engine) attentionScreen(ctx context.Context, actor app.Actor, locale string) (Message, error) {
	attn, err := e.svc.NeedsAttention(ctx, actor, struct{}{})
	if err != nil {
		return Message{}, err
	}
	m := Message{Title: "⚠️ Needs attention"}
	if len(attn.Unconfirmed) == 0 && len(attn.ExpiringPaymentMethods) == 0 {
		m.Lines = []string{"All clear."}
	}
	if len(attn.Unconfirmed) > 0 {
		m.Lines = append(m.Lines, "These dates passed. Tap one to say whether it was charged.")
	}
	for i, c := range attn.Unconfirmed {
		if i == maxListed {
			break
		}
		m.Rows = append(m.Rows, []Button{{Label: fmt.Sprintf("❓ %s · %s · %s", shortDate(c.DueDate), c.SubscriptionName, c.Amount.Format(locale)), Data: route(scrCharge, c.ID)}})
	}
	for _, pm := range attn.ExpiringPaymentMethods {
		m.Lines = append(m.Lines, fmt.Sprintf("💳 %s expires %s", pm.Label(), shortDate(pm.ExpiresOn)))
	}
	m.Rows = append(m.Rows, homeRow())
	return m, nil
}

func chargeButton(c app.ChargeView, today engine.Date, locale string) Button {
	icon := map[domain.ChargeStatus]string{domain.ChargePaid: "✅ ", domain.ChargeSkipped: "⏭ ", domain.ChargeFailed: "❌ "}[c.Status]
	day := shortDate(c.DueDate)
	switch today.DaysUntil(c.DueDate) {
	case 0:
		day = "Today"
	case 1:
		day = "Tomorrow"
	}
	return Button{Label: fmt.Sprintf("%s%s · %s · %s", icon, day, c.SubscriptionName, c.Amount.Format(locale)), Data: route(scrCharge, c.ID)}
}

func shortDate(d engine.Date) string { return d.At(12, 0, time.UTC).Format("Mon 2 Jan") }

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// refreshApps redraws the live app messages of the given households (all
// when nil). A screen whose subject disappeared falls back to home.
func (e *Engine) refreshApps(ctx context.Context, households map[string]bool) {
	msgs, err := e.svc.Store().Q.ListLiveAppMessages(ctx)
	if err != nil {
		return
	}
	for _, msg := range msgs {
		ch, ok := e.channels[msg.Channel]
		if !ok {
			continue
		}
		actor, err := e.svc.ActorForMember(ctx, store.Str(msg.MemberID))
		if err != nil || (households != nil && !households[actor.HouseholdID]) {
			continue
		}
		m, _, err := e.screen(ctx, actor, msg.Route)
		if errors.Is(err, domain.ErrNotFound) {
			m, _, err = e.screen(ctx, actor, RouteHome)
			e.setRoute(ctx, ChatRef{Channel: msg.Channel, ChatID: msg.ChatID, MessageID: msg.MessageID}, RouteHome)
		}
		if err != nil {
			continue
		}
		if err := ch.Edit(ctx, msg.ChatID, msg.MessageID, m); err != nil {
			e.log.Debug("could not refresh app message", "channel", msg.Channel, "err", err)
		}
	}
}
