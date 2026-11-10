package actions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/assistant"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/importer"
	"github.com/akamachinecode/exhale/internal/money"
)

// Import from chat (ADR 0022): a file sent to the bot (a bank export, a
// PDF statement, a screenshot) is read like an upload in the web, and the
// review list comes back as a message. Each line is ticked; a number
// button skips it, and nothing changes until the person applies the list
// with the same operations as the web: set_charge_status for the charges
// the statement confirms, import_subscriptions for the new ones. A list
// too long for a chat is offered in the web instead.
//
// Callback data: "imp|t|<review>|<n>" ticks or unticks line n,
// "imp|ok|<review>" applies, "imp|no|<review>" discards.
const (
	impPrefix = "imp"
	impToggle = "t"
	impApply  = "ok"
	impDrop   = "no"
	// maxChatLines is the longest list ticked in the chat.
	maxChatLines = 8
	// maxMissingLines bounds the "expected but not found" section.
	maxMissingLines = 5
)

// FileReader reads an uploaded file into a review list and keeps the
// review while the person decides. *assistant.Assistant implements it.
type FileReader interface {
	ReadImport(ctx context.Context, actor app.Actor, data []byte) (importer.Result, error)
	SaveReview(ctx context.Context, actor app.Actor, r assistant.Review) error
	LoadReview(ctx context.Context, actor app.Actor) (assistant.Review, error)
	DropReview(ctx context.Context, actor app.Actor) error
}

func (e *Engine) reader() FileReader {
	r, _ := e.assistant.(FileReader)
	return r
}

// SetPublicURL sets the address of the web, for links from chat to it.
func (e *Engine) SetPublicURL(u string) { e.publicURL = strings.TrimRight(u, "/") }

// FileTooLargeText answers a file over the import limit.
func FileTooLargeText(locale string) string {
	return tr(locale, "⚠️ That file is too large to import: up to 20 MB, 5 MB for a picture.")
}

// line is one decision of a review: a charge to mark paid or a
// subscription to add.
type line struct {
	pay bool
	i   int // index in Match.Matches or Result.Items
}

func reviewLines(r assistant.Review) []line {
	var out []line
	if r.Result.Match != nil {
		for i, m := range r.Result.Match.Matches {
			if m.Action == app.MatchMarkPaid {
				out = append(out, line{pay: true, i: i})
			}
		}
	}
	for i := range r.Result.Items {
		if i < len(r.Outcomes) && r.Outcomes[i].Action == "create" {
			out = append(out, line{i: i})
		}
	}
	return out
}

// ImportFile reads a file sent to a chat and returns the review to show.
func (e *Engine) ImportFile(ctx context.Context, actor app.Actor, chat ChatRef, data []byte) Message {
	today, locale := e.context(ctx, actor)
	r := e.reader()
	if r == nil {
		return Message{Lines: []string{tr(locale, "⚠️ Importing files is not available here.")}}
	}
	res, err := r.ReadImport(ctx, actor, data)
	if err != nil {
		e.log.Info("chat import: the file was not read", "err", err)
		return Message{Lines: []string{"⚠️ " + importError(err, locale)}}
	}
	rev := assistant.Review{ID: assistant.NewReviewID(), Channel: chat.Channel, Result: res}
	if len(res.Items) > 0 {
		dry, err := e.svc.Import(ctx, actor, app.ImportInput{Items: res.Items, DryRun: true})
		if err != nil {
			return Message{Lines: []string{"⚠️ " + importError(err, locale)}}
		}
		rev.Outcomes = dry.Items
	}
	if m := rev.Result.Match; m != nil {
		// The preview of each change: a charge that can no longer be marked
		// paid (changed meanwhile) is left out rather than failing later.
		kept := m.Matches[:0]
		for _, mt := range m.Matches {
			if mt.Action == app.MatchMarkPaid {
				if _, err := e.svc.SetChargeStatus(ctx, actor, payInput(mt, true)); err != nil {
					continue
				}
			}
			kept = append(kept, mt)
		}
		m.Matches = kept
	}
	if len(reviewLines(rev)) == 0 {
		msg := e.reviewMessage(rev, today, locale)
		msg.Title, msg.Rows = tr(locale, "📄 Nothing to import"), nil
		msg.Lines = append([]string{tr(locale, "Everything in this file is already in Exhale.")}, msg.Lines...)
		return msg
	}
	if err := r.SaveReview(ctx, actor, rev); err != nil {
		e.log.Error("chat import: could not keep the review", "err", err)
		return Message{Lines: []string{tr(locale, "⚠️ Something went wrong. Try again in a moment.")}}
	}
	return e.reviewMessage(rev, today, locale)
}

