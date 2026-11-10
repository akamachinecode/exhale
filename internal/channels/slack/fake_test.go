package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/store"
)

// fakeSlack is a minimal Slack: the Web API methods the adapter calls, a
// response_url endpoint and a Socket Mode WebSocket that plays envelopes
// and records their acknowledgements. Nothing here talks to real Slack.
type fakeSlack struct {
	srv *httptest.Server

	mu      sync.Mutex
	calls   []call
	acks    map[string]bool
	conn    *websocket.Conn
	opens   []string          // app-level tokens that opened a socket, in order
	sockets int               // sockets accepted
	reject  map[string]bool   // tokens Slack does not recognize
	msgs    map[string]string // "channel|ts" → blocks JSON, kept current by chat.update
	order   []string          // message keys in the order they were posted
	homes   map[string]string // user → blocks JSON of the published Home tab
	seq     int
	wmu     sync.Mutex // one writer on the socket at a time
}

type call struct {
	Method string
	Token  string
	P      map[string]string
}

func newFakeSlack(t *testing.T) *fakeSlack {
	f := &fakeSlack{acks: map[string]bool{}, reject: map[string]bool{}, msgs: map[string]string{}, homes: map[string]string{}}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSlack) apiURL() string { return f.srv.URL + "/api/" }

func (f *fakeSlack) next() int {
	f.seq++
	return f.seq
}

func (f *fakeSlack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/socket" {
		f.socket(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	p := map[string]string{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		for k, v := range m {
			if s, ok := v.(string); ok {
				p[k] = s
			} else {
				b, _ := json.Marshal(v)
				p[k] = string(b)
			}
		}
	} else {
		r.Body = io.NopCloser(bytes.NewReader(body))
		_ = r.ParseForm()
		for k, v := range r.PostForm {
			p[k] = v[0]
		}
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = p["token"]
	}
	delete(p, "token")
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if strings.HasPrefix(r.URL.Path, "/respond/") {
		method = "response_url"
	}
	w.Header().Set("Content-Type", "application/json")
	reply := func(v map[string]any) {
		v["ok"] = true
		_ = json.NewEncoder(w).Encode(v)
	}
	fail := func(code string) { _ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code}) }

	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{method, token, p})
	if method != "response_url" && f.reject[token] {
		fail("invalid_auth")
		return
	}
	switch method {
	case "auth.test":
		reply(map[string]any{"url": "https://acme.slack.com/", "team": "Acme", "user": "exhale", "team_id": "T1", "user_id": "UBOT", "bot_id": "B1"})
	case "apps.connections.open":
		if !strings.HasPrefix(token, "xapp-") {
			fail("not_allowed_token_type")
			return
		}
		f.opens = append(f.opens, token)
		reply(map[string]any{"url": "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/socket"})
	case "conversations.open":
		reply(map[string]any{"channel": map[string]any{"id": "D" + strings.TrimPrefix(p["users"], "U")}})
	case "chat.postMessage":
		ch := p["channel"]
		switch {
		case ch == "#subscriptions":
			ch = "C777"
		case strings.HasPrefix(ch, "#"):
			fail("channel_not_found")
			return
		}
		ts := fmt.Sprintf("1762333200.%06d", f.next())
		f.msgs[ch+"|"+ts] = p["blocks"]
		f.order = append(f.order, ch+"|"+ts)
		reply(map[string]any{"channel": ch, "ts": ts})
	case "chat.update":
		key := p["channel"] + "|" + p["ts"]
		if _, ok := f.msgs[key]; !ok {
			fail("message_not_found")
			return
		}
		f.msgs[key] = p["blocks"]
		reply(map[string]any{"channel": p["channel"], "ts": p["ts"], "text": p["text"]})
	case "chat.postEphemeral":
		reply(map[string]any{"message_ts": fmt.Sprintf("1762333200.%06d", f.next())})
	case "views.publish":
		var v struct {
			Blocks json.RawMessage `json:"blocks"`
		}
		_ = json.Unmarshal([]byte(p["view"]), &v)
		f.homes[p["user_id"]] = string(v.Blocks)
		reply(map[string]any{"view": map[string]any{"id": "V1", "type": "home"}})
	case "users.info":
		reply(map[string]any{"user": map[string]any{"id": p["user"], "name": "marta", "real_name": "Marta G"}})
	default: // pins.add, pins.remove, response_url
		reply(map[string]any{})
	}
}

