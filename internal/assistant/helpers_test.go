package assistant

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// world is a household with an assistant driven by the fake provider.
type world struct {
	t     *testing.T
	ctx   context.Context
	st    *store.Store
	svc   *app.Service
	owner app.Actor
	a     *Assistant
	fake  *Fake
	out   strings.Builder // the transcript, compared with a golden file
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	madrid, _ := time.LoadLocation("Europe/Madrid")
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, madrid)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	owner.Scope = app.ScopeWrite
	a, err := New(svc, nil, Env{})
	if err != nil {
		t.Fatal(err)
	}
	f := &Fake{}
	a.SetProvider(f)
	return &world{t: t, ctx: ctx, st: st, svc: svc, owner: owner, a: a, fake: f}
}

// member adds a second person to the household.
func (w *world) member(name string) app.Actor {
	w.t.Helper()
	id := "mem_" + strings.ToLower(name)
	if err := w.st.Q.CreateMember(w.ctx, db.CreateMemberParams{ID: id, HouseholdID: w.owner.HouseholdID, Name: name, Role: "member", CreatedAt: store.FormatTime(w.svc.Now())}); err != nil {
		w.t.Fatal(err)
	}
	m, err := w.svc.ActorForMember(w.ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return m
}

func (w *world) sub(actor app.Actor, in app.CreateSubscriptionInput) app.SubscriptionView {
	w.t.Helper()
	if in.Currency == "" {
		in.Currency = "EUR"
	}
	d, err := w.svc.CreateSubscription(w.ctx, actor, in)
	if err != nil {
		w.t.Fatal(err)
	}
	return d.Subscription
}

func (w *world) names(actor app.Actor) string {
	w.t.Helper()
	list, err := w.svc.ListSubscriptions(w.ctx, actor, app.ListSubscriptionsInput{})
	if err != nil {
		w.t.Fatal(err)
	}
	var n []string
	for _, s := range list.Subscriptions {
		n = append(n, s.Name+":"+string(s.Status))
	}
	return strings.Join(n, ",")
}

// say sends a message as actor and writes what happened to the transcript:
// the tools the model called, what Exhale answered each, the answer and the
// changes prepared.
func (w *world) say(actor app.Actor, text string) Turn {
	w.t.Helper()
	before := w.fake.Calls()
	turn, err := w.a.Ask(w.ctx, actor, "test_"+actor.MemberID, text)
	w.out.WriteString("> " + text + "\n")
	if err != nil {
		w.out.WriteString("! " + Explain(err).String() + "\n")
		return turn
	}
	if n := w.fake.Calls(); n > before {
		msgs := w.fake.Requests[n-1].Messages
		start := 0
		for i, m := range msgs {
			if m.Role == RoleUser {
				start = i + 1
			}
		}
		for _, m := range msgs[start:] {
			for _, c := range m.ToolCalls {
				w.out.WriteString("  model → " + c.Name + "(" + string(c.Arguments) + ")\n")
			}
			for _, r := range m.ToolResults {
				line, _, _ := strings.Cut(r.Content, "\n")
				if !r.IsError && !strings.HasPrefix(line, "PREVIEW") && !strings.HasPrefix(line, "This change") {
					line = "ok"
				}
				w.out.WriteString("  exhale ← " + line + "\n")
			}
		}
	}
	if turn.Text != "" {
		w.out.WriteString("< " + strings.ReplaceAll(turn.Text, "\n", "\n  ") + "\n")
	}
	if turn.Note != nil {
		w.out.WriteString("  (" + turn.Note.String() + ")\n")
	}
	for _, p := range turn.Proposals {
		mark := "[proposal]"
		if p.Destructive {
			mark = "[proposal, destructive]"
		}
		w.out.WriteString("  " + mark + " " + p.Title.String() + "\n")
		for _, l := range p.Lines {
			w.out.WriteString("    " + l.String() + "\n")
		}
	}
	for _, m := range turn.Mentions {
		w.out.WriteString("  [link] " + m.Name + "\n")
	}
	return turn
}

func (w *world) outcome(label string, o Outcome, err error) Outcome {
	w.t.Helper()
	w.out.WriteString("> press " + label + "\n")
	if err != nil {
		w.out.WriteString("! " + Explain(err).String() + "\n")
		return o
	}
	undo := ""
	if o.CanUndo {
		undo = " (Undo offered)"
	}
	w.out.WriteString("  [" + o.Status + "] " + o.Title.String() + undo + "\n")
	for _, l := range o.Lines {
		w.out.WriteString("    " + l.String() + "\n")
	}
	return o
}

func (w *world) confirm(actor app.Actor, p Proposal) Outcome {
	o, err := w.a.Decide(w.ctx, actor, p.ID, true)
	return w.outcome("Confirm", o, err)
}

func (w *world) discard(actor app.Actor, p Proposal) Outcome {
	o, err := w.a.Decide(w.ctx, actor, p.ID, false)
	return w.outcome("Discard", o, err)
}

func (w *world) undo(actor app.Actor, p Proposal) Outcome {
	o, err := w.a.Undo(w.ctx, actor, p.ID)
	return w.outcome("Undo", o, err)
}

var randomID = regexp.MustCompile(`\b(sub|chg|aud|pm|cat|mem|hh)_[0-9a-z]{16}\b`)

// transcript returns the conversation with random ids made stable.
func (w *world) transcript() string {
	return randomID.ReplaceAllString(w.out.String(), "${1}_X")
}

// lastUserText is the newest message of the person in a request, briefing
// included.
func lastUserText(req Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == RoleUser {
			return req.Messages[i].Text
		}
	}
	return ""
}

// joined is the English of texts, joined by spaces.
func joined(ts []domain.Text) string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.String()
	}
	return strings.Join(out, " ")
}
