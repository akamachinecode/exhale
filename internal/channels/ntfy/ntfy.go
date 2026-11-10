// Package ntfy sends reminders as push notifications through ntfy
// (https://ntfy.sh or a self-hosted server). It is a notify-only channel:
// the notification opens the subscription in the web when tapped.
package ntfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/channels/notify"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/netguard"
)

// Name is the channel name in channel_links.
const Name = "ntfy"

// DefaultServer is the public ntfy service.
const DefaultServer = "https://ntfy.sh"

// Config is the ntfy server every member's topic lives on.
type Config struct {
	Server string `json:"server"`
	// Token is an ntfy access token (tk_…) for servers that need one to
	// publish. Optional.
	Token string `json:"token,omitempty"`
}

var topicRE = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

// Topic validates a topic a member typed. A pasted address such as
// https://ntfy.sh/my-topic is accepted too: its last part is the topic.
func Topic(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if i := strings.LastIndex(raw, "/"); i >= 0 {
		raw = raw[i+1:]
	}
	if !topicRE.MatchString(raw) {
		return "", fmt.Errorf("%w: a topic is 1 to 64 letters, numbers, - or _", domain.ErrValidation)
	}
	return raw, nil
}

type viewAction struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	URL    string `json:"url"`
}

// Notification is the body of ntfy's JSON publish API.
type Notification struct {
	Topic    string       `json:"topic"`
	Title    string       `json:"title,omitempty"`
	Message  string       `json:"message,omitempty"`
	Tags     []string     `json:"tags,omitempty"`
	Priority int          `json:"priority,omitempty"`
	Click    string       `json:"click,omitempty"`
	Actions  []viewAction `json:"actions,omitempty"`
}

// tags maps the emoji a title starts with to ntfy's tag for it, which ntfy
// shows as that emoji before the title. Titles with any other emoji keep it
// in the text.
var tags = map[string]string{
	"🔔":  "bell",
	"❓":  "question",
	"⚠️": "warning",
	"✅":  "white_check_mark",
	"❌":  "x",
	"💳":  "credit_card",
}

// FromMessage turns a message into a notification for a topic. link ("" for
// none) is opened when the notification is tapped and by its one button.
func FromMessage(topic string, m actions.Message, link string) Notification {
	n := Notification{Topic: topic, Title: strings.TrimSpace(m.Title), Message: notify.Body(m), Priority: 3}
	if emoji, text := notify.SplitTitle(m.Title); tags[emoji] != "" && text != "" {
		n.Title, n.Tags = text, []string{tags[emoji]}
	}
	// Something went wrong: make it stand out.
	failed := m.Event != nil && m.Event.Kind == actions.EventFailed
	if failed || slices.Contains(n.Tags, "warning") {
		n.Priority = 4
	}
	if link != "" {
		n.Click = link
		n.Actions = []viewAction{{Action: "view", Label: notify.OpenLabel, URL: link}}
	}
	return n
}

// Publish posts a notification to the server.
func Publish(ctx context.Context, client *http.Client, cfg Config, n Notification) error {
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}
	host := netguard.Host(cfg.Server)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Server, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("the ntfy server address is not valid")
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s: %w", host, netguard.Cause(err))
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	var problem struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &problem)
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		if cfg.Token != "" {
			return fmt.Errorf("%s refused the access token for this topic (%d)", host, res.StatusCode)
		}
		return fmt.Errorf("%s needs an access token to publish to this topic (%d)", host, res.StatusCode)
	case res.StatusCode >= 300 && res.StatusCode < 400:
		return fmt.Errorf("%s answered with a redirect (%d); use the server's final address", host, res.StatusCode)
	case problem.Error != "":
		return fmt.Errorf("%s answered %d: %s", host, res.StatusCode, problem.Error)
	}
	return fmt.Errorf("%s answered %d; is it an ntfy server?", host, res.StatusCode)
}

// Channel is the ntfy channel. Its chat id is the member's topic.
type Channel struct {
	notify.OneWay
	// Config returns the current server settings, read for every message.
	Config func(ctx context.Context) Config
	// Client must come from a netguard.Guard: the server address is typed
	// by a person.
	Client *http.Client
	// PublicURL is the instance's public address ("" if unknown).
	PublicURL string
}

func (c *Channel) Name() string { return Name }

// Send publishes the message to a topic.
func (c *Channel) Send(ctx context.Context, topic string, m actions.Message) (string, error) {
	return "", Publish(ctx, c.Client, c.Config(ctx), FromMessage(topic, m, notify.Link(c.PublicURL, m)))
}
