package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Engine renders reminders, runs button flows and keeps sent messages in
// sync with the data.
type Engine struct {
	svc      *app.Service
	log      *slog.Logger
	channels map[string]Channel
}

// New returns an Engine delivering through the given channels.
func New(svc *app.Service, log *slog.Logger, channels ...Channel) *Engine {
	e := &Engine{svc: svc, log: log, channels: map[string]Channel{}}
	for _, c := range channels {
		e.channels[c.Name()] = c
	}
	return e
}

func (e *Engine) context(ctx context.Context, actor app.Actor) (engine.Date, string) {
	info, err := e.svc.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return engine.DateOf(time.Now()), "en"
	}
	return info.Today, info.Household.Locale
}

// Locale returns the language of the actor's household.
func (e *Engine) Locale(ctx context.Context, actor app.Actor) string {
	_, locale := e.context(ctx, actor)
	return locale
}

// Render returns the message for a charge.
func (e *Engine) Render(ctx context.Context, actor app.Actor, kind, chargeID string) (Message, error) {
	r, err := e.chargeResult(ctx, actor, chargeID)
	if err != nil {
		return Message{}, err
	}
	today, locale := e.context(ctx, actor)
	return chargeMessage(kind, r, today, locale), nil
}

func (e *Engine) chargeResult(ctx context.Context, actor app.Actor, chargeID string) (app.ChargeResult, error) {
	// A dry-run no-op status change returns the full charge view cheaply.
	list, err := e.svc.ListCharges(ctx, actor, app.ListChargesInput{From: "2000-01-01", To: "2100-12-31"})
	if err != nil {
		return app.ChargeResult{}, err
	}
	for _, c := range list.Charges {
		if c.ID == chargeID {
			sub, err := e.svc.GetSubscription(ctx, actor, app.GetSubscriptionInput{ID: c.SubscriptionID})
			if err != nil {
				return app.ChargeResult{}, err
			}
			return app.ChargeResult{Charge: c, Subscription: sub.Subscription}, nil
		}
	}
	return app.ChargeResult{}, fmt.Errorf("charge %s: %w", chargeID, domain.ErrNotFound)
}

// Handle runs the action behind a button: app navigation (ADR 0019) or a
// charge action. Charge actions pressed inside an app message stay inside
// the app.
func (e *Engine) Handle(ctx context.Context, actor app.Actor, chat ChatRef, raw string) (Reply, error) {
	if strings.HasPrefix(raw, appPrefix+"|") {
		return e.handleApp(ctx, actor, chat, raw)
	}
	rep, err := e.handleCharge(ctx, actor, chat, raw)
	if err != nil || !e.isApp(ctx, chat) {
		return rep, err
	}
	verb, chargeID, _ := strings.Cut(raw, "|")
	chargeID, _, _ = strings.Cut(chargeID, "|")
	return e.inApp(ctx, actor, chat, verb, chargeID, rep), nil
}

