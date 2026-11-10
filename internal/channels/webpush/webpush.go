// Package webpush sends reminders as Web Push notifications to the browsers
// and installed apps (PWA) where a member turned them on (ADR 0026).
//
// Each device is one destination: a channel link whose external id is the
// push endpoint, next to the browser's keys. Messages are encrypted for the
// browser (RFC 8291) and signed with the instance's VAPID key (RFC 8292),
// both written on the standard library.
//
// A notification can carry up to two action buttons taken from the
// message's own buttons (actions.PushButtons). Each carries a signed token
// for that device, charge and verb, so the service worker can run it with
// no tab open and no session.
package webpush

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/channels/notify"
	"github.com/akamachinecode/exhale/internal/netguard"
)

// Name is the channel name in channel_links.
const Name = "webpush"

// TTL is how long a push service keeps a message for a device that is
// offline.
const TTL = 72 * time.Hour

// Device is one browser subscription.
type Device struct {
	LinkID   string
	MemberID string
	Keys     Keys
}

// ErrNoDevice is returned by a Lookup that does not know the endpoint.
var ErrNoDevice = errors.New("no such device")

// Channel is the Web Push channel. Its chat id is the push endpoint.
type Channel struct {
	notify.OneWay
	// Lookup returns the device of an endpoint.
	Lookup func(ctx context.Context, endpoint string) (Device, error)
	// Gone forgets a device the push service says no longer exists.
	Gone func(ctx context.Context, endpoint string)
	// Keys returns the instance's VAPID key pair and the key that signs
	// action tokens.
	Keys func(ctx context.Context) (VAPID, []byte, error)
	// Subject is the contact in the VAPID claims (mailto: or https:).
	Subject string
	// Client must come from a netguard.Guard: endpoints are sent by
	// browsers, so anyone signed in can choose one.
	Client *http.Client
	// Now defaults to time.Now.
	Now func() time.Time
}

func (c *Channel) Name() string { return Name }

func (c *Channel) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// ActionButton is a button of a notification.
type ActionButton struct {
	Action string `json:"action"`
	Title  string `json:"title"`
	Token  string `json:"token"`
}

// Payload is what the service worker receives (web/public/sw.js).
type Payload struct {
	Title   string         `json:"title"`
	Body    string         `json:"body,omitempty"`
	Tag     string         `json:"tag,omitempty"`
	URL     string         `json:"url"`
	Actions []ActionButton `json:"actions,omitempty"`
}

// BuildPayload turns a message into the notification for one device.
func BuildPayload(m actions.Message, dev Device, key []byte, now time.Time) Payload {
	p := Payload{Title: strings.TrimSpace(m.Title), Body: notify.Body(m), URL: "/", Tag: "exhale"}
	if p.Title == "" {
		p.Title = "Exhale"
	}
	if ev := m.Event; ev != nil {
		if strings.HasPrefix(ev.Path, "/") {
			p.URL = ev.Path
		}
		if ev.ChargeID != "" {
			p.Tag = ev.ChargeID // a newer message about the charge replaces the older one
		}
	}
	for _, b := range actions.PushButtons(m) {
		verb, chargeID := actions.ButtonVerb(b.Data)
		tok := SignAction(key, Action{LinkID: dev.LinkID, MemberID: dev.MemberID, ChargeID: chargeID, Verb: verb, Expires: now.Add(ActionTTL)})
		p.Actions = append(p.Actions, ActionButton{Action: verb, Title: b.Label, Token: tok})
	}
	return p
}

// encode marshals the payload, shortening the body until it fits in one
// push message.
func (p Payload) encode() ([]byte, error) {
	for {
		raw, err := json.Marshal(p)
		if err != nil || len(raw) <= MaxPlaintext {
			return raw, err
		}
		if p.Body == "" {
			return nil, ErrTooLarge
		}
		cut := max(0, len(p.Body)-(len(raw)-MaxPlaintext)-8)
		for cut > 0 && !utf8.RuneStart(p.Body[cut]) {
			cut--
		}
		p.Body = strings.TrimRight(p.Body[:cut], " \n") + "…"
		if cut == 0 {
			p.Body = ""
		}
	}
}

// Send encrypts the message for the device and posts it to its push
// service. A device the push service no longer knows (404, 410) is removed
// and reported as actions.ErrGone, which is not retried.
func (c *Channel) Send(ctx context.Context, endpoint string, m actions.Message) (string, error) {
	dev, err := c.Lookup(ctx, endpoint)
	if errors.Is(err, ErrNoDevice) {
		return "", actions.ErrGone
	}
	if err != nil {
		return "", err
	}
	vapid, key, err := c.Keys(ctx)
	if err != nil {
		return "", err
	}
	urgency := "normal"
	if m.Event != nil && m.Event.Kind == actions.EventFailed {
		urgency = "high"
	}
	err = c.Push(ctx, endpoint, dev.Keys, vapid, BuildPayload(m, dev, key, c.now()), urgency)
	if errors.Is(err, actions.ErrGone) && c.Gone != nil {
		c.Gone(ctx, endpoint)
	}
	return "", err
}

// Push delivers one payload to an endpoint.
func (c *Channel) Push(ctx context.Context, endpoint string, keys Keys, vapid VAPID, p Payload, urgency string) error {
	plain, err := p.encode()
	if err != nil {
		return err
	}
	body, err := Encrypt(plain, keys)
	if err != nil {
		return fmt.Errorf("could not encrypt for this device: %w", err)
	}
	auth, err := vapid.Header(endpoint, c.Subject, c.now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("the push endpoint is not a valid address")
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(int(TTL/time.Second)))
	req.Header.Set("Urgency", urgency)
	host := netguard.Host(endpoint)
	res, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s: %w", host, netguard.Cause(err))
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return nil
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		return fmt.Errorf("%w: %s no longer knows this device (%d)", actions.ErrGone, host, res.StatusCode)
	case res.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%s is rate limiting (429); will retry", host)
	}
	detail := strings.Join(strings.Fields(string(raw)), " ")
	if utf8.RuneCountInString(detail) > 160 {
		detail = string([]rune(detail)[:160]) + "…"
	}
	if detail != "" {
		return fmt.Errorf("%s answered %d: %s", host, res.StatusCode, detail)
	}
	return fmt.Errorf("%s answered %d", host, res.StatusCode)
}
