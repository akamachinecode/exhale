// Package fake is a text channel for tests: it renders messages as plain
// text so conversation scripts can be compared with golden files. Having it
// from day one keeps the action layer channel-agnostic (ADR 0007).
package fake

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/akamachinecode/exhale/internal/actions"
)

// Channel records messages per chat.
type Channel struct {
	mu   sync.Mutex
	next int
	// Messages maps chat → message id → latest rendering.
	Messages map[string]map[string]actions.Message
	// Log is every send and edit in order.
	Log []string
}

// New returns an empty fake channel.
func New() *Channel { return &Channel{Messages: map[string]map[string]actions.Message{}} }

func (c *Channel) Name() string { return "fake" }

func (c *Channel) Send(_ context.Context, chatID string, m actions.Message) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	id := fmt.Sprint(c.next)
	if c.Messages[chatID] == nil {
		c.Messages[chatID] = map[string]actions.Message{}
	}
	c.Messages[chatID][id] = m
	c.Log = append(c.Log, "SEND #"+id+"\n"+Render(m))
	return id, nil
}

func (c *Channel) Edit(_ context.Context, chatID, messageID string, m actions.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Messages[chatID] == nil {
		return fmt.Errorf("no such chat %s", chatID)
	}
	c.Messages[chatID][messageID] = m
	c.Log = append(c.Log, "EDIT #"+messageID+"\n"+Render(m))
	return nil
}

// Render formats a message as text: title, lines, sections (## heading,
// | table rows), then buttons as [Label](data) rows.
func Render(m actions.Message) string {
	var b strings.Builder
	b.WriteString(m.Title + "\n")
	for _, l := range m.Lines {
		b.WriteString("  " + l + "\n")
	}
	for _, s := range m.Sections {
		b.WriteString("  ## " + s.Heading + "\n")
		for _, l := range s.Lines {
			b.WriteString("  " + l + "\n")
		}
		if len(s.Table) > 0 {
			for _, l := range strings.Split(s.TableText(), "\n") {
				b.WriteString("  | " + l + "\n")
			}
		}
	}
	for _, row := range m.Rows {
		parts := make([]string, len(row))
		for i, btn := range row {
			parts[i] = fmt.Sprintf("[%s](%s)", btn.Label, btn.Data)
		}
		b.WriteString("  " + strings.Join(parts, " ") + "\n")
	}
	if m.Footer != "" {
		b.WriteString("  — " + m.Footer + "\n")
	}
	return b.String()
}

// Button returns the data of the first button whose label contains label.
func Button(m actions.Message, label string) string {
	for _, row := range m.Rows {
		for _, btn := range row {
			if strings.Contains(btn.Label, label) {
				return btn.Data
			}
		}
	}
	return ""
}

// Count returns how many sends and edits happened.
func (c *Channel) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.Log)
}

// Last returns the latest send or edit.
func (c *Channel) Last() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.Log) == 0 {
		return ""
	}
	return c.Log[len(c.Log)-1]
}