func (e *Engine) handleCharge(ctx context.Context, actor app.Actor, chat ChatRef, raw string) (Reply, error) {
	today, locale := e.context(ctx, actor)
	p := strings.Split(raw, "|")
	if len(p) < 2 {
		return Reply{Toast: tr(locale, "This button has expired.")}, nil
	}
	verb, chargeID := p[0], p[1]
	arg := func(i int) string {
		if i < len(p) {
			return p[i]
		}
		return ""
	}
	done := func(res app.ChargeResult, toast string) (Reply, error) {
		m := chargeMessage("", res, today, locale)
		if res.AuditID != "" {
			m.Rows = append(m.Rows, []Button{{Label: tr(locale, "↩︎ Undo"), Data: data(vUndo, res.Charge.ID, res.AuditID)}})
		}
		return Reply{Edit: &m, Toast: toast}, nil
	}
	status := func(st, toast string) (Reply, error) {
		res, err := e.svc.SetChargeStatus(ctx, actor, app.SetChargeStatusInput{ChargeID: chargeID, Status: st})
		if err != nil {
			return e.fail(err)
		}
		return done(res, toast)
	}
	switch verb {
	case vPaid:
		return status(string(domain.ChargePaid), tr(locale, "Marked as paid ✅"))
	case vFailed:
		return status(string(domain.ChargeFailed), tr(locale, "Marked as failed"))
	case vSkip:
		return status(string(domain.ChargeSkipped), tr(locale, "Skipped"))
	case vReopen:
		return status(string(domain.ChargePending), tr(locale, "Reopened"))
	case vNotYet:
		m, err := e.Render(ctx, actor, app.KindConfirm, chargeID)
		if err != nil {
			return e.fail(err)
		}
		m.Footer = tr(locale, "OK — I'll ask again tomorrow.")
		return Reply{Edit: &m, Toast: tr(locale, "I'll ask again tomorrow")}, nil
	case vBack:
		m, err := e.Render(ctx, actor, "", chargeID)
		if err != nil {
			return e.fail(err)
		}
		return Reply{Edit: &m}, nil
	case vDateMenu:
		r, err := e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		month, err := engine.ParseDate(arg(2) + "-01")
		if err != nil {
			month = r.Charge.DueDate
		}
		m := Message{Title: tr(locale, "📅 New date for %s", r.Subscription.Name), Lines: details(r.Charge, r.Subscription, locale), Rows: calendar(chargeID, month, r.Charge.DueDate, today, locale)}
		return Reply{Edit: &m}, nil
	case vDatePick:
		r, err := e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		d := arg(2)
		def := domain.DefaultScope(domain.FieldDate)
		this, following := tr(locale, "Only this charge"), tr(locale, "This and following")
		if def == domain.ScopeThisCharge {
			this = "● " + this
		} else {
			following = "● " + following
		}
		m := Message{
			Title: tr(locale, "📅 Move %s to %s?", r.Subscription.Name, d),
			Lines: []string{tr(locale, "Only this charge: a one-off delay; later charges keep their date."), tr(locale, "This and following: the billing day changes from now on.")},
			Rows: [][]Button{
				{{Label: this, Data: data(vDateScope, chargeID, d, "this")}, {Label: following, Data: data(vDateScope, chargeID, d, "following")}},
				{{Label: tr(locale, "↩︎ Back"), Data: data(vDateMenu, chargeID, d[:7])}},
			},
		}
		return Reply{Edit: &m}, nil
	case vDateScope:
		res, err := e.svc.ChangeChargeDate(ctx, actor, app.ChangeChargeDateInput{ChargeID: chargeID, NewDate: arg(2), Scope: arg(3)})
		if err != nil {
			return e.fail(err)
		}
		return done(res, tr(locale, "Date changed 📅"))
	case vMethod:
		info, err := e.svc.GetHousehold(ctx, actor, struct{}{})
		if err != nil {
			return e.fail(err)
		}
		r, err := e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		rows := [][]Button{}
		for _, pm := range info.PaymentMethods {
			label := pm.Label()
			if pm.ID == r.Subscription.PaymentMethodID {
				label = "● " + label
			}
			rows = append(rows, []Button{{Label: label, Data: data(vMethodSet, chargeID, pm.ID)}})
		}
		if len(rows) == 0 {
			return Reply{Toast: tr(locale, "Add a payment method in the web app first.")}, nil
		}
		rows = append(rows, []Button{{Label: tr(locale, "↩︎ Back"), Data: data(vBack, chargeID)}})
		m := Message{Title: tr(locale, "💳 Payment method for %s", r.Subscription.Name), Lines: []string{tr(locale, "Applies to this and following charges.")}, Rows: rows}
		return Reply{Edit: &m}, nil
	case vMethodSet:
		r, err := e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		pm := arg(2)
		if _, err := e.svc.UpdateSubscription(ctx, actor, app.UpdateSubscriptionInput{ID: r.Subscription.ID, PaymentMethodID: &pm}); err != nil {
			return e.fail(err)
		}
		r, err = e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		return done(r, tr(locale, "Payment method updated 💳"))
	case vAmount:
		r, err := e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		if err := e.saveFlow(ctx, actor, chat, flowState{Kind: "amount", ChargeID: chargeID}); err != nil {
			return e.fail(err)
		}
		m := Message{Title: tr(locale, "✏️ New amount for %s", r.Subscription.Name), Lines: []string{tr(locale, "Current: %s. Reply with the new amount, e.g. %s", r.Charge.Amount.Format(locale), r.Charge.Amount.Decimal())}}
		return Reply{Send: &m, Toast: tr(locale, "Type the new amount")}, nil
	case "ascope":
		res, err := e.svc.ChangeChargeAmount(ctx, actor, app.ChangeChargeAmountInput{ChargeID: chargeID, Amount: arg(2), Scope: arg(3)})
		if err != nil {
			return e.fail(err)
		}
		return done(res, tr(locale, "Amount updated ✏️"))
	case vCancel:
		r, err := e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		m := Message{
			Title: tr(locale, "❌ Cancel %s?", r.Subscription.Name),
			Lines: []string{tr(locale, "At period end: no more charges; you keep the service until the day before the next one."), tr(locale, cancelHint)},
			Rows: [][]Button{
				{{Label: tr(locale, "At period end"), Data: data(vCancelOK, chargeID, "cancel_at_period_end")}, {Label: tr(locale, "Right now"), Data: data(vCancelOK, chargeID, "cancel_now")}},
			},
		}
		if link := cancelPage(r.Subscription, locale); link != nil {
			m.Rows = append(m.Rows, link)
		}
		m.Rows = append(m.Rows, []Button{{Label: tr(locale, "↩︎ Keep it"), Data: data(vBack, chargeID)}})
		return Reply{Edit: &m}, nil
	case vCancelOK:
		r, err := e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		d, err := e.svc.SetSubscriptionStatus(ctx, actor, app.SetSubscriptionStatusInput{ID: r.Subscription.ID, Action: arg(2)})
		if err != nil {
			return e.fail(err)
		}
		m := Message{Title: tr(locale, "❌ %s cancelled", d.Subscription.Name), Lines: []string{tr(locale, "No more charges.")}}
		if !d.Subscription.CancelledUntil.IsZero() {
			m.Lines = []string{tr(locale, "Service until %s. No more charges.", longDate(d.Subscription.CancelledUntil, locale))}
		}
		m.Lines = append(m.Lines, tr(locale, cancelHint))
		if link := cancelPage(d.Subscription, locale); link != nil {
			m.Rows = append(m.Rows, link)
		}
		return Reply{Edit: &m, Toast: tr(locale, "Cancelled")}, nil
	case vPaidAmount:
		r, err := e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		if err := e.saveFlow(ctx, actor, chat, flowState{Kind: "paid", ChargeID: chargeID}); err != nil {
			return e.fail(err)
		}
		m := Message{Title: tr(locale, "💶 How much did %s take?", r.Subscription.Name), Lines: []string{tr(locale, "Expected %s. Type the amount, e.g. %s, or with another currency: 11.20 EUR.", r.Charge.Amount.Format(locale), r.Charge.Amount.Decimal())}}
		return Reply{Send: &m, Toast: tr(locale, "Type the amount")}, nil
	case vNewPrice:
		r, err := e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		price := arg(2)
		if _, err := e.svc.UpdateSubscription(ctx, actor, app.UpdateSubscriptionInput{ID: r.Subscription.ID, Price: &price}); err != nil {
			return e.fail(err)
		}
		m, err := e.Render(ctx, actor, "", chargeID)
		if err != nil {
			return e.fail(err)
		}
		m.Footer = tr(locale, "New price saved for the next charges.")
		return Reply{Edit: &m, Toast: tr(locale, "Price updated 💶")}, nil
	case vUse:
		r, err := e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		using := arg(2) == "1"
		d, err := e.svc.UpdateSubscription(ctx, actor, app.UpdateSubscriptionInput{ID: r.Subscription.ID, Using: &using})
		if err != nil {
			return e.fail(err)
		}
		m, err := e.Render(ctx, actor, "", chargeID)
		if err != nil {
			return e.fail(err)
		}
		toast := tr(locale, "Good to know 👍")
		if !using {
			toast = tr(locale, "Noted")
			m.Footer = tr(locale, "You said you don't use it: it costs %s a year.", d.Subscription.YearlyCost.Format(locale))
		}
		return Reply{Edit: &m, Toast: toast}, nil
	case vKeep:
		res, err := e.svc.DecideRenewal(ctx, actor, app.DecideRenewalInput{ChargeID: chargeID, Decision: "keep"})
		if err != nil {
			return e.fail(err)
		}
		return done(res, tr(locale, "Kept 👍"))
	case vExpire:
		r, err := e.chargeResult(ctx, actor, chargeID)
		if err != nil {
			return e.fail(err)
		}
		if _, err := e.svc.SetChargeStatus(ctx, actor, app.SetChargeStatusInput{ChargeID: chargeID, Status: string(domain.ChargeSkipped), Note: "expired"}); err != nil {
			return e.fail(err)
		}
		if _, err := e.svc.SetSubscriptionStatus(ctx, actor, app.SetSubscriptionStatusInput{ID: r.Subscription.ID, Action: "cancel_now"}); err != nil {
			return e.fail(err)
		}
		m := Message{Title: tr(locale, "⌛ %s expired", r.Subscription.Name), Lines: []string{tr(locale, "No more reminders.")}}
		return Reply{Edit: &m, Toast: tr(locale, "Expired")}, nil
	case vUndo:
		if _, err := e.svc.Undo(ctx, actor, app.UndoInput{AuditID: arg(2)}); err != nil {
			return e.fail(err)
		}
		m, err := e.Render(ctx, actor, "", chargeID)
		if err != nil {
			return Reply{Toast: tr(locale, "Undone")}, nil
		}
		return Reply{Edit: &m, Toast: tr(locale, "Undone ↩︎")}, nil
	}
	return Reply{Toast: tr(locale, "Unknown action.")}, nil
}

