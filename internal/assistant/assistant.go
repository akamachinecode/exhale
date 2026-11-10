package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
	"github.com/akamachinecode/exhale/internal/toolschema"
)

// Limits. They bound what one message can cost and how long it can take.
const (
	// MaxRounds is how many times the model is called for one message.
	MaxRounds = 6
	// MaxProposals is how many changes one message can prepare.
	MaxProposals = 5
	// MaxMessageLen is the longest message a person can send, in bytes.
	MaxMessageLen = 4000
	// maxToolResult bounds a tool result handed to the model, in bytes.
	maxToolResult = 30_000
	// chatTokens bounds one answer, reasoning included.
	chatTokens = 8192
	// callTimeout bounds one call to the model; turnTimeout the whole
	// message.
	callTimeout = 60 * time.Second
	turnTimeout = 150 * time.Second
	// Memory: the last turns of a conversation, kept for a while.
	memoryTurns   = 6
	memoryTTL     = 30 * time.Minute
	proposalTTL   = 30 * time.Minute
	appliedTTL    = 24 * time.Hour // how long an applied change keeps its Undo button
	memoryTextMax = 1500
)

// Assistant answers plain language with the household's data and prepares
// changes for the person to confirm.
type Assistant struct {
	svc *app.Service
	log *slog.Logger
	env Env

	tools    []Tool
	bindings map[string]tool

	mu        sync.Mutex
	cached    Provider
	cachedFor Config
	fixed     Provider // tests: use this provider whatever the settings say
	limits    map[string]*rate.Limiter
	locks     map[string]*sync.Mutex
}

type tool struct {
	app.Binding
	dryRun bool // the input has a dryRun field
}

// excluded lists the use cases the assistant does not offer, with the
// reason. Everything else in the catalog is a tool.
var excluded = map[string]string{
	"export_data": "the whole household in one result costs more tokens than any question needs; the web exports it",
}

// New returns an assistant. It is off until an owner configures it (or env
// does).
func New(svc *app.Service, log *slog.Logger, env Env) (*Assistant, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	a := &Assistant{svc: svc, log: log, env: env, bindings: map[string]tool{}, limits: map[string]*rate.Limiter{}, locks: map[string]*sync.Mutex{}}
	for _, b := range app.Catalog() {
		if _, skip := excluded[b.Name]; skip {
			continue
		}
		t := tool{Binding: b, dryRun: toolschema.HasField(b, "dryRun")}
		if b.Write && !t.dryRun {
			// Without a preview a write cannot go through the confirmation
			// gate, so it is not offered at all.
			return nil, fmt.Errorf("assistant: write tool %s has no dryRun field", b.Name)
		}
		schema, err := toolschema.Input(b)
		if err != nil {
			return nil, err
		}
		desc := b.Description
		if b.Write {
			// The gate decides when a change is applied, not the model.
			delete(schema.Properties, "dryRun")
			desc += " This only prepares the change: Exhale shows the person a preview and applies it when they confirm."
		}
		raw, err := json.Marshal(schema)
		if err != nil {
			return nil, err
		}
		a.tools = append(a.tools, Tool{Name: b.Name, Description: desc, Schema: raw})
		a.bindings[b.Name] = t
	}
	return a, nil
}

// SetProvider makes the assistant use p whatever the settings say. Tests
// use it with the fake provider.
func (a *Assistant) SetProvider(p Provider) { a.fixed = p }

// Tools lists the tools offered to the model.
func (a *Assistant) Tools() []Tool { return a.tools }

// agentFor returns the actor the model acts as: the member who is talking,
// with the same visibility and never more scope, labelled so the audit log
// shows the assistant made the change for them.
func agentFor(actor app.Actor) app.Actor {
	name, _, _ := strings.Cut(actor.Label, " (")
	a := actor
	a.Kind = app.ActorAgent
	a.Label = "Exhale assistant (" + name + ")"
	if a.Scope == app.ScopeTrusted {
		a.Scope = app.ScopeWrite
	}
	return a
}