func (f *fakeSlack) socket(w http.ResponseWriter, r *http.Request) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conn = c
	f.sockets++
	f.mu.Unlock()
	f.wmu.Lock()
	_ = c.WriteJSON(map[string]any{"type": "hello", "num_connections": 1, "connection_info": map[string]any{"app_id": "A1"}})
	f.wmu.Unlock()
	for {
		var ack struct {
			EnvelopeID string `json:"envelope_id"`
		}
		if err := c.ReadJSON(&ack); err != nil {
			return
		}
		f.mu.Lock()
		f.acks[ack.EnvelopeID] = true
		f.mu.Unlock()
	}
}

// envelope plays a Socket Mode envelope on the current socket and returns
// its id.
func (f *fakeSlack) envelope(t *testing.T, kind string, payload any) string {
	t.Helper()
	var c *websocket.Conn
	eventually(t, "a socket", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		c = f.conn
		return c != nil
	})
	f.mu.Lock()
	id := fmt.Sprintf("env-%d", f.next())
	f.mu.Unlock()
	f.wmu.Lock()
	defer f.wmu.Unlock()
	if err := c.WriteJSON(map[string]any{"envelope_id": id, "type": kind, "payload": payload, "accepts_response_payload": kind != "events_api"}); err != nil {
		t.Fatalf("writing envelope: %v", err)
	}
	return id
}

// drop cuts the socket, as a network failure would.
func (f *fakeSlack) drop() {
	f.mu.Lock()
	c := f.conn
	f.conn = nil
	f.mu.Unlock()
	if c != nil {
		_ = c.UnderlyingConn().Close()
	}
}

func dmOf(user string) string { return "D" + strings.TrimPrefix(user, "U") }

// say plays a message typed in the DM with the bot.
func (f *fakeSlack) say(t *testing.T, user, text string) string {
	t.Helper()
	return f.envelope(t, "events_api", map[string]any{"type": "event_callback", "team_id": "T1", "api_app_id": "A1", "event": map[string]any{
		"type": "message", "channel_type": "im", "user": user, "text": text, "channel": dmOf(user), "ts": "1762333100.000100", "event_ts": "1762333100.000100"}})
}

// press plays a button press on a message.
func (f *fakeSlack) press(t *testing.T, user, channel, ts, value string) string {
	t.Helper()
	return f.envelope(t, "interactive", map[string]any{
		"type": "block_actions", "user": map[string]any{"id": user, "username": "someone"}, "api_app_id": "A1",
		"container": map[string]any{"type": "message", "message_ts": ts, "channel_id": channel},
		"channel":   map[string]any{"id": channel}, "message": map[string]any{"ts": ts},
		"response_url": f.srv.URL + "/respond/" + ts,
		"actions":      []map[string]any{{"action_id": actionPrefix + "0:0", "block_id": "row0", "type": "button", "value": value}},
	})
}

// pressHome plays a button press in the Home tab.
func (f *fakeSlack) pressHome(t *testing.T, user, value string) string {
	t.Helper()
	return f.envelope(t, "interactive", map[string]any{
		"type": "block_actions", "user": map[string]any{"id": user}, "api_app_id": "A1",
		"container": map[string]any{"type": "view", "view_id": "V1"}, "view": map[string]any{"id": "V1", "type": "home"},
		"actions": []map[string]any{{"action_id": actionPrefix + "0:0", "block_id": "row0", "type": "button", "value": value}},
	})
}

func (f *fakeSlack) slash(t *testing.T, user, channel, text string) string {
	t.Helper()
	return f.envelope(t, "slash_commands", map[string]any{"command": "/exhale", "text": text, "user_id": user, "user_name": "someone",
		"channel_id": channel, "team_id": "T1", "response_url": f.srv.URL + "/respond/slash"})
}

