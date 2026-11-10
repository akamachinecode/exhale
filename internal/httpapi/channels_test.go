package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/email/smtptest"
	"github.com/akamachinecode/exhale/internal/channels/notifiers"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/testutil"
)

// sink records the bodies posted to a fake ntfy server or webhook.
type sink struct {
	mu     sync.Mutex
	bodies []string
}

func (s *sink) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.bodies = append(s.bodies, string(raw))
	s.mu.Unlock()
}

func (s *sink) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

type channelsWorld struct {
	url   string
	smtp  *smtptest.Server
	ntfy  *sink
	hook  *sink
	owner *browser
	guest *browser
}

// channelsServer is an Exhale with the notify-only channels, a fake mail
// server, a fake ntfy server (http://ntfy.test) and a webhook receiver
// (http://hooks.test), an owner (Alex) and a member (Marta).
func channelsServer(t *testing.T) *channelsWorld {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w := &channelsWorld{ntfy: &sink{}, hook: &sink{}}
	w.smtp = (&smtptest.Server{Username: "mailer", Password: "s3cret-pass"}).Start(t)
	ntfySrv, hookSrv := httptest.NewServer(w.ntfy), httptest.NewServer(w.hook)
	t.Cleanup(ntfySrv.Close)
	t.Cleanup(hookSrv.Close)
	net := testutil.NewInternet().
		Host("ntfy.test", ntfySrv.Listener.Addr().String()).
		Host("hooks.test", hookSrv.Listener.Addr().String()).
		PrivateHost("nas.lan", "192.168.1.20")
	channels, err := notifiers.New(st, notifiers.Options{PublicURL: "https://exhale.example.com", Version: "test", Guard: net.Guard(nil)}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	svc := app.New(st, bus.New(), nil, func() time.Time { return time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC) })
	h, _, err := New(svc, Config{Version: "test", Telegram: fakeTelegram{}, Channels: channels})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	w.url = srv.URL

	w.owner = newBrowser(t, srv.URL)
	if code, s := w.owner.do("POST", "/api/v1/auth/setup", alex); code != 200 {
		t.Fatalf("setup: %d %v", code, s)
	}
	_, inv := w.owner.do("POST", "/api/v1/auth/invites", map[string]string{"role": "member"})
	w.guest = newBrowser(t, srv.URL)
	join := map[string]string{"code": inv["code"].(string), "name": "Marta", "email": "marta@example.com", "password": "another long password"}
	if code, s := w.guest.do("POST", "/api/v1/auth/join", join); code != 200 {
		t.Fatalf("join: %d %v", code, s)
	}
	return w
}

func (w *channelsWorld) smtpBody() map[string]any {
	return map[string]any{"host": w.smtp.Host, "port": w.smtp.Port, "username": "mailer", "password": "s3cret-pass", "from": "reminders@example.com", "tls": "none"}
}

func text(v any) string { b, _ := json.Marshal(v); return string(b) }

