package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Proposal is a change the model prepared. Its title and lines are written
// by Exhale from the arguments and the dry run, never by the model, so the
// card the person confirms says what will really happen.
type Proposal struct {
	ID          string   `json:"id"`
	Tool        string   `json:"tool" doc:"the use case that would run, e.g. create_subscription"`
	Title       string   `json:"title" doc:"what would happen, e.g. Add Netflix"`
	Lines       []string `json:"lines" doc:"the details of the change"`
	Destructive bool     `json:"destructive" doc:"it deletes or cancels"`
}

// Outcome statuses.
const (
	StatusPending   = "pending"
	StatusApplied   = "applied"
	StatusDiscarded = "discarded"
	StatusFailed    = "failed"
	StatusUndone    = "undone"
)

// Outcome is what happened to a proposal.
type Outcome struct {
	ID      string   `json:"id"`
	Status  string   `json:"status" enum:"pending,applied,discarded,failed,undone"`
	Title   string   `json:"title"`
	Lines   []string `json:"lines"`
	CanUndo bool     `json:"canUndo" doc:"an applied change that Undo can revert"`
	// SubscriptionID is the subscription the change was about, for a link.
	SubscriptionID string `json:"subscriptionId,omitempty"`
}

// stored is a proposal as kept in the flows table until the person decides.
type stored struct {
	Proposal
	Conv           string          `json:"conv"`
	Args           json.RawMessage `json:"args"`
	Status         string          `json:"status"`
	Audits         []app.AuditRef  `json:"audits,omitempty"`
	SubscriptionID string          `json:"subscriptionId,omitempty"`
	Result         []string        `json:"result,omitempty"`
}

// proposalID is short because chat buttons carry it: "ai|ok|" plus the id
// must fit Telegram's 64 bytes of callback data.
func proposalID() string { return "p" + auth.Random(8) }

func proposalKey(id string) string { return "aip_" + id }

func (a *Assistant) saveProposal(ctx context.Context, memberID string, s stored, ttl time.Duration) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	now := time.Now()
	return a.svc.Store().Q.SaveFlow(ctx, db.SaveFlowParams{ID: proposalKey(s.ID), MemberID: memberID, State: string(b), ExpiresAt: store.FormatTime(now.Add(ttl)), CreatedAt: store.FormatTime(now)})
}

func (a *Assistant) loadProposal(ctx context.Context, actor app.Actor, id string) (stored, error) {
	var s stored
	row, err := a.svc.Store().Q.GetFlow(ctx, proposalKey(id))
	// A proposal belongs to the member it was made for: nobody else can
	// confirm it, even with its id.
	if err != nil || row.MemberID != actor.MemberID || store.ParseTime(row.ExpiresAt).Before(time.Now()) {
		return s, errKind(KindExpired, "")
	}
	if err := json.Unmarshal([]byte(row.State), &s); err != nil {
		return s, errKind(KindExpired, "")
	}
	return s, nil
}

func (a *Assistant) propose(ctx context.Context, st *turnState, t tool, args map[string]any, final json.RawMessage, preview any) (Proposal, error) {
	_, locale := a.today(ctx, st.agent)
	title, lines, subID := a.describe(ctx, st.agent, t, args, preview, locale)
	pr := Proposal{ID: proposalID(), Tool: t.Name, Title: title, Lines: lines, Destructive: t.Destructive}
	if subID != "" {
		st.touched = append(st.touched, subID)
	}
	return pr, a.saveProposal(ctx, st.actor.MemberID, stored{Proposal: pr, Conv: st.conv, Args: final, Status: StatusPending, SubscriptionID: subID}, proposalTTL)
}

func (a *Assistant) today(ctx context.Context, actor app.Actor) (engine.Date, string) {
	info, err := a.svc.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return engine.DateOf(a.svc.Now()), "en"
	}
	return info.Today, info.Household.Locale
}

func (s stored) outcome() Outcome {
	o := Outcome{ID: s.ID, Status: s.Status, Title: s.Title, Lines: s.Lines, SubscriptionID: s.SubscriptionID}
	if s.Status != StatusPending {
		o.Lines = s.Result
	}
	if o.Lines == nil {
		o.Lines = []string{}
	}
	if s.Status == StatusApplied {
		for _, r := range s.Audits {
			if undoable(r) {
				o.CanUndo = true
			}
		}
	}
	return o
}