// Turn is the assistant's answer to one message.
type Turn struct {
	Text      string     `json:"text" doc:"the answer, plain text"`
	Proposals []Proposal `json:"proposals" doc:"changes prepared for the person to confirm or discard; nothing is applied until they confirm"`
	Mentions  []Mention  `json:"mentions" doc:"subscriptions the answer talks about, for links"`
	Note      string     `json:"note,omitempty" doc:"why the answer may be incomplete, e.g. the step limit was reached"`
}

// Mention is a subscription the answer talks about.
type Mention struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type memory struct {
	Turns []memTurn `json:"turns"`
	// Events is what the person did with proposals since their last
	// message, told to the model with the next one.
	Events []string `json:"events,omitempty"`
}

type memTurn struct {
	User      string `json:"user"`
	Assistant string `json:"assistant"`
}

func memoryID(conv string) string { return "ai_" + conv }

func (a *Assistant) lock(key string) func() {
	a.mu.Lock()
	m, ok := a.locks[key]
	if !ok {
		if len(a.locks) > 10_000 {
			a.locks = map[string]*sync.Mutex{}
		}
		m = &sync.Mutex{}
		a.locks[key] = m
	}
	a.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// allow applies the per-member limit: a burst of 12 messages, then one
// every 15 seconds.
func (a *Assistant) allow(memberID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.limits) > 10_000 {
		a.limits = map[string]*rate.Limiter{}
	}
	l, ok := a.limits[memberID]
	if !ok {
		l = rate.NewLimiter(rate.Every(15*time.Second), 12)
		a.limits[memberID] = l
	}
	return l.Allow()
}

func (a *Assistant) loadMemory(ctx context.Context, actor app.Actor, conv string) memory {
	var m memory
	row, err := a.svc.Store().Q.GetFlow(ctx, memoryID(conv))
	if err != nil || row.MemberID != actor.MemberID || store.ParseTime(row.ExpiresAt).Before(time.Now()) {
		return m
	}
	_ = json.Unmarshal([]byte(row.State), &m)
	return m
}

func (a *Assistant) saveMemory(ctx context.Context, actor app.Actor, conv string, m memory) {
	if len(m.Turns) > memoryTurns {
		m.Turns = m.Turns[len(m.Turns)-memoryTurns:]
	}
	b, _ := json.Marshal(m)
	now := time.Now()
	if err := a.svc.Store().Q.SaveFlow(ctx, db.SaveFlowParams{ID: memoryID(conv), MemberID: actor.MemberID, State: string(b), ExpiresAt: store.FormatTime(now.Add(memoryTTL)), CreatedAt: store.FormatTime(now)}); err != nil {
		a.log.Warn("assistant: could not keep the conversation", "err", err)
	}
}

// Reset forgets the conversation.
func (a *Assistant) Reset(ctx context.Context, actor app.Actor, conv string) {
	row, err := a.svc.Store().Q.GetFlow(ctx, memoryID(conv))
	if err == nil && row.MemberID == actor.MemberID {
		_ = a.svc.Store().Q.DeleteFlow(ctx, row.ID)
	}
}

// remember adds what the person did with a proposal to the conversation,
// so the model knows it with their next message.
func (a *Assistant) remember(ctx context.Context, actor app.Actor, conv, event string) {
	if conv == "" {
		return
	}
	unlock := a.lock("conv:" + conv)
	defer unlock()
	m := a.loadMemory(ctx, actor, conv)
	if len(m.Turns) == 0 {
		return // the conversation is over; nothing to add to
	}
	m.Events = append(m.Events, event)
	if len(m.Events) > 10 {
		m.Events = m.Events[len(m.Events)-10:]
	}
	a.saveMemory(ctx, actor, conv, m)
}