// TestChannelSettings covers the instance setup: only owners change it,
// wrong settings are refused with a reason, and secrets never come back.
func TestChannelSettings(t *testing.T) {
	w := channelsServer(t)
	anon := newBrowser(t, w.url)

	// Signed out: nothing. Members: read only.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/settings/channels", nil},
		{"POST", "/api/v1/settings/channels", map[string]any{"allowPrivate": true}},
		{"POST", "/api/v1/settings/smtp", w.smtpBody()},
		{"POST", "/api/v1/settings/smtp/remove", nil},
		{"POST", "/api/v1/settings/smtp/test", nil},
		{"POST", "/api/v1/settings/ntfy", map[string]any{"server": "http://ntfy.test", "token": "tk_secret"}},
		{"POST", "/api/v1/settings/ntfy/remove", nil},
	} {
		if code, _ := anon.do(c.method, c.path, c.body); code != 401 {
			t.Errorf("signed out: %s %s = %d, want 401", c.method, c.path, code)
		}
		want := 403
		if c.method == "GET" {
			want = 200
		}
		if code, out := w.guest.do(c.method, c.path, c.body); code != want {
			t.Errorf("member: %s %s = %d %v, want %d", c.method, c.path, code, out, want)
		}
	}
	if len(w.smtp.Messages()) != 0 {
		t.Fatal("a member's requests reached the mail server")
	}

	// The owner sets up email. A wrong password is refused with the reason.
	bad := w.smtpBody()
	bad["password"] = "wrong-pass"
	code, out := w.owner.do("POST", "/api/v1/settings/smtp", bad)
	if code != 422 || !strings.Contains(text(out), "rejected the username or password") || strings.Contains(text(out), "wrong-pass") {
		t.Fatalf("wrong password: %d %v", code, out)
	}
	if code, out := w.owner.do("POST", "/api/v1/settings/smtp", map[string]any{"host": "", "from": "x@example.com"}); code != 422 {
		t.Fatalf("no host: %d %v", code, out)
	}
	if code, out := w.owner.do("POST", "/api/v1/settings/smtp/test", nil); code != 409 || !strings.Contains(text(out), "not set up") {
		t.Fatalf("test before setup: %d %v", code, out)
	}
	code, out = w.owner.do("POST", "/api/v1/settings/smtp", w.smtpBody())
	if code != 200 {
		t.Fatalf("set smtp: %d %v", code, out)
	}
	smtp := out["smtp"].(map[string]any)
	if smtp["configured"] != true || smtp["source"] != "settings" || smtp["host"] != w.smtp.Host || smtp["username"] != "mailer" || smtp["hasPassword"] != true || smtp["tls"] != "none" {
		t.Fatalf("smtp status = %v", smtp)
	}
	code, out = w.owner.do("POST", "/api/v1/settings/ntfy", map[string]any{"server": "http://ntfy.test", "token": "tk_secret"})
	if code != 200 || out["ntfy"].(map[string]any)["hasToken"] != true || out["ntfy"].(map[string]any)["server"] != "http://ntfy.test" {
		t.Fatalf("set ntfy: %d %v", code, out)
	}

	// Secrets go in and never come out, for anyone.
	for name, b := range map[string]*browser{"owner": w.owner, "member": w.guest} {
		code, out := b.do("GET", "/api/v1/settings/channels", nil)
		if code != 200 {
			t.Fatalf("%s: status %d", name, code)
		}
		if s := text(out); strings.Contains(s, "s3cret-pass") || strings.Contains(s, "tk_secret") {
			t.Fatalf("%s sees a secret: %s", name, s)
		}
	}
	for _, s := range []string{text(out)} {
		if strings.Contains(s, "s3cret-pass") || strings.Contains(s, "tk_secret") {
			t.Fatalf("a secret came back from a save: %s", s)
		}
	}
	// Members see that email works and which ntfy server to use, not how
	// the mail server is reached.
	_, out = w.guest.do("GET", "/api/v1/settings/channels", nil)
	smtp = out["smtp"].(map[string]any)
	if smtp["configured"] != true || smtp["host"] != nil || smtp["username"] != nil || smtp["from"] != nil {
		t.Fatalf("member's view of smtp = %v", smtp)
	}
	if out["ntfy"].(map[string]any)["server"] != "http://ntfy.test" || out["publicUrl"] != "https://exhale.example.com" {
		t.Fatalf("member's view = %v", out)
	}

	// The test email goes to the owner's own address.
	code, out = w.owner.do("POST", "/api/v1/settings/smtp/test", nil)
	if code != 200 || out["sentTo"] != "alex@example.com" {
		t.Fatalf("test email: %d %v", code, out)
	}
	if got := w.smtp.Messages(); len(got) != 1 || got[0].To[0] != "alex@example.com" || got[0].Auth != "mailer" {
		t.Fatalf("mail server got %+v", got)
	}

	// Private destinations: off by default, the owner's switch.
	if code, out := w.owner.do("POST", "/api/v1/settings/ntfy", map[string]any{"server": "http://nas.lan"}); code != 422 || !strings.Contains(text(out), "private network") {
		t.Fatalf("LAN ntfy server: %d %v", code, out)
	}
	if code, out := w.owner.do("POST", "/api/v1/settings/channels", map[string]any{"allowPrivate": true}); code != 200 || out["allowPrivate"] != true {
		t.Fatalf("allow private: %d %v", code, out)
	}
	if code, out := w.owner.do("POST", "/api/v1/settings/ntfy", map[string]any{"server": "http://nas.lan"}); code != 200 || out["ntfy"].(map[string]any)["hasToken"] != false {
		t.Fatalf("LAN ntfy server once allowed (and the token must not follow): %d %v", code, out)
	}
	if code, out := w.owner.do("POST", "/api/v1/settings/ntfy", map[string]any{"server": "http://127.0.0.1:9000"}); code != 422 {
		t.Fatalf("loopback ntfy server: %d %v", code, out)
	}

	if code, out := w.owner.do("POST", "/api/v1/settings/smtp/remove", nil); code != 200 || out["smtp"].(map[string]any)["configured"] != false {
		t.Fatalf("remove smtp: %d %v", code, out)
	}
	if code, out := w.owner.do("POST", "/api/v1/settings/ntfy/remove", nil); code != 200 || out["ntfy"].(map[string]any)["server"] != "https://ntfy.sh" {
		t.Fatalf("remove ntfy: %d %v", code, out)
	}
}