func (e *Engine) fail(err error) (Reply, error) {
	if errors.Is(err, domain.ErrValidation) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
		msg := err.Error()
		if i := strings.Index(msg, ": "); i > 0 && errors.Is(err, domain.ErrValidation) {
			msg = msg[i+2:]
		}
		return Reply{Toast: "⚠️ " + msg}, nil
	}
	return Reply{}, err
}

// cancelHint reminds that Exhale tracks subscriptions; it does not cancel
// them with the provider.
const cancelHint = "Exhale only stops tracking it: cancel it with the provider too."

// ChatRef identifies a conversation on a channel and, for button presses,
// the message the button belongs to.
type ChatRef struct {
	Channel   string
	ChatID    string
	MessageID string
}

// flowState is what a chat is in the middle of: a question waiting for a
// typed answer, or the draft of the add wizard.
type flowState struct {
	Kind     string            `json:"kind"`
	ChargeID string            `json:"chargeId,omitempty"`
	Ref      string            `json:"ref,omitempty"`   // subscription or payment method being edited
	Field    string            `json:"field,omitempty"` // what the next typed answer is
	Draft    map[string]string `json:"draft,omitempty"`
}

func decodeFlow(state string) flowState {
	var f flowState
	_ = json.Unmarshal([]byte(state), &f)
	if f.Draft == nil {
		f.Draft = map[string]string{}
	}
	return f
}