// Ask answers one message of a conversation. conv identifies the
// conversation (a chat, or a member's web panel); its last turns are kept
// for half an hour so follow-up questions work.
func (a *Assistant) Ask(ctx context.Context, actor app.Actor, conv, text string) (Turn, error) {
	turn := Turn{Proposals: []Proposal{}, Mentions: []Mention{}}
	text = strings.TrimSpace(text)
	if text == "" {
		return turn, nil
	}
	if len(text) > MaxMessageLen {
		return turn, errKind(KindTooLarge, "message too long")
	}
	p, err := a.provider(ctx)
	if err != nil {
		return turn, err
	}
	if !a.allow(actor.MemberID) {
		return turn, errKind(KindBusy, "")
	}
	unlock := a.lock("conv:" + conv)
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	_ = a.svc.Store().Q.DeleteExpiredFlows(ctx, store.FormatTime(time.Now()))

	agent := agentFor(actor)
	briefing, err := a.svc.BuildContext(ctx, agent, struct{}{})
	if err != nil {
		return turn, err
	}
	mem := a.loadMemory(ctx, actor, conv)
	var msgs []Message
	for _, t := range mem.Turns {
		msgs = append(msgs, Message{Role: RoleUser, Text: t.User}, Message{Role: RoleAssistant, Text: t.Assistant})
	}
	var b strings.Builder
	b.WriteString("<briefing>\n" + dataText(briefing.Markdown) + "\n</briefing>\n")
	if len(mem.Events) > 0 {
		b.WriteString("\n<since_your_last_answer>\n")
		for _, e := range mem.Events {
			b.WriteString("- " + dataText(e) + "\n")
		}
		b.WriteString("</since_your_last_answer>\n")
	}
	b.WriteString("\n" + text)
	msgs = append(msgs, Message{Role: RoleUser, Text: b.String()})

	st := &turnState{conv: conv, actor: actor, agent: agent, seen: map[string]bool{}}
	var usage Usage
	rounds := 0
	for rounds < MaxRounds {
		rounds++
		res, err := p.Complete(ctx, Request{System: systemPrompt, Messages: msgs, Tools: a.tools, MaxTokens: chatTokens})
		if err != nil {
			a.log.Warn("assistant: the model call failed", "provider", p.Name(), "round", rounds, "err", err)
			if len(st.proposals) == 0 {
				return turn, err
			}
			// Keep what was prepared before the failure.
			turn.Note = Explain(err)
			break
		}
		usage.Input, usage.Output = usage.Input+res.Usage.Input, usage.Output+res.Usage.Output
		if res.Text != "" {
			turn.Text = res.Text
		}
		if res.Stop == StopLength {
			turn.Note = "The answer was cut short because it was too long."
		}
		if len(res.ToolCalls) == 0 {
			break
		}
		if rounds == MaxRounds {
			turn.Note = fmt.Sprintf("I stopped after %d steps. Ask for one thing at a time if something is missing.", MaxRounds)
			break
		}
		msgs = append(msgs, Message{Role: RoleAssistant, Text: res.Text, ToolCalls: res.ToolCalls, Raw: res.Raw})
		results := make([]ToolResult, 0, len(res.ToolCalls))
		for _, c := range res.ToolCalls {
			results = append(results, a.runTool(ctx, st, c))
		}
		msgs = append(msgs, Message{Role: RoleTool, ToolResults: results})
	}
	turn.Proposals = append(turn.Proposals, st.proposals...)
	if turn.Text == "" && len(turn.Proposals) == 0 && turn.Note == "" {
		turn.Text = "I have no answer for that."
	}
	turn.Mentions = a.mentions(ctx, agent, turn, st)
	// Token counts and round counts only: never the words.
	a.log.Info("assistant answered", "provider", p.Name(), "rounds", rounds, "proposals", len(turn.Proposals), "input_tokens", usage.Input, "output_tokens", usage.Output)

	said := turn.Text
	for _, pr := range turn.Proposals {
		said += "\n[prepared for confirmation: " + pr.Title + "]"
	}
	if strings.TrimSpace(said) == "" {
		said = "(no answer)"
	}
	mem.Turns = append(mem.Turns, memTurn{User: truncate(text, memoryTextMax), Assistant: truncate(strings.TrimSpace(said), memoryTextMax)})
	mem.Events = nil
	a.saveMemory(ctx, actor, conv, mem)
	return turn, nil
}