func (f *fakeSlack) openHome(t *testing.T, user string) string {
	t.Helper()
	return f.envelope(t, "events_api", map[string]any{"type": "event_callback", "team_id": "T1", "api_app_id": "A1", "event": map[string]any{
		"type": "app_home_opened", "user": user, "channel": dmOf(user), "tab": "home", "event_ts": "1762333100.000200"}})
}

func (f *fakeSlack) acked(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acks[id]
}

// find reports whether a call to method carried text in any parameter.
func (f *fakeSlack) find(method, text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.Method != method {
			continue
		}
		for _, v := range c.P {
			if strings.Contains(v, text) {
				return true
			}
		}
	}
	return false
}

func (f *fakeSlack) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

// last returns the newest message in a conversation: its ts and blocks.
func (f *fakeSlack) last(channel string) (ts, blocks string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.order) - 1; i >= 0; i-- {
		if ch, ts, _ := strings.Cut(f.order[i], "|"); ch == channel {
			return ts, f.msgs[f.order[i]]
		}
	}
	return "", ""
}

func (f *fakeSlack) message(channel, ts string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.msgs[channel+"|"+ts]
}

func (f *fakeSlack) home(user string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.homes[user]
}

// buttonValue returns the value of the first button whose label contains
// label in a blocks JSON, or "".
func buttonValue(blocks, label string) string {
	var bs []struct {
		Elements []struct {
			Text  struct{ Text string } `json:"text"`
			Value string                `json:"value"`
		} `json:"elements"`
	}
	_ = json.Unmarshal([]byte(blocks), &bs)
	for _, b := range bs {
		for _, e := range b.Elements {
			if strings.Contains(e.Text.Text, label) && e.Value != "" {
				return e.Value
			}
		}
	}
	return ""
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// world is an Exhale with a household, wired to a fake Slack.
type world struct {
	ctx   context.Context
	api   *fakeSlack
	st    *store.Store
	svc   *app.Service
	owner app.Actor
	eng   *actions.Engine
	log   *slog.Logger
}

const (
	testBot = "xoxb-test-bot-token-1111"
	testApp = "xapp-1-A1-test-app-token-2222"
)

func newWorld(t *testing.T) *world {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Acme", OwnerName: "Alex", Currency: "EUR", Timezone: "UTC", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	w := &world{ctx: ctx, api: newFakeSlack(t), st: st, svc: svc, owner: owner,
		log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	t.Cleanup(func() {
		// Tokens must never reach a log, whatever happened in the test.
		cancel()
		time.Sleep(20 * time.Millisecond)
		logs.mu.Lock()
		defer logs.mu.Unlock()
		for _, secret := range []string{testBot, testApp, "xoxb-", "xapp-"} {
			if strings.Contains(logs.b.String(), secret) {
				t.Errorf("a token reached the log (%q)", secret)
			}
		}
	})
	return w
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// adapter starts an adapter with the test tokens and waits for its socket.
func (w *world) adapter(t *testing.T) *Adapter {
	t.Helper()
	a := New(testBot, testApp, w.api.apiURL(), w.svc, w.log)
	w.eng = actions.New(w.svc, w.log, a)
	a.SetEngine(w.eng)
	go a.Serve(w.ctx)
	eventually(t, "the socket", a.Connected)
	return a
}

// link links a Slack user to a member the way a person does: a code from
// the web, sent to the bot.
func (w *world) link(t *testing.T, user string, member app.Actor) {
	t.Helper()
	code, err := auth.CreateLinkCode(w.ctx, w.st, member.MemberID, Name)
	if err != nil {
		t.Fatal(err)
	}
	w.api.say(t, user, code)
	eventually(t, "the link", func() bool {
		_, err := auth.MemberForChannel(w.ctx, w.st, Name, user)
		return err == nil
	})
	eventually(t, "the app message after linking", func() bool { return w.api.count("pins.add") > 0 })
}

func (w *world) netflix(t *testing.T, private bool) (subID, chargeID string) {
	t.Helper()
	d, err := w.svc.CreateSubscription(w.ctx, w.owner, app.CreateSubscriptionInput{Name: "Netflix", Price: "19.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-08", Private: private})
	if err != nil {
		t.Fatal(err)
	}
	return d.Subscription.ID, d.Upcoming[0].ID
}