func parseTime(s string) time.Time { return store.ParseTime(s) }

func flowID(c ChatRef) string { return domain.PrefixFlow + "_" + c.Channel + "_" + c.ChatID }

func (e *Engine) saveFlow(ctx context.Context, actor app.Actor, chat ChatRef, f flowState) error {
	b, _ := json.Marshal(f)
	now := time.Now()
	return e.svc.Store().Q.SaveFlow(ctx, db.SaveFlowParams{ID: flowID(chat), MemberID: actor.MemberID, State: string(b), ExpiresAt: store.FormatTime(now.Add(flowTTL)), CreatedAt: store.FormatTime(now)})
}

// HandleText handles a typed message that answers a pending question (for
// example a new amount). ok is false if no question was pending.
func (e *Engine) HandleText(ctx context.Context, actor app.Actor, chat ChatRef, text string) (Reply, bool, error) {
	q := e.svc.Store().Q
	row, err := q.GetFlow(ctx, flowID(chat))
	if err != nil || store.ParseTime(row.ExpiresAt).Before(time.Now()) {
		return Reply{}, false, nil
	}
	f := decodeFlow(row.State)
	_ = q.DeleteFlow(ctx, row.ID)
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "/") {
		return Reply{}, false, nil // a command leaves the question unanswered
	}
	if f.Kind != "amount" {
		rep, err := e.flowText(ctx, actor, chat, f, text)
		return rep, true, err
	}
	amount := strings.ReplaceAll(strings.TrimSpace(text), ",", ".")
	r, err := e.chargeResult(ctx, actor, f.ChargeID)
	if err != nil {
		rep, err := e.fail(err)
		return rep, true, err
	}
	def := domain.DefaultScope(domain.FieldAmount)
	_, locale := e.context(ctx, actor)
	this, following := tr(locale, "Only this charge"), tr(locale, "This and following")
	if def == domain.ScopeThisAndAfter {
		following = "● " + following
	} else {
		this = "● " + this
	}
	m := Message{
		Title: fmt.Sprintf("✏️ %s → %s %s?", r.Subscription.Name, amount, r.Charge.Amount.Currency),
		Rows:  [][]Button{{{Label: this, Data: data("ascope", f.ChargeID, amount, "this")}, {Label: following, Data: data("ascope", f.ChargeID, amount, "following")}}},
	}
	return Reply{Send: &m}, true, nil
}

