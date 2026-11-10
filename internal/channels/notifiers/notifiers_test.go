package notifiers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/channels/email"
	"github.com/akamachinecode/exhale/internal/channels/email/smtptest"
	"github.com/akamachinecode/exhale/internal/channels/notifiers"
	"github.com/akamachinecode/exhale/internal/channels/ntfy"
	"github.com/akamachinecode/exhale/internal/channels/webhook"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/testutil"
)

// endpoint is a fake HTTP service (an ntfy server, a webhook receiver).
type endpoint struct {
	mu     sync.Mutex
	status int
	bodies []string
	auth   []string
}

func (e *endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bodies = append(e.bodies, string(raw))
	e.auth = append(e.auth, r.Header.Get("Authorization"))
	if e.status != 0 {
		w.WriteHeader(e.status)
	}
}

func (e *endpoint) fail(status int) { e.mu.Lock(); e.status = status; e.mu.Unlock() }

func (e *endpoint) got() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.bodies...)
}

// world is an Exhale store with fake services around it: a mail server, an
// ntfy server at http://ntfy.test and a webhook receiver at
// http://hooks.test. nas.lan is a host on a private network.
type world struct {
	st   *store.Store
	smtp *smtptest.Server
	ntfy *endpoint
	hook *endpoint
	opts notifiers.Options
}

func newWorld(t *testing.T) *world {
	t.Helper()
	st, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w := &world{st: st, ntfy: &endpoint{}, hook: &endpoint{}}
	w.smtp = (&smtptest.Server{Username: "alex", Password: "s3cret-pass"}).Start(t)
	ntfySrv, hookSrv := httptest.NewServer(w.ntfy), httptest.NewServer(w.hook)
	t.Cleanup(ntfySrv.Close)
	t.Cleanup(hookSrv.Close)
	net := testutil.NewInternet().
		Host("ntfy.test", ntfySrv.Listener.Addr().String()).
		Host("hooks.test", hookSrv.Listener.Addr().String()).
		PrivateHost("nas.lan", "192.168.1.20").
		PrivateHost("sneaky.test", "127.0.0.1")
	w.opts = notifiers.Options{PublicURL: "https://exhale.example.com/", Version: "test", Guard: net.Guard(nil)}
	return w
}