func payInput(m app.ChargeMatch, dry bool) app.SetChargeStatusInput {
	return app.SetChargeStatusInput{ChargeID: m.Charge.ID, Status: string(domain.ChargePaid), ChargedAmount: m.Paid.Decimal(), ChargedIn: m.Paid.Currency, DryRun: dry}
}

// importError says why a file could not be read, in the member's language
// for the cases a person can act on.
func importError(err error, locale string) string {
	var ae *assistant.Error
	switch {
	case errors.Is(err, assistant.ErrScanNeedsAssistant):
		return tr(locale, "This PDF has no text: it is probably a scan. Reading it needs the built-in assistant, which an owner can turn on in Settings. Or send the statement as CSV.")
	case errors.Is(err, assistant.ErrPDFNeedsAssistant):
		return tr(locale, "I found no list of transactions in this PDF. Send the statement as CSV, or ask an owner to turn on the built-in assistant to read it.")
	case errors.Is(err, assistant.ErrPDFUnreadable):
		return tr(locale, "I could not open this PDF: it may be damaged or protected with a password. Send the statement as CSV instead.")
	case errors.Is(err, assistant.ErrNeedsAssistant):
		return tr(locale, "Reading a picture or free text needs the built-in assistant, which an owner can turn on in Settings. A CSV from your bank works without it.")
	case errors.Is(err, importer.ErrUnknownFormat):
		return tr(locale, "I can't read this kind of file. Send your bank's export (CSV), a PDF statement or a Wallos export.")
	case errors.As(err, &ae):
		return Say(locale, assistant.Explain(err))
	case errors.Is(err, domain.ErrValidation):
		return ErrorText(locale, err)
	}
	return tr(locale, "I could not read that file.")
}

// reviewMessage shows a review: the charges the statement confirms, the
// new subscriptions, and what was expected but not found.
func (e *Engine) reviewMessage(rev assistant.Review, today engine.Date, locale string) Message {
	res := rev.Result
	m := Message{Title: tr(locale, "📄 Read from your file")}
	if res.Match != nil && !res.Match.From.IsZero() {
		m.Lines = append(m.Lines, tr(locale, "Statement from %s to %s.", shortDate(res.Match.From, locale), shortDate(res.Match.To, locale)))
	}
	all := reviewLines(rev)
	long := len(all) > maxChatLines
	on := 0
	for n := range all {
		if !rev.Off[n] {
			on++
		}
	}
	pays, adds := 0, 0
	for _, l := range all {
		if l.pay {
			pays++
		} else {
			adds++
		}
	}
	if long {
		m.Lines = append(m.Lines, tr(locale, "Charges to mark paid: %d. New subscriptions: %d.", pays, adds))
	} else {
		var paid, added Section
		paid.Heading, added.Heading = tr(locale, "Paid, according to the statement"), tr(locale, "New subscriptions")
		for n, l := range all {
			mark := "✅"
			if rev.Off[n] {
				mark = "⬜"
			}
			if l.pay {
				paid.Lines = append(paid.Lines, fmt.Sprintf("%s %d. %s", mark, n+1, payLine(res.Match.Matches[l.i], locale)))
			} else {
				added.Lines = append(added.Lines, fmt.Sprintf("%s %d. %s", mark, n+1, itemLine(res.Items[l.i], locale)))
			}
		}
		for _, s := range []Section{paid, added} {
			if len(s.Lines) > 0 {
				m.Sections = append(m.Sections, s)
			}
		}
	}
	if res.Match != nil && len(res.Match.Missing) > 0 {
		s := Section{Heading: tr(locale, "Expected but not in this statement")}
		for i, c := range res.Match.Missing {
			if i == maxMissingLines {
				s.Lines = append(s.Lines, tr(locale, "…and %d more", len(res.Match.Missing)-maxMissingLines))
				break
			}
			s.Lines = append(s.Lines, tr(locale, "%s · %s due %s", c.SubscriptionName, c.Amount.Format(locale), shortDate(c.DueDate, locale)))
		}
		m.Sections = append(m.Sections, s)
	}
	var notes []string
	if res.Match != nil {
		already := 0
		for _, mt := range res.Match.Matches {
			if mt.Action == app.MatchAlreadyPaid {
				already++
			}
		}
		if already > 0 {
			notes = append(notes, tr(locale, "Already marked paid: %d.", already))
		}
	}
	dups := 0
	for _, o := range rev.Outcomes {
		if o.Action == "duplicate" {
			dups++
		}
	}
	if dups > 0 {
		notes = append(notes, tr(locale, "Already in Exhale: %d.", dups))
	}
	if len(all) == 0 {
		m.Footer = strings.Join(notes, " ")
		return m
	}
	if long {
		notes = append(notes, tr(locale, "Too many to tick here: accept them all, or review them one by one in Exhale (Settings → Data → Import)."))
	} else {
		notes = append(notes, tr(locale, "Tap a number to skip that line."))
		var row []Button
		for n := range all {
			label := "✅ " + strconv.Itoa(n+1)
			if rev.Off[n] {
				label = "⬜ " + strconv.Itoa(n+1)
			}
			row = append(row, Button{Label: label, Data: data(impPrefix, impToggle, rev.ID, strconv.Itoa(n))})
			if len(row) == 4 {
				m.Rows = append(m.Rows, row)
				row = nil
			}
		}
		if len(row) > 0 {
			m.Rows = append(m.Rows, row)
		}
	}
	m.Footer = strings.Join(notes, " ")
	apply := tr(locale, "✅ Accept all (%d)", on)
	if on < len(all) {
		apply = tr(locale, "✅ Apply %d of %d", on, len(all))
	}
	actions := []Button{{Label: apply, Data: data(impPrefix, impApply, rev.ID)}, {Label: tr(locale, "✖ Discard"), Data: data(impPrefix, impDrop, rev.ID)}}
	if on == 0 {
		actions = actions[1:]
	}
	m.Rows = append(m.Rows, actions)
	if long && e.publicURL != "" {
		m.Rows = append(m.Rows, []Button{{Label: tr(locale, "🔗 Review in Exhale"), URL: e.publicURL + "/settings?tab=data"}})
	}
	return m
}