// TestMemberDestinations covers what every member manages: their own
// email, topic and webhooks, with a test message, and nobody else's.
func TestMemberDestinations(t *testing.T) {
	w := channelsServer(t)
	w.owner.do("POST", "/api/v1/settings/smtp", w.smtpBody())
	w.owner.do("POST", "/api/v1/settings/ntfy", map[string]any{"server": "http://ntfy.test"})

	add := func(b *browser, channel, dest, template string) (int, map[string]any) {
		return b.do("POST", "/api/v1/auth/links/destination", map[string]any{"channel": channel, "destination": dest, "template": template})
	}
	if code, _ := newBrowser(t, w.url).do("POST", "/api/v1/auth/links/destination", map[string]any{"channel": "email", "destination": "x@example.com"}); code != 401 {
		t.Fatalf("signed out: %d", code)
	}

	code, mail := add(w.guest, "email", " Marta@Example.com ", "")
	if code != 200 || mail["channel"] != "email" || mail["destination"] != "marta@example.com" {
		t.Fatalf("add email: %d %v", code, mail)
	}
	code, topic := add(w.guest, "ntfy", "marta-reminders", "")
	if code != 200 || topic["destination"] != "marta-reminders" {
		t.Fatalf("add ntfy: %d %v", code, topic)
	}
	code, hook := add(w.guest, "webhook", "http://hooks.test/in/secret-token", `{"content":"{{title}} — {{amount}} {{currency}}"}`)
	if code != 200 || hook["destination"] != "hooks.test" || hook["hasTemplate"] != true {
		t.Fatalf("add webhook: %d %v", code, hook)
	}
	if strings.Contains(text(hook), "secret-token") {
		t.Fatalf("the webhook URL came back: %v", hook)
	}
	// Adding the same one again is not an error and not a second link.
	if code, again := add(w.guest, "email", "marta@example.com", ""); code != 200 || again["id"] != mail["id"] {
		t.Fatalf("same email again: %d %v", code, again)
	}

	for name, c := range map[string][3]string{
		"bad email":        {"email", "nobody", ""},
		"bad topic":        {"ntfy", "has space", ""},
		"not http":         {"webhook", "ftp://hooks.test/x", ""},
		"this machine":     {"webhook", "http://127.0.0.1:8484/api/v1/household", ""},
		"cloud metadata":   {"webhook", "http://169.254.169.254/latest/meta-data/", ""},
		"private network":  {"webhook", "http://nas.lan/hook", ""},
		"bad template":     {"webhook", "http://hooks.test/x", `{"content":{{title}}}`},
		"unknown variable": {"webhook", "http://hooks.test/x", `{"content":"{{password}}"}`},
		"template on mail": {"email", "x@example.com", `{"a":"b"}`},
		"telegram":         {"telegram", "12345", ""},
	} {
		if code, out := add(w.guest, c[0], c[1], c[2]); code != 422 {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}

	// The list shows a member their own destinations, never the webhook URL.
	code, list := w.guest.do("GET", "/api/v1/auth/links", nil)
	if code != 200 || len(list["links"].([]any)) != 3 {
		t.Fatalf("links: %d %v", code, list)
	}
	if strings.Contains(text(list), "secret-token") {
		t.Fatalf("the webhook URL is in the list: %v", list)
	}
	if _, mine := w.owner.do("GET", "/api/v1/auth/links", nil); len(mine["links"].([]any)) != 0 {
		t.Fatalf("the owner sees a member's destinations: %v", mine)
	}

	// Test messages reach each destination.
	for _, id := range []any{mail["id"], topic["id"], hook["id"]} {
		if code, out := w.guest.do("POST", "/api/v1/auth/links/test", map[string]any{"id": id}); code != 200 {
			t.Fatalf("test %v: %d %v", id, code, out)
		}
	}
	if got := w.smtp.Messages(); len(got) != 1 || got[0].To[0] != "marta@example.com" {
		t.Fatalf("mail server got %+v", got)
	}
	if got := w.ntfy.got(); len(got) != 1 || !strings.Contains(got[0], `"topic":"marta-reminders"`) {
		t.Fatalf("ntfy got %v", got)
	}
	if got := w.hook.got(); len(got) != 1 || got[0] != `{"content":"Test reminder from Exhale — 12.99 EUR"}` {
		t.Fatalf("webhook got %v", got)
	}

	// Another member cannot test, remove or take over a destination.
	if code, _ := w.owner.do("POST", "/api/v1/auth/links/test", map[string]any{"id": mail["id"]}); code != 404 {
		t.Fatalf("testing someone else's destination: %d", code)
	}
	if code, _ := w.owner.do("POST", "/api/v1/auth/links/unlink", map[string]any{"id": hook["id"]}); code != 404 {
		t.Fatalf("removing someone else's destination: %d", code)
	}
	if code, out := add(w.owner, "email", "marta@example.com", ""); code != 409 {
		t.Fatalf("taking someone else's address: %d %v", code, out)
	}
	if code, out := add(w.owner, "webhook", "http://hooks.test/in/secret-token", `{"content":"mine now"}`); code != 409 {
		t.Fatalf("taking someone else's webhook: %d %v", code, out)
	}
	if got := w.smtp.Messages(); len(got) != 1 {
		t.Fatalf("the owner's attempts sent mail: %+v", got)
	}
	if _, list := w.guest.do("GET", "/api/v1/auth/links", nil); len(list["links"].([]any)) != 3 {
		t.Fatalf("the member lost a destination: %v", list)
	}
	// The refused takeover did not touch the member's template.
	w.guest.do("POST", "/api/v1/auth/links/test", map[string]any{"id": hook["id"]})
	if got := w.hook.got(); len(got) != 2 || got[1] != got[0] {
		t.Fatalf("webhook got %v", got)
	}

	// A failed delivery tells the member why.
	w.owner.do("POST", "/api/v1/settings/smtp/remove", nil)
	if code, out := w.guest.do("POST", "/api/v1/auth/links/test", map[string]any{"id": mail["id"]}); code != 409 || !strings.Contains(text(out), "not set up") {
		t.Fatalf("test with email removed: %d %v", code, out)
	}

	// Removing your own works, and takes the template with it.
	if code, _ := w.guest.do("POST", "/api/v1/auth/links/unlink", map[string]any{"id": hook["id"]}); code != 200 {
		t.Fatalf("remove: %d", code)
	}
	if _, list := w.guest.do("GET", "/api/v1/auth/links", nil); len(list["links"].([]any)) != 2 {
		t.Fatalf("after removing: %v", list)
	}
	code, hook = add(w.guest, "webhook", "http://hooks.test/in/secret-token", "")
	if code != 200 || hook["hasTemplate"] == true {
		t.Fatalf("add again without a template: %d %v", code, hook)
	}
}

func TestTestMessagesAreThrottled(t *testing.T) {
	w := channelsServer(t)
	_, hook := w.guest.do("POST", "/api/v1/auth/links/destination", map[string]any{"channel": "webhook", "destination": "http://hooks.test/in"})
	last := 0
	for range 15 {
		last, _ = w.guest.do("POST", "/api/v1/auth/links/test", map[string]any{"id": hook["id"]})
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("15 quick test messages should be throttled, last status %d", last)
	}
	if n := len(w.hook.got()); n > 10 {
		t.Fatalf("%d test messages went out", n)
	}
}
