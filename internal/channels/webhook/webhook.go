// Package webhook sends each reminder as one HTTP POST with a JSON body to
// an address the member chooses. With a body template it speaks to Discord,
// Slack incoming webhooks, Gotify, Home Assistant and anything else that
// takes JSON. It is a notify-only channel.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/channels/notify"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/netguard"
)

// Name is the channel name in channel_links.
const Name = "webhook"

// MaxTemplate is the largest body template accepted, in bytes.
const MaxTemplate = 8 << 10

// Variables are the names a body template can use as {{name}}, in the
// order the docs list them.
var Variables = []string{
	"event", "title", "emoji", "message", "text", "subscription", "amount", "currency",
	"dueDate", "daysUntil", "paymentMethod", "category", "link",
}

// Payload is the default body.
type Payload struct {
	Event         string `json:"event"`
	Title         string `json:"title"`
	Message       string `json:"message"`
	Subscription  string `json:"subscription,omitempty"`
	Amount        string `json:"amount,omitempty"`
	Currency      string `json:"currency,omitempty"`
	DueDate       string `json:"dueDate,omitempty"`
	DaysUntil     *int   `json:"daysUntil,omitempty"`
	PaymentMethod string `json:"paymentMethod,omitempty"`
	Category      string `json:"category,omitempty"`
	Link          string `json:"link,omitempty"`
}

// Vars returns the value of every template variable for a message. link is
// "" when the instance has no public address.
func Vars(m actions.Message, link string) map[string]string {
	emoji, title := notify.SplitTitle(m.Title)
	v := map[string]string{
		"event": "message", "title": title, "emoji": emoji, "message": notify.Body(m),
		"text": notify.Text(m, link), "link": link, "daysUntil": "0",
		"subscription": "", "amount": "", "currency": "", "dueDate": "", "paymentMethod": "", "category": "",
	}
	if ev := m.Event; ev != nil {
		if ev.Kind != "" {
			v["event"] = ev.Kind
		}
		v["subscription"], v["amount"], v["currency"] = ev.Subscription, ev.Amount, ev.Currency
		v["dueDate"], v["daysUntil"] = ev.DueDate, strconv.Itoa(ev.DaysUntil)
		v["paymentMethod"], v["category"] = ev.PaymentMethod, ev.Category
	}
	return v
}

// DefaultBody is the JSON sent when the destination has no template.
func DefaultBody(m actions.Message, link string) ([]byte, error) {
	v := Vars(m, link)
	p := Payload{
		Event: v["event"], Title: v["title"], Message: v["message"], Subscription: v["subscription"],
		Amount: v["amount"], Currency: v["currency"], DueDate: v["dueDate"],
		PaymentMethod: v["paymentMethod"], Category: v["category"], Link: link,
	}
	if m.Event != nil && m.Event.DueDate != "" {
		p.DaysUntil = &m.Event.DaysUntil
	}
	return marshal(p)
}

func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)

// Render fills a body template. Each {{name}} becomes the variable's value
// escaped for a JSON string, so it can be written inside quotes
// ("content": "{{title}}") whatever the value contains; numbers such as
// {{daysUntil}} also work without quotes. The result must be valid JSON.
func Render(tmpl string, vars map[string]string) ([]byte, error) {
	var unknown []string
	out := placeholder.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := placeholder.FindStringSubmatch(match)[1]
		val, ok := vars[name]
		if !ok {
			unknown = append(unknown, name)
			return match
		}
		quoted, _ := marshal(val)
		return string(quoted[1 : len(quoted)-1])
	})
	if len(unknown) > 0 {
		return nil, fmt.Errorf("%w: unknown variable {{%s}}; the variables are %s", domain.ErrValidation, unknown[0], strings.Join(Variables, ", "))
	}
	if !json.Valid([]byte(out)) {
		return nil, fmt.Errorf("%w: the template is not valid JSON once filled in; write text variables inside quotes, like \"{{title}}\"", domain.ErrValidation)
	}
	return []byte(out), nil
}

// ValidateTemplate checks a template a member typed by filling it with a
// sample reminder. An empty template (the default body) is valid.
func ValidateTemplate(tmpl string) error {
	if strings.TrimSpace(tmpl) == "" {
		return nil
	}
	if len(tmpl) > MaxTemplate {
		return fmt.Errorf("%w: the template is too long (8 KB at most)", domain.ErrValidation)
	}
	_, err := Render(tmpl, Vars(notify.Test("2026-01-01"), "https://exhale.example.com/subscriptions/sub_x"))
	return err
}

// Channel is the webhook channel. Its chat id is the destination URL.
type Channel struct {
	notify.OneWay
	// Client must come from a netguard.Guard: any member can add a URL.
	Client *http.Client
	// Template returns the body template of a destination ("" for the
	// default body).
	Template func(ctx context.Context, url string) string
	// PublicURL is the instance's public address ("" if unknown).
	PublicURL string
	UserAgent string
}

func (c *Channel) Name() string { return Name }

// Send posts the message to a URL. Any answer outside 2xx is an error, so
// the outbox retries. Errors name the host only: the rest of a webhook URL
// is a secret.
func (c *Channel) Send(ctx context.Context, url string, m actions.Message) (string, error) {
	link := notify.Link(c.PublicURL, m)
	var body []byte
	var err error
	if tmpl := c.template(ctx, url); tmpl != "" {
		body, err = Render(tmpl, Vars(m, link))
	} else {
		body, err = DefaultBody(m, link)
	}
	if err != nil {
		return "", err
	}
	host := netguard.Host(url)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("the webhook address is not valid")
	}
	req.Header.Set("Content-Type", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	res, err := c.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach %s: %w", host, netguard.Cause(err))
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return "", nil
	case res.StatusCode >= 300 && res.StatusCode < 400:
		return "", fmt.Errorf("%s answered with a redirect (%d), which is not followed; use the final address", host, res.StatusCode)
	}
	if snippet := strings.Join(strings.Fields(string(raw)), " "); snippet != "" {
		if r := []rune(snippet); len(r) > 200 {
			snippet = string(r[:200]) + "…"
		}
		return "", fmt.Errorf("%s answered %d: %s", host, res.StatusCode, snippet)
	}
	return "", fmt.Errorf("%s answered %d", host, res.StatusCode)
}

func (c *Channel) template(ctx context.Context, url string) string {
	if c.Template == nil {
		return ""
	}
	return strings.TrimSpace(c.Template(ctx, url))
}