func (w *world) manager(t *testing.T) *notifiers.Manager {
	t.Helper()
	m, err := notifiers.New(w.st, w.opts, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (w *world) smtpInput() notifiers.SMTPInput {
	return notifiers.SMTPInput{Host: w.smtp.Host, Port: w.smtp.Port, Username: "alex", Password: "s3cret-pass", From: "reminders@example.com", TLS: "none"}
}

func channel(t *testing.T, m *notifiers.Manager, name string) actions.Channel {
	t.Helper()
	for _, c := range m.Channels() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("no %s channel", name)
	return nil
}

var reminder = actions.Message{
	Title: "🔔 Netflix renews in 3 days",
	Lines: []string{"💶 12,99 € · Fri, 13 Nov 2026"},
	Event: &actions.Event{Kind: actions.EventReminder, Subscription: "Netflix", Amount: "12.99", Currency: "EUR", DueDate: "2026-11-13", DaysUntil: 3, Path: "/subscriptions/sub_1"},
}

func TestSMTPSettings(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	m := w.manager(t)
	mail := channel(t, m, email.Name)

	if st := m.Status(ctx, true); st.SMTP.Configured || st.PublicURL != "https://exhale.example.com" {
		t.Fatalf("fresh status = %+v", st)
	}
	if _, err := mail.Send(ctx, "alex@example.com", reminder); !errors.Is(err, email.ErrNotConfigured) {
		t.Fatalf("send before setup: %v", err)
	}

	// A wrong password is caught when saving, and nothing is saved.
	bad := w.smtpInput()
	bad.Password = "wrong"
	if err := m.SetSMTP(ctx, bad); !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "rejected the username or password") {
		t.Fatalf("wrong password: %v", err)
	}
	if m.Status(ctx, true).SMTP.Configured {
		t.Fatal("a failed check was saved")
	}

	if err := m.SetSMTP(ctx, w.smtpInput()); err != nil {
		t.Fatal(err)
	}
	st := m.Status(ctx, true)
	if !st.SMTP.Configured || st.SMTP.Source != "settings" || st.SMTP.Host != w.smtp.Host || st.SMTP.Username != "alex" || !st.SMTP.HasPassword || st.SMTP.From != "reminders@example.com" || st.SMTP.TLS != "none" {
		t.Fatalf("status = %+v", st.SMTP)
	}
	if raw, _ := json.Marshal(st); strings.Contains(string(raw), "s3cret-pass") {
		t.Fatalf("the password is in the status: %s", raw)
	}
	// Members who are not owners only learn that email works.
	if got := m.Status(ctx, false).SMTP; !got.Configured || got.Host != "" || got.Username != "" || got.From != "" || got.HasPassword {
		t.Fatalf("status for members = %+v", got)
	}

	// The same channel object now sends: no restart.
	if _, err := mail.Send(ctx, "alex@example.com", reminder); err != nil {
		t.Fatal(err)
	}
	if got := w.smtp.Messages(); len(got) != 1 || got[0].Auth != "alex" {
		t.Fatalf("messages = %+v", got)
	}

	// An empty password keeps the saved one, but only for the same server
	// and user.
	keep := w.smtpInput()
	keep.Password, keep.From = "", "Casa <casa@example.com>"
	if err := m.SetSMTP(ctx, keep); err != nil {
		t.Fatalf("keeping the password: %v", err)
	}
	if got := m.Status(ctx, true).SMTP; got.From != "Casa <casa@example.com>" || !got.HasPassword {
		t.Fatalf("status = %+v", got)
	}
	moved := keep
	moved.Host = "localhost"
	if err := m.SetSMTP(ctx, moved); !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "enter the password") {
		t.Fatalf("the saved password would go to another server: %v", err)
	}

	if err := m.Test(ctx, email.Name, "alex@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := w.smtp.Messages(); len(got) != 2 || !strings.Contains(got[1].Data, "Test reminder from Exhale") {
		t.Fatalf("test message: %+v", got)
	}

	if err := m.RemoveSMTP(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := mail.Send(ctx, "alex@example.com", reminder); !errors.Is(err, email.ErrNotConfigured) {
		t.Fatalf("send after removal: %v", err)
	}
}

func TestEnvironmentWins(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	yes := true
	w.opts.SMTP = email.Config{Host: w.smtp.Host, Port: w.smtp.Port, Username: "alex", Password: "s3cret-pass", From: "env@example.com", TLS: "none"}
	w.opts.NtfyServer, w.opts.NtfyToken = "http://ntfy.test/", "tk_env"
	w.opts.AllowPrivate = &yes
	m := w.manager(t)

	st := m.Status(ctx, true)
	if st.SMTP.Source != "env" || st.SMTP.From != "env@example.com" || st.Ntfy.Source != "env" || st.Ntfy.Server != "http://ntfy.test" || !st.Ntfy.HasToken || !st.AllowPrivate || st.AllowPrivateSource != "env" {
		t.Fatalf("status = %+v", st)
	}
	if raw, _ := json.Marshal(st); strings.Contains(string(raw), "s3cret-pass") || strings.Contains(string(raw), "tk_env") {
		t.Fatalf("a secret is in the status: %s", raw)
	}
	for name, err := range map[string]error{
		"SetSMTP":         m.SetSMTP(ctx, w.smtpInput()),
		"RemoveSMTP":      m.RemoveSMTP(ctx),
		"SetNtfy":         m.SetNtfy(ctx, notifiers.NtfyInput{Server: "https://ntfy.sh"}),
		"RemoveNtfy":      m.RemoveNtfy(ctx),
		"SetAllowPrivate": m.SetAllowPrivate(ctx, false),
	} {
		if !errors.Is(err, domain.ErrConflict) {
			t.Errorf("%s with the environment set: %v", name, err)
		}
	}
	if _, err := channel(t, m, email.Name).Send(ctx, "alex@example.com", reminder); err != nil {
		t.Fatal(err)
	}
	if _, err := channel(t, m, ntfy.Name).Send(ctx, "alex", reminder); err != nil {
		t.Fatal(err)
	}
	if got := w.ntfy.auth; len(got) != 1 || got[0] != "Bearer tk_env" {
		t.Fatalf("ntfy auth = %v", got)
	}

	// Bad values stop the server at start, with the variable named.
	w.opts.SMTP.From = "nobody"
	if _, err := notifiers.New(w.st, w.opts, slog.New(slog.DiscardHandler)); err == nil || !strings.Contains(err.Error(), "EXHALE_SMTP") {
		t.Fatalf("bad EXHALE_SMTP_FROM: %v", err)
	}
	w.opts.SMTP = email.Config{}
	w.opts.PublicURL = "exhale.example.com"
	if _, err := notifiers.New(w.st, w.opts, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("a public URL without a scheme was accepted")
	}
}

func TestNtfySettings(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	m := w.manager(t)
	push := channel(t, m, ntfy.Name)

	if st := m.Status(ctx, false).Ntfy; st.Server != "https://ntfy.sh" || st.Source != "" || st.HasToken {
		t.Fatalf("default = %+v", st)
	}
	if err := m.SetNtfy(ctx, notifiers.NtfyInput{Server: "http://ntfy.test/", Token: "tk_secret"}); err != nil {
		t.Fatal(err)
	}
	st := m.Status(ctx, true)
	if st.Ntfy.Server != "http://ntfy.test" || st.Ntfy.Source != "settings" || !st.Ntfy.HasToken {
		t.Fatalf("status = %+v", st.Ntfy)
	}
	if raw, _ := json.Marshal(st); strings.Contains(string(raw), "tk_secret") {
		t.Fatalf("the token is in the status: %s", raw)
	}
	if _, err := push.Send(ctx, "alex-reminders", reminder); err != nil {
		t.Fatal(err)
	}
	if len(w.ntfy.got()) != 1 || w.ntfy.auth[0] != "Bearer tk_secret" || !strings.Contains(w.ntfy.got()[0], `"topic":"alex-reminders"`) {
		t.Fatalf("ntfy got %v with %v", w.ntfy.got(), w.ntfy.auth)
	}

	// An empty token keeps the saved one for the same server…
	if err := m.SetNtfy(ctx, notifiers.NtfyInput{Server: "http://ntfy.test"}); err != nil {
		t.Fatal(err)
	}
	if !m.Status(ctx, true).Ntfy.HasToken {
		t.Fatal("the token was dropped")
	}
	// …is never carried to another server…
	if err := m.SetNtfy(ctx, notifiers.NtfyInput{Server: "http://hooks.test"}); err != nil {
		t.Fatal(err)
	}
	if m.Status(ctx, true).Ntfy.HasToken {
		t.Fatal("the token followed to another server")
	}
	// …and can be removed.
	if err := m.SetNtfy(ctx, notifiers.NtfyInput{Server: "http://ntfy.test", Token: "tk_secret"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetNtfy(ctx, notifiers.NtfyInput{Server: "http://ntfy.test", RemoveToken: true}); err != nil {
		t.Fatal(err)
	}
	if m.Status(ctx, true).Ntfy.HasToken {
		t.Fatal("the token was not removed")
	}

	for name, in := range map[string]notifiers.NtfyInput{
		"not a url":       {Server: "ntfy.test"},
		"token in url":    {Server: "http://user:pass@ntfy.test"},
		"this machine":    {Server: "http://127.0.0.1:8080"},
		"name → loopback": {Server: "http://sneaky.test"},
		"private network": {Server: "http://nas.lan"},
		"unknown host":    {Server: "http://nope.test"},
		"bad token":       {Server: "http://ntfy.test", Token: "tk_a\r\nX-Evil: 1"},
	} {
		if err := m.SetNtfy(ctx, in); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// A LAN server works once the owner allows private destinations.
	if err := m.SetAllowPrivate(ctx, true); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(ctx, true); !st.AllowPrivate || st.AllowPrivateSource != "settings" {
		t.Fatalf("status = %+v", st)
	}
	if err := m.SetNtfy(ctx, notifiers.NtfyInput{Server: "http://nas.lan"}); err != nil {
		t.Fatalf("LAN server with private allowed: %v", err)
	}
	if err := m.SetNtfy(ctx, notifiers.NtfyInput{Server: "http://sneaky.test"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("loopback with private allowed: %v", err)
	}
	if err := m.RemoveNtfy(ctx); err != nil {
		t.Fatal(err)
	}
	if got := m.Status(ctx, true).Ntfy; got.Server != "https://ntfy.sh" || got.Source != "" {
		t.Fatalf("after removal = %+v", got)
	}
}

func TestDestinations(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	m := w.manager(t)

	for _, c := range []struct{ channel, in, template, want string }{
		{"email", " Alex@Example.com ", "", "alex@example.com"},
		{"ntfy", "https://ntfy.sh/alex-reminders", "", "alex-reminders"},
		{"webhook", " http://hooks.test/in/secret?x=1 ", "", "http://hooks.test/in/secret?x=1"},
		{"webhook", "http://hooks.test/in", `{"content":"{{text}}"}`, "http://hooks.test/in"},
	} {
		got, err := m.Validate(ctx, c.channel, c.in, c.template)
		if err != nil || got != c.want {
			t.Errorf("Validate(%s, %q) = %q, %v", c.channel, c.in, got, err)
		}
	}
	for _, c := range []struct{ channel, in, template string }{
		{"email", "not an address", ""},
		{"email", "alex@example.com", `{"a":"b"}`}, // templates are for webhooks
		{"ntfy", "has space", ""},
		{"webhook", "ftp://hooks.test/in", ""},
		{"webhook", "http://127.0.0.1:8484/api/v1/household", ""},
		{"webhook", "http://169.254.169.254/latest/meta-data/", ""},
		{"webhook", "http://sneaky.test/in", ""},
		{"webhook", "http://nas.lan/in", ""},
		{"webhook", "http://hooks.test/in", `{"content":{{title}}}`},
		{"webhook", "http://hooks.test/in", `{"content":"{{nope}}"}`},
		{"webhook", "http://hooks.test/" + strings.Repeat("a", 2100), ""},
		{"telegram", "12345", ""},
		{"slack", "x", ""},
	} {
		if _, err := m.Validate(ctx, c.channel, c.in, c.template); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("Validate(%s, %q, %q) = %v", c.channel, c.in, c.template, err)
		}
	}
	if err := m.SetAllowPrivate(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Validate(ctx, "webhook", "http://nas.lan/in", ""); err != nil {
		t.Fatalf("LAN webhook with private allowed: %v", err)
	}
	if _, err := m.Validate(ctx, "webhook", "http://sneaky.test/in", ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("loopback with private allowed: %v", err)
	}

	// The body template is kept per URL and read for every message.
	url := "http://hooks.test/in/secret-token"
	hook := channel(t, m, webhook.Name)
	if _, err := hook.Send(ctx, url, reminder); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTemplate(ctx, url, `{"content":"{{title}} · {{amount}} {{currency}}"}`); err != nil {
		t.Fatal(err)
	}
	if text, has := m.Describe(ctx, "webhook", url); text != "hooks.test" || !has {
		t.Fatalf("Describe = %q, %v", text, has)
	}
	if _, err := hook.Send(ctx, url, reminder); err != nil {
		t.Fatal(err)
	}
	if err := m.Forget(ctx, "webhook", url); err != nil {
		t.Fatal(err)
	}
	if err := m.Test(ctx, "webhook", url); err != nil {
		t.Fatal(err)
	}
	got := w.hook.got()
	if len(got) != 3 || !strings.Contains(got[0], `"event":"reminder"`) || got[1] != `{"content":"Netflix renews in 3 days · 12.99 EUR"}` || !strings.Contains(got[2], `"event":"test"`) {
		t.Fatalf("webhook got %q", got)
	}
	if !strings.Contains(got[0], `"link":"https://exhale.example.com/subscriptions/sub_1"`) {
		t.Fatalf("no link in %s", got[0])
	}
	if text, has := m.Describe(ctx, "email", "alex@example.com"); text != "alex@example.com" || has {
		t.Fatalf("Describe = %q, %v", text, has)
	}
	if err := m.Test(ctx, "telegram", "1"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("test on telegram: %v", err)
	}
	if !m.Notifies("email") || !m.Notifies("ntfy") || !m.Notifies("webhook") || m.Notifies("telegram") {
		t.Fatal("Notifies")
	}
}