func payLine(m app.ChargeMatch, locale string) string {
	on := m.Transaction.Date
	if d, err := engine.ParseDate(on); err == nil {
		on = shortDate(d, locale)
	}
	s := tr(locale, "%s · %s on %s", m.Charge.SubscriptionName, m.Paid.Format(locale), on)
	if m.Paid != m.Charge.Amount {
		s += " · " + tr(locale, "expected %s", m.Charge.Amount.Format(locale))
	}
	return s
}

func itemLine(it app.ImportItem, locale string) string {
	parts := []string{it.Name}
	if p, err := money.Parse(it.Price, it.Currency); err == nil {
		parts = append(parts, p.Format(locale))
	} else {
		parts = append(parts, strings.TrimSpace(it.Price+" "+it.Currency))
	}
	unit := cmp.Or(it.Unit, "month")
	if it.Interval <= 1 {
		parts = append(parts, cadenceText(map[string]string{"day": "daily", "week": "weekly", "month": "monthly", "year": "yearly"}[unit], locale))
	} else {
		parts = append(parts, cadenceText(fmt.Sprintf("every %d %ss", it.Interval, unit), locale))
	}
	if d, err := engine.ParseDate(it.NextCharge); err == nil {
		parts = append(parts, tr(locale, "next %s", shortDate(d, locale)))
	}
	return strings.Join(parts, " · ")
}

// handleImport runs the buttons of a review.
func (e *Engine) handleImport(ctx context.Context, actor app.Actor, chat ChatRef, raw string) (Reply, error) {
	today, locale := e.context(ctx, actor)
	p := strings.Split(raw, "|")
	r := e.reader()
	if len(p) < 3 || r == nil {
		return Reply{Toast: tr(locale, "This button has expired.")}, nil
	}
	rev, err := r.LoadReview(ctx, actor)
	if err != nil || rev.ID != p[2] {
		m := Message{Title: tr(locale, "📄 This list has expired"), Lines: []string{tr(locale, "Send the file again to see it.")}}
		return Reply{Edit: &m, Toast: tr(locale, "This list has expired.")}, nil
	}
	switch p[1] {
	case impToggle:
		n, err := strconv.Atoi(part(p, 3))
		if err != nil || n < 0 || n >= len(reviewLines(rev)) {
			return Reply{Toast: tr(locale, "This button has expired.")}, nil
		}
		if rev.Off == nil {
			rev.Off = map[int]bool{}
		}
		rev.Off[n] = !rev.Off[n]
		if err := r.SaveReview(ctx, actor, rev); err != nil {
			return Reply{}, err
		}
		m := e.reviewMessage(rev, today, locale)
		return Reply{Edit: &m}, nil
	case impDrop:
		_ = r.DropReview(ctx, actor)
		m := Message{Title: tr(locale, "✖ Not imported"), Lines: []string{tr(locale, "Nothing was changed.")}}
		return Reply{Edit: &m, Toast: tr(locale, "Nothing was changed")}, nil
	case impApply:
		return e.applyReview(ctx, actor, chat, rev, today, locale)
	}
	return Reply{Toast: tr(locale, "Unknown action.")}, nil
}