// undoable reports whether app.Undo can revert a recorded change.
func undoable(r app.AuditRef) bool {
	return (r.Entity == "subscription" || r.Entity == "charge") && r.Action != "undo"
}

// Decide applies a proposal (accept) or drops it. Only the member it was
// made for can decide, and only once: a second press returns what already
// happened.
func (a *Assistant) Decide(ctx context.Context, actor app.Actor, id string, accept bool) (Outcome, error) {
	s, event, err := a.decide(ctx, actor, id, accept)
	if err != nil {
		return Outcome{}, err
	}
	if event != "" {
		a.remember(ctx, actor, s.Conv, event)
	}
	return s.outcome(), nil
}

func (a *Assistant) decide(ctx context.Context, actor app.Actor, id string, accept bool) (s stored, event string, err error) {
	a.deciding.Lock()
	defer a.deciding.Unlock()
	if s, err = a.loadProposal(ctx, actor, id); err != nil {
		return s, "", err
	}
	if s.Status != StatusPending {
		return s, "", nil
	}
	t, ok := a.bindings[s.Tool]
	if !ok {
		return s, "", errKind(KindExpired, "")
	}
	if !accept {
		s.Status = StatusDiscarded
		return s, "The person discarded: " + s.Title + ". Nothing was changed.", a.saveProposal(ctx, actor.MemberID, s, proposalTTL)
	}
	cctx, refs := app.CollectAudits(ctx)
	out, err := t.Invoke(a.svc, cctx, agentFor(actor), s.Args)
	if err != nil {
		s.Status, s.Result = StatusFailed, []string{failure(err)}
		_ = a.saveProposal(ctx, actor.MemberID, s, proposalTTL)
		return s, "The person confirmed: " + s.Title + ", but it failed: " + failure(err), nil
	}
	s.Status, s.Audits, s.Result = StatusApplied, *refs, []string{}
	if d, ok := out.(app.SubscriptionDetail); ok {
		s.SubscriptionID = d.Subscription.ID // the id a dry run returned was not saved
	}
	if r, ok := out.(app.ImportResult); ok {
		s.Result = []string{fmt.Sprintf("%d created, %d already there, %d with problems", r.Created, r.Duplicates, r.Errors)}
	}
	if t.Name == "delete_subscription" {
		s.SubscriptionID = ""
	}
	if err := a.saveProposal(ctx, actor.MemberID, s, appliedTTL); err != nil {
		a.log.Warn("assistant: could not keep the applied change for undo", "err", err)
	}
	a.log.Info("assistant change applied", "tool", s.Tool, "member", actor.MemberID, "audits", len(s.Audits))
	return s, "The person confirmed: " + s.Title + ". It is done.", nil
}

// Undo reverts a change applied from a proposal, newest part first.
func (a *Assistant) Undo(ctx context.Context, actor app.Actor, id string) (Outcome, error) {
	o, conv, event, err := a.undo(ctx, actor, id)
	if err != nil {
		return Outcome{}, err
	}
	if event != "" {
		a.remember(ctx, actor, conv, event)
	}
	return o, nil
}

func (a *Assistant) undo(ctx context.Context, actor app.Actor, id string) (o Outcome, conv, event string, err error) {
	a.deciding.Lock()
	defer a.deciding.Unlock()
	s, err := a.loadProposal(ctx, actor, id)
	if err != nil {
		return Outcome{}, "", "", err
	}
	if s.Status != StatusApplied {
		return s.outcome(), s.Conv, "", nil
	}
	agent := agentFor(actor)
	var kept []string
	undone := 0
	for i := len(s.Audits) - 1; i >= 0; i-- {
		r := s.Audits[i]
		if !undoable(r) {
			kept = append(kept, strings.ReplaceAll(r.Entity, "_", " "))
			continue
		}
		if _, err := a.svc.Undo(ctx, agent, app.UndoInput{AuditID: r.ID}); err != nil {
			s.Result = []string{failure(err)}
			_ = a.saveProposal(ctx, actor.MemberID, s, appliedTTL)
			o := s.outcome()
			o.Status = StatusFailed
			return o, s.Conv, "", nil
		}
		undone++
	}
	if undone == 0 {
		return s.outcome(), s.Conv, "", nil
	}
	s.Status, s.Result = StatusUndone, []string{}
	if len(kept) > 0 {
		sort.Strings(kept)
		s.Result = []string{"Kept, because Undo does not cover it yet: " + strings.Join(compact(kept), ", ") + "."}
	}
	if s.Tool == "create_subscription" || s.Tool == "import_subscriptions" {
		s.SubscriptionID = ""
	}
	if err := a.saveProposal(ctx, actor.MemberID, s, proposalTTL); err != nil {
		return Outcome{}, "", "", err
	}
	return s.outcome(), s.Conv, "The person undid: " + s.Title + ".", nil
}