type turnState struct {
	conv      string
	actor     app.Actor
	agent     app.Actor
	proposals []Proposal
	seen      map[string]bool // write calls already prepared in this turn
	touched   []string        // subscription ids the proposals are about
}

// runTool runs one tool call. Reads run as the member. Writes never apply
// here: they are previewed with dryRun and kept as a proposal.
func (a *Assistant) runTool(ctx context.Context, st *turnState, c ToolCall) ToolResult {
	fail := func(msg string) ToolResult {
		return ToolResult{CallID: c.ID, Name: c.Name, Content: "error: " + msg, IsError: true}
	}
	t, ok := a.bindings[c.Name]
	if !ok {
		return fail("there is no tool called " + c.Name)
	}
	args := map[string]any{}
	if len(c.Arguments) > 0 && string(c.Arguments) != "null" {
		if err := json.Unmarshal(c.Arguments, &args); err != nil {
			return fail("the arguments are not a JSON object")
		}
	}
	// The gate owns these two; whatever the model sent is dropped.
	delete(args, "dryRun")
	delete(args, "confirm")
	for k, v := range args {
		if v == nil {
			delete(args, k)
		}
	}
	if !t.Write {
		raw, _ := json.Marshal(args)
		out, err := t.Invoke(a.svc, ctx, st.agent, raw)
		if err != nil {
			return fail(toolschema.ErrorText(err))
		}
		return ToolResult{CallID: c.ID, Name: c.Name, Content: resultText(out)}
	}
	final, _ := json.Marshal(args)
	if st.seen[c.Name+string(final)] {
		return ToolResult{CallID: c.ID, Name: c.Name, Content: "This change is already prepared and waiting for the person to confirm. Do not prepare it again."}
	}
	if len(st.proposals) >= MaxProposals {
		return fail(fmt.Sprintf("%d changes are already waiting for confirmation in this message; let the person confirm those first", MaxProposals))
	}
	args["dryRun"] = true
	dry, _ := json.Marshal(args)
	preview, err := t.Invoke(a.svc, ctx, st.agent, dry)
	if err != nil {
		return fail(toolschema.ErrorText(err))
	}
	delete(args, "dryRun")
	pr, err := a.propose(ctx, st, t, args, final, preview)
	if err != nil {
		return fail("the change could not be prepared")
	}
	st.seen[c.Name+string(final)] = true
	st.proposals = append(st.proposals, pr)
	return ToolResult{CallID: c.ID, Name: c.Name, Content: "PREVIEW ONLY — nothing was changed. Exhale now shows the person this change with a button to confirm it. Do not call this tool again for it; say in a sentence what you prepared.\n" + resultText(preview)}
}

// resultText renders a tool result for the model, bounded in size.
func resultText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	if len(b) > maxToolResult {
		return truncate(string(b), maxToolResult) + "\n[cut: the result is longer than fits. Ask for less: one subscription, a shorter period or a filter.]"
	}
	return string(b)
}

// mentions returns the subscriptions an answer talks about, so the web can
// link them: those a proposal is about, and those named in the text.
func (a *Assistant) mentions(ctx context.Context, agent app.Actor, turn Turn, st *turnState) []Mention {
	out := []Mention{}
	list, err := a.svc.ListSubscriptions(ctx, agent, app.ListSubscriptionsInput{})
	if err != nil {
		return out
	}
	text := strings.ToLower(turn.Text)
	touched := map[string]bool{}
	for _, id := range st.touched {
		touched[id] = true
	}
	seen := map[string]bool{}
	for _, s := range list.Subscriptions {
		name := strings.ToLower(strings.TrimSpace(s.Name))
		if seen[s.ID] || (!touched[s.ID] && (len(name) < 3 || !strings.Contains(text, name))) {
			continue
		}
		seen[s.ID] = true
		out = append(out, Mention{ID: s.ID, Name: s.Name})
		if len(out) == 8 {
			break
		}
	}
	return out
}

func sortStrings(s []string) { sort.Strings(s) }
