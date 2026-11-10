// Package actions (piece ⑥) decides what every reminder says and which
// buttons it carries, and runs the multi-step flows behind those buttons.
// It knows nothing about Telegram or Slack: channel adapters only render
// Message values and pass button data back (ADR 0003).
package actions

import (
	"context"
	"strings"
	"unicode/utf8"
)

// Button is an action attached to a message. Data is opaque to channels and
// at most 64 bytes, Telegram's limit for callback data. A button with a URL
// opens that page instead of running an action.
type Button struct {
	Label string
	Data  string
	URL   string
}

// Message is a channel-agnostic screen: a title, detail lines, sections and
// rows of buttons. Each channel renders it at the best fidelity it offers
// (ADR 0019).
type Message struct {
	Title    string
	Lines    []string
	Sections []Section
	Rows     [][]Button
	// Footer is shown in a muted style (e.g. "Moved by Claude · Undo").
	Footer string
	// Event is the data behind the message, for channels that only notify
	// (email, ntfy, webhooks). Nil for screens that are not about a charge.
	Event *Event
}

// Section is a titled block of a screen: a heading followed by lines and an
// optional table.
type Section struct {
	Heading string
	Lines   []string
	// Table rows are cells; channels without tables show TableText in a
	// monospace block. Cells should not contain emoji (they break alignment).
	Table [][]string
}

// TableText aligns the table in columns: the first column left-aligned, the
// others right-aligned (amounts, counts).
func (s Section) TableText() string {
	widths := []int{}
	for _, row := range s.Table {
		for i, cell := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}
	lines := make([]string, len(s.Table))
	for r, row := range s.Table {
		cells := make([]string, len(row))
		for i, cell := range row {
			pad := strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell))
			if i == 0 {
				cells[i] = cell + pad
			} else {
				cells[i] = pad + cell
			}
		}
		lines[r] = strings.TrimRight(strings.Join(cells, "  "), " ")
	}
	return strings.Join(lines, "\n")
}

// Channel delivers messages to one chat platform. Channels that only notify
// and cannot edit what they sent (email, ntfy, webhooks) return an empty
// message id from Send; their messages are not tracked or re-edited.
type Channel interface {
	Name() string
	Send(ctx context.Context, chatID string, m Message) (messageID string, err error)
	Edit(ctx context.Context, chatID, messageID string, m Message) error
}

// Reply is what the user sees after pressing a button or typing.
type Reply struct {
	// Edit replaces the message the button belongs to.
	Edit *Message
	// Toast is a short notification (Telegram callback answer).
	Toast string
	// Send is a new message (e.g. a question that expects a typed answer).
	Send *Message
}