func compact(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// failure is the reason a change did not apply, as a sentence.
func failure(err error) string {
	msg := err.Error()
	for _, sentinel := range []error{domain.ErrValidation, domain.ErrNotFound, domain.ErrConflict, app.ErrForbidden} {
		msg = strings.TrimPrefix(msg, sentinel.Error()+": ")
	}
	if msg == "" {
		return "It could not be applied."
	}
	return strings.ToUpper(msg[:1]) + msg[1:]
}

// --- what a proposal says ---------------------------------------------------------

func str(args map[string]any, key string) string {
	switch v := args[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// labels are the names of arguments as a person reads them.
var labels = map[string]string{
	"name": "Name", "price": "Price", "currency": "Currency", "interval": "Every", "unit": "Unit", "nextCharge": "Next charge",
	"firstCharge": "First charge", "endDate": "Ends", "priceFrom": "New price from", "categoryId": "Category", "paymentMethodId": "Payment method",
	"payerId": "Paid by", "private": "Private", "url": "Link", "notes": "Notes", "autoConfirm": "Mark paid automatically",
	"reminderDays": "Remind (days before)", "reminderHour": "Remind at (hour)", "startedOn": "Subscribed since", "trialEndsOn": "Free trial ends",
	"noticeDays": "Notice (days)", "renewal": "Renewal", "cancelUrl": "Cancel page", "account": "Account", "shares": "Split", "using": "Still using it",
	"status": "Status", "kind": "Kind", "last4": "Last four digits", "expiresOn": "Expires", "ownerId": "Owner", "moveTo": "Move its subscriptions to",
	"from": "From", "to": "To", "timezone": "Time zone", "locale": "Language", "color": "Colour", "auditId": "Change", "scope": "Applies to",
	"note": "Note", "chargedAmount": "Amount taken", "chargedCurrency": "In", "newDate": "New date", "amount": "Amount", "decision": "Decision",
	"allowDuplicates": "Also import duplicates", "reminderTime": "Reminder time", "payday": "Payday",
}

// argLines lists arguments as "Label: value", in a stable order, leaving
// out the ones named in skip.
func argLines(args map[string]any, names map[string]string, skip ...string) []string {
	hidden := map[string]bool{"id": true, "chargeId": true, "dryRun": true, "confirm": true}
	for _, s := range skip {
		hidden[s] = true
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		if !hidden[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	lines := []string{}
	for _, k := range keys {
		label := labels[k]
		if label == "" {
			label = k
		}
		v := str(args, k)
		if n, ok := names[v]; ok && strings.HasSuffix(k, "Id") {
			v = n // an id the person never saw: show the name
		}
		if v == "" {
			v = "(none)"
		}
		lines = append(lines, label+": "+truncate(v, 200))
	}
	return lines
}

func scopeLine(args map[string]any, def string) string {
	scope := str(args, "scope")
	if scope == "" {
		scope = def
	}
	if scope == "following" {
		return "This charge and the ones after it."
	}
	return "Only this charge."
}

const providerHint = "Exhale only stops tracking it: cancel it with the provider too."

// describe writes the card of a proposal from the arguments and the dry
// run. It returns the id of the subscription it is about, if any.
func (a *Assistant) describe(ctx context.Context, agent app.Actor, t tool, args map[string]any, preview any, locale string) (title string, lines []string, subID string) {
	names := map[string]string{}
	if info, err := a.svc.GetHousehold(ctx, agent, struct{}{}); err == nil {
		for _, m := range info.Members {
			names[m.ID] = m.Name
		}
		for _, c := range info.Categories {
			names[c.ID] = c.Name
		}
		for _, p := range info.PaymentMethods {
			names[p.ID] = p.Label()
		}
	}
	switch out := preview.(type) {
	case app.SubscriptionDetail:
		s := out.Subscription
		subID = s.ID
		switch t.Name {
		case "create_subscription":
			subID = "" // not saved yet
			lines = []string{s.Price.Format(locale) + " " + s.Cadence}
			if !s.NextCharge.IsZero() {
				lines = append(lines, "First charge: "+s.NextCharge.String())
			}
			lines = append(lines, argLines(args, names, "name", "price", "currency", "interval", "unit", "firstCharge")...)
			return "Add " + s.Name, lines, subID
		case "set_subscription_status":
			name := str(args, "name")
			if sub, err := a.svc.GetSubscription(ctx, agent, app.GetSubscriptionInput{ID: str(args, "id")}); err == nil {
				name = sub.Subscription.Name
			}
			switch str(args, "action") {
			case "pause":
				return "Pause " + name, []string{"No charges or reminders until it is resumed."}, subID
			case "resume":
				return "Resume " + name, []string{}, subID
			case "cancel_now":
				return "Cancel " + name + " now", []string{"No more charges from today.", providerHint}, subID
			default:
				lines = []string{"No more charges."}
				if !s.CancelledUntil.IsZero() {
					lines = []string{"Service until " + s.CancelledUntil.String() + ". No more charges."}
				}
				return "Cancel " + name + " at the end of the period", append(lines, providerHint), subID
			}
		default:
			name := s.Name
			if before, err := a.svc.GetSubscription(ctx, agent, app.GetSubscriptionInput{ID: str(args, "id")}); err == nil {
				name = before.Subscription.Name
			}
			return "Change " + name, argLines(args, names), subID
		}
	case app.ChargeResult:
		subID = out.Subscription.ID
		name, due := out.Subscription.Name, out.Charge.DueDate.String()
		if list, err := a.svc.ListCharges(ctx, agent, app.ListChargesInput{From: "2000-01-01", To: "2100-12-31"}); err == nil {
			for _, c := range list.Charges {
				if c.ID == str(args, "chargeId") {
					due = c.DueDate.String() // the date before the change
				}
			}
		}
		switch t.Name {
		case "change_charge_date":
			return fmt.Sprintf("Move the %s charge of %s to %s", due, name, str(args, "newDate")), []string{scopeLine(args, "this")}, subID
		case "change_charge_amount":
			return fmt.Sprintf("Change the %s charge of %s to %s", due, name, out.Charge.Amount.Format(locale)), []string{scopeLine(args, "following")}, subID
		case "set_charge_status":
			verb := map[string]string{"paid": "Mark as paid", "failed": "Mark as failed", "skipped": "Skip", "pending": "Reopen"}[str(args, "status")]
			if verb == "" {
				verb = "Update"
			}
			return fmt.Sprintf("%s: %s, %s (%s)", verb, name, due, out.Charge.Amount.Format(locale)), argLines(args, names, "status"), subID
		case "decide_renewal":
			return fmt.Sprintf("Keep the %s renewal of %s", due, name), []string{}, subID
		}
		return t.Title + ": " + name, argLines(args, names), subID
	case app.DeleteResult:
		if t.Name == "delete_subscription" {
			name := out.Deleted
			if sub, err := a.svc.GetSubscription(ctx, agent, app.GetSubscriptionInput{ID: str(args, "id")}); err == nil {
				name = sub.Subscription.Name
			}
			return "Delete " + name, []string{"Deletes the subscription and all its charges. To stop paying but keep the history, cancel it instead."}, out.Deleted
		}
		name := names[str(args, "id")]
		if name == "" {
			name = str(args, "id")
		}
		return t.Title + ": " + name, argLines(args, names), ""
	case app.ImportResult:
		lines = []string{}
		for i, it := range out.Items {
			if i == 8 {
				lines = append(lines, fmt.Sprintf("… and %d more", len(out.Items)-8))
				break
			}
			switch it.Action {
			case "create":
				lines = append(lines, "+ "+it.Name)
			case "duplicate":
				lines = append(lines, "= "+it.Name+" (already in Exhale, skipped)")
			default:
				lines = append(lines, "! "+it.Name+" (skipped: "+it.Message+")")
			}
		}
		if len(out.NewCategories) > 0 {
			lines = append(lines, "New categories: "+strings.Join(out.NewCategories, ", "))
		}
		if len(out.NewPaymentMethods) > 0 {
			lines = append(lines, "New payment methods: "+strings.Join(out.NewPaymentMethods, ", "))
		}
		return fmt.Sprintf("Import %d subscription%s", out.Created, plural(out.Created)), lines, ""
	}
	// Everything else: the use case's own title and its arguments.
	title = t.Title
	if n := names[str(args, "id")]; n != "" {
		title += ": " + n
	} else if n := str(args, "name"); n != "" {
		title += ": " + n
	}
	return title, argLines(args, names, "name"), ""
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