// Deliver sends an outbox item to every chat the member has linked. It
// implements the scheduler's delivery step.
func (e *Engine) Deliver(ctx context.Context, item app.OutboxItem) error {
	actor, err := e.svc.ActorForMember(ctx, item.MemberID)
	if err != nil {
		return err
	}
	m, err := e.Render(ctx, actor, item.Kind, item.ChargeID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil // the charge is gone; nothing to say
		}
		return err
	}
	links, err := e.svc.Store().Q.ChannelLinksForMember(ctx, item.MemberID)
	if err != nil {
		return err
	}
	var errs []error
	for _, l := range links {
		ch, ok := e.channels[l.Channel]
		if !ok {
			continue
		}
		msgID, err := ch.Send(ctx, l.ExternalID, m)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", l.Channel, err))
			continue
		}
		_ = e.svc.Store().Q.SaveMessage(ctx, db.SaveMessageParams{ID: domain.NewID("msg"), Channel: l.Channel, ChatID: l.ExternalID, MessageID: msgID, ChargeID: store.Ptr(item.ChargeID), MemberID: store.Ptr(item.MemberID), CreatedAt: store.FormatTime(time.Now())})
	}
	if len(links) == 0 {
		e.log.Info("reminder not delivered: member has no linked chat", "member", item.MemberID, "charge", item.ChargeID)
	}
	return errors.Join(errs...)
}

// appRefreshDelay coalesces bursts of events into one redraw of the live
// app messages; appRefreshEvery keeps relative dates right across midnight.
var (
	appRefreshDelay = 400 * time.Millisecond
	appRefreshEvery = time.Hour
)

// Sync re-edits messages already sent whenever their data changes, from any
// source: web, another chat, an AI agent or the scheduler (bus ⑫). Reminder
// messages follow their charge; live app messages redraw the screen they
// show (ADR 0019).
func (e *Engine) Sync(ctx context.Context) error {
	events, stop := e.svc.Bus().Subscribe(128)
	defer stop()
	hourly := time.NewTicker(appRefreshEvery)
	defer hourly.Stop()
	pending := map[string]bool{}
	var flush <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if ev.Type == bus.ChargeUpdated && ev.ChargeID != "" {
				e.refresh(ctx, ev)
			}
			if ev.HouseholdID != "" {
				pending[ev.HouseholdID] = true
				if flush == nil {
					flush = time.After(appRefreshDelay)
				}
			}
		case <-flush:
			e.refreshApps(ctx, pending)
			pending, flush = map[string]bool{}, nil
		case <-hourly.C:
			e.refreshApps(ctx, nil)
		}
	}
}

func (e *Engine) refresh(ctx context.Context, ev bus.Event) {
	msgs, err := e.svc.Store().Q.ListMessagesForCharge(ctx, store.Ptr(ev.ChargeID))
	if err != nil {
		return
	}
	for _, msg := range msgs {
		ch, ok := e.channels[msg.Channel]
		if !ok {
			continue
		}
		actor, err := e.svc.ActorForMember(ctx, store.Str(msg.MemberID))
		if err != nil {
			continue
		}
		m, err := e.Render(ctx, actor, "", ev.ChargeID)
		if err != nil {
			continue
		}
		if strings.HasPrefix(ev.ActorLabel, actor.Label) {
			continue // the member's own action already updated the message they used
		}
		if ev.ActorLabel != "" {
			_, locale := e.context(ctx, actor)
			m.Footer = tr(locale, "Changed by %s", ev.ActorLabel)
			if ev.AuditID != "" {
				m.Rows = append(m.Rows, []Button{{Label: tr(locale, "↩︎ Undo"), Data: data(vUndo, ev.ChargeID, ev.AuditID)}})
			}
		}
		if err := ch.Edit(ctx, msg.ChatID, msg.MessageID, m); err != nil {
			e.log.Debug("could not re-edit message", "channel", msg.Channel, "err", err)
		}
	}
}