func part(p []string, i int) string {
	if i < len(p) {
		return p[i]
	}
	return ""
}

// applyReview applies the ticked lines with the same operations as the
// web. A charge paid at another amount gets the usual "new price?"
// question, as its own message.
func (e *Engine) applyReview(ctx context.Context, actor app.Actor, chat ChatRef, rev assistant.Review, today engine.Date, locale string) (Reply, error) {
	_ = e.reader().DropReview(ctx, actor) // applied once, even if pressed twice
	var items []app.ImportItem
	var questions []Message
	paid, failed := 0, 0
	for n, l := range reviewLines(rev) {
		if rev.Off[n] {
			continue
		}
		if !l.pay {
			items = append(items, rev.Result.Items[l.i])
			continue
		}
		res, err := e.svc.SetChargeStatus(ctx, actor, payInput(rev.Result.Match.Matches[l.i], false))
		if err != nil {
			failed++
			e.log.Info("chat import: a charge was not marked paid", "err", err)
			continue
		}
		paid++
		if res.PriceChange != nil {
			questions = append(questions, paidMessage(res, today, locale))
		}
	}
	m := Message{Title: tr(locale, "✅ Imported")}
	if paid > 0 {
		m.Lines = append(m.Lines, tr(locale, "Charges marked paid: %d.", paid))
	}
	if len(items) > 0 {
		res, err := e.svc.Import(ctx, actor, app.ImportInput{Items: items})
		if err != nil {
			m.Lines = append(m.Lines, "⚠️ "+importError(err, locale))
		} else {
			m.Lines = append(m.Lines, tr(locale, "Subscriptions added: %d.", res.Created))
			failed += res.Errors
		}
	}
	if failed > 0 {
		m.Lines = append(m.Lines, tr(locale, "⚠️ Not done: %d (changed meanwhile or not valid).", failed))
	}
	if len(m.Lines) == 0 {
		m.Title, m.Lines = tr(locale, "✖ Not imported"), []string{tr(locale, "Nothing was changed.")}
	} else {
		m.Footer = tr(locale, "Each change can be undone from Activity in Exhale.")
	}
	if ch, ok := e.channels[chat.Channel]; ok {
		for _, q := range questions {
			if _, err := ch.Send(ctx, chat.ChatID, q); err != nil {
				e.log.Warn("chat import: could not ask about a new price", "err", err)
			}
		}
	}
	return Reply{Edit: &m, Toast: tr(locale, "Done ✅")}, nil
}

// paidMessage shows a charge just marked paid; when the bank took another
// amount it asks whether that is the new price.
func paidMessage(res app.ChargeResult, today engine.Date, locale string) Message {
	m := chargeMessage("", res, today, locale)
	if res.AuditID != "" {
		m.Rows = append(m.Rows, []Button{{Label: tr(locale, "↩︎ Undo"), Data: data(vUndo, res.Charge.ID, res.AuditID)}})
	}
	if h := res.PriceChange; h != nil {
		m.Sections = append(m.Sections, Section{Heading: tr(locale, "New price?"), Lines: []string{
			tr(locale, "It took %s instead of %s (%s%%, %s a year).", h.Charged.Format(locale), h.Expected.Format(locale), h.Change, signed(h.YearlyDelta.Format(locale), h.YearlyDelta.Minor)),
		}})
		m.Rows = append([][]Button{{{Label: tr(locale, "Yes, from now on"), Data: data(vNewPrice, res.Charge.ID, h.Charged.Decimal())}, {Label: tr(locale, "Just this time"), Data: data(vBack, res.Charge.ID)}}}, m.Rows...)
	}
	return m
}
