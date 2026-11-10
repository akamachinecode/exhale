package actions

import (
	"context"
	"strings"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/assistant"
)

// Plain language in chat (ADR 0018, part B). Free text that is neither a
// command nor the answer to a pending question goes to the assistant; the
// changes it prepares arrive as messages with buttons, and nothing is
// applied until one is pressed. It is written against Message and Reply, so
// every channel gets it.
//
// Callback data: "ai|ok|<proposal>", "ai|no|<proposal>", "ai|undo|<proposal>".
// The proposal itself stays on the server; a button carries its short id.
const (
	aiPrefix = "ai"
	aiDo     = "ok"
	aiDont   = "no"
	aiUndo   = "undo"
)

// Assistant is the model behind free text. *assistant.Assistant implements
// it; an Engine without one answers free text with the channel's help.
type Assistant interface {
	Enabled(ctx context.Context) bool
	Ask(ctx context.Context, actor app.Actor, conv, text string) (assistant.Turn, error)
	Decide(ctx context.Context, actor app.Actor, id string, accept bool) (assistant.Outcome, error)
	Undo(ctx context.Context, actor app.Actor, id string) (assistant.Outcome, error)
}

// SetAssistant wires the assistant. Without it free text gets the help.
func (e *Engine) SetAssistant(a Assistant) { e.assistant = a }

// CanAsk reports whether free text will be answered by the assistant: it
// is wired, configured and switched on.
func (e *Engine) CanAsk(ctx context.Context) bool {
	return e.assistant != nil && e.assistant.Enabled(ctx)
}

// Ask answers free text typed in a chat. It returns the messages to send:
// the answer, then one message per prepared change with its buttons. ok is
// false when the assistant is off; the channel then shows its help.
func (e *Engine) Ask(ctx context.Context, actor app.Actor, chat ChatRef, text string) (msgs []Message, ok bool) {
	if !e.CanAsk(ctx) {
		return nil, false
	}
	turn, err := e.assistant.Ask(ctx, actor, chat.Channel+"_"+chat.ChatID, text)
	if err != nil {
		return []Message{{Lines: []string{"⚠️ " + assistant.Explain(err)}}}, true
	}
	if turn.Text != "" || turn.Note != "" {
		m := Message{Footer: turn.Note}
		if turn.Text != "" {
			m.Lines = strings.Split(turn.Text, "\n")
		}
		msgs = append(msgs, m)
	}
	for _, p := range turn.Proposals {
		msgs = append(msgs, proposalMessage(p, e.Locale(ctx, actor)))
	}
	return msgs, true
}

func proposalMessage(p assistant.Proposal, locale string) Message {
	title := p.Title + "?"
	if p.Destructive {
		title = "⚠️ " + title
	}
	return Message{
		Title: title, Lines: p.Lines,
		Rows: [][]Button{{{Label: tr(locale, "✅ Do it"), Data: data(aiPrefix, aiDo, p.ID)}, {Label: tr(locale, "✖ Don't"), Data: data(aiPrefix, aiDont, p.ID)}}},
	}
}

// handleAssistant runs the buttons of a prepared change: apply it, drop it,
// or undo it afterwards. The message is edited in place with the result.
func (e *Engine) handleAssistant(ctx context.Context, actor app.Actor, raw string) (Reply, error) {
	locale := e.Locale(ctx, actor)
	p := strings.Split(raw, "|")
	if len(p) != 3 || e.assistant == nil {
		return Reply{Toast: tr(locale, "This button has expired.")}, nil
	}
	var out assistant.Outcome
	var err error
	switch p[1] {
	case aiDo:
		out, err = e.assistant.Decide(ctx, actor, p[2], true)
	case aiDont:
		out, err = e.assistant.Decide(ctx, actor, p[2], false)
	case aiUndo:
		out, err = e.assistant.Undo(ctx, actor, p[2])
	default:
		return Reply{Toast: tr(locale, "Unknown action.")}, nil
	}
	if err != nil {
		return Reply{Toast: assistant.Explain(err)}, nil
	}
	m := Message{Lines: out.Lines}
	toast := ""
	switch out.Status {
	case assistant.StatusApplied:
		m.Title, toast = tr(locale, "✅ Done: %s", out.Title), tr(locale, "Done ✅")
		if out.CanUndo {
			m.Rows = [][]Button{{{Label: tr(locale, "↩︎ Undo"), Data: data(aiPrefix, aiUndo, out.ID)}}}
		}
	case assistant.StatusDiscarded:
		m.Title, toast = tr(locale, "✖ Not done: %s", out.Title), tr(locale, "Nothing was changed")
	case assistant.StatusUndone:
		m.Title, toast = tr(locale, "↩︎ Undone: %s", out.Title), tr(locale, "Undone ↩︎")
	case assistant.StatusFailed:
		m.Title, toast = tr(locale, "⚠️ Could not do it: %s", out.Title), tr(locale, "It could not be done")
	default:
		return Reply{Edit: new(proposalMessage(assistant.Proposal{ID: out.ID, Title: out.Title, Lines: out.Lines}, locale))}, nil
	}
	return Reply{Edit: &m, Toast: toast}, nil
}
