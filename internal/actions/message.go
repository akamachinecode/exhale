// Package actions (piece ⑥) decides what every reminder says and which
// buttons it carries, and runs the multi-step flows behind those buttons.
// It knows nothing about Telegram or Slack: channel adapters only render
// Message values and pass button data back (ADR 0003).
package actions

import "context"

// Button is an action attached to a message. Data is opaque to channels and
// at most 64 bytes, Telegram's limit for callback data.
type Button struct {
	Label string
	Data  string
}

// Message is a channel-agnostic message: a title, detail lines and rows of
// buttons.
type Message struct {
	Title string
	Lines []string
	Rows  [][]Button
	// Footer is shown in a muted style (e.g. "Moved by Claude · Undo").
	Footer string
}

// Channel delivers messages to one chat platform.
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
