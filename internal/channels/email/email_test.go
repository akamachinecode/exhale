package email_test

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/channels/email"
	"github.com/akamachinecode/exhale/internal/channels/email/smtptest"
)

var reminder = actions.Message{
	Title: "🔔 Netflix renews in 3 days",
	Lines: []string{"💶 12,99 € · Fri, 13 Nov 2026", "💳 Visa ••4421"},
	Rows:  [][]actions.Button{{{Label: "✅ Already paid", Data: "paid|chg_1"}}},
	Event: &actions.Event{Kind: actions.EventReminder, SubscriptionID: "sub_1", Subscription: "Netflix", Amount: "12.99", Currency: "EUR", DueDate: "2026-11-13", DaysUntil: 3, Path: "/subscriptions/sub_1"},
}

func channel(cfg email.Config, publicURL string) *email.Channel {
	return &email.Channel{Config: func(context.Context) (email.Config, bool) { return cfg, true }, PublicURL: publicURL}
}

func config(s *smtptest.Server, tls string) email.Config {
	return email.Config{Host: s.Host, Port: s.Port, From: "Casa <reminders@example.com>", TLS: tls, RootCAs: s.Roots()}
}

// parts returns the decoded headers and bodies of a captured message.
func parts(t *testing.T, data string) (mail.Header, map[string]string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(data))
	if err != nil {
		t.Fatalf("not a mail message: %v\n%s", err, data)
	}
	typ, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || typ != "multipart/alternative" {
		t.Fatalf("content type = %q (%v)", msg.Header.Get("Content-Type"), err)
	}
	out := map[string]string{}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		p, err := mr.NextPart() // decodes quoted-printable
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		pt, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		out[pt] = string(b)
	}
	return msg.Header, out
}

func TestSendPlainRelay(t *testing.T) {
	srv := (&smtptest.Server{}).Start(t)
	ch := channel(config(srv, email.TLSNone), "https://exhale.example.com/")
	id, err := ch.Send(context.Background(), "alex@example.com", reminder)
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("message id = %q; a notify-only channel returns none", id)
	}
	got := srv.Messages()
	if len(got) != 1 {
		t.Fatalf("messages = %d", len(got))
	}
	m := got[0]
	if m.From != "reminders@example.com" || len(m.To) != 1 || m.To[0] != "alex@example.com" {
		t.Fatalf("envelope = %s → %v", m.From, m.To)
	}
	hdr, body := parts(t, m.Data)
	subject, _ := new(mime.WordDecoder).DecodeHeader(hdr.Get("Subject"))
	if subject != "Netflix renews in 3 days — 12.99 EUR" {
		t.Fatalf("subject = %q", subject)
	}
	if from, _ := hdr.AddressList("From"); len(from) != 1 || from[0].Name != "Casa" || from[0].Address != "reminders@example.com" {
		t.Fatalf("from = %q", hdr.Get("From"))
	}
	if hdr.Get("Message-Id") == "" || hdr.Get("Date") == "" || hdr.Get("Auto-Submitted") != "auto-generated" {
		t.Fatalf("headers = %v", hdr)
	}
	text, html := body["text/plain"], body["text/html"]
	for _, want := range []string{"🔔 Netflix renews in 3 days", "💶 12,99 € · Fri, 13 Nov 2026", "Open in Exhale: https://exhale.example.com/subscriptions/sub_1"} {
		if !strings.Contains(text, want) {
			t.Errorf("text part misses %q:\n%s", want, text)
		}
	}
	for _, want := range []string{"Netflix renews in 3 days</h1>", `href="https://exhale.example.com/subscriptions/sub_1"`, "Open in Exhale</a>", `content="light dark"`, `href="https://exhale.example.com/settings"`} {
		if !strings.Contains(html, want) {
			t.Errorf("html part misses %q:\n%s", want, html)
		}
	}
	for _, s := range []string{text, html} {
		if strings.Contains(s, "paid|chg_1") || strings.Contains(s, "Already paid") {
			t.Errorf("buttons leaked into the email:\n%s", s)
		}
	}
}

func TestSendWithoutPublicURLOmitsTheLink(t *testing.T) {
	srv := (&smtptest.Server{}).Start(t)
	if _, err := channel(config(srv, email.TLSNone), "").Send(context.Background(), "alex@example.com", reminder); err != nil {
		t.Fatal(err)
	}
	_, body := parts(t, srv.Messages()[0].Data)
	if strings.Contains(body["text/plain"], "Open in Exhale") || strings.Contains(body["text/html"], "<a ") {
		t.Fatalf("a link without a public address:\n%s\n%s", body["text/plain"], body["text/html"])
	}
}

func TestStartTLSAndAuth(t *testing.T) {
	for _, mechs := range []string{"PLAIN LOGIN", "LOGIN"} {
		t.Run(mechs, func(t *testing.T) {
			srv := (&smtptest.Server{StartTLS: true, Username: "alex", Password: "s3cret", Mechanisms: mechs}).Start(t)
			cfg := config(srv, email.TLSStartTLS)
			cfg.Username, cfg.Password = "alex", "s3cret"
			if err := email.Check(context.Background(), cfg); err != nil {
				t.Fatalf("check: %v", err)
			}
			if _, err := channel(cfg, "").Send(context.Background(), "alex@example.com", reminder); err != nil {
				t.Fatal(err)
			}
			got := srv.Messages()
			if len(got) != 1 || !got[0].TLS || got[0].Auth != "alex" {
				t.Fatalf("messages = %+v; want one sent over TLS as alex", got)
			}
		})
	}
}

func TestImplicitTLS(t *testing.T) {
	srv := (&smtptest.Server{Implicit: true, Username: "alex", Password: "s3cret"}).Start(t)
	cfg := config(srv, email.TLSImplicit)
	cfg.Username, cfg.Password = "alex", "s3cret"
	if _, err := channel(cfg, "").Send(context.Background(), "alex@example.com", reminder); err != nil {
		t.Fatal(err)
	}
	if got := srv.Messages(); len(got) != 1 || !got[0].TLS {
		t.Fatalf("messages = %+v", got)
	}
}

func TestFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("wrong password", func(t *testing.T) {
		srv := (&smtptest.Server{StartTLS: true, Username: "alex", Password: "s3cret"}).Start(t)
		cfg := config(srv, email.TLSStartTLS)
		cfg.Username, cfg.Password = "alex", "not-the-password"
		err := email.Check(ctx, cfg)
		if err == nil || !strings.Contains(err.Error(), "rejected the username or password") {
			t.Fatalf("error = %v", err)
		}
		if strings.Contains(err.Error(), "not-the-password") {
			t.Fatalf("the password is in the error: %v", err)
		}
		if _, err := channel(cfg, "").Send(ctx, "alex@example.com", reminder); err == nil {
			t.Fatal("sent without signing in")
		}
		if len(srv.Messages()) != 0 {
			t.Fatal("a message was accepted")
		}
	})
	t.Run("starttls is never skipped", func(t *testing.T) {
		srv := (&smtptest.Server{}).Start(t) // offers no STARTTLS
		err := email.Check(ctx, config(srv, email.TLSStartTLS))
		if err == nil || !strings.Contains(err.Error(), "does not offer STARTTLS") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("untrusted certificate", func(t *testing.T) {
		srv := (&smtptest.Server{StartTLS: true}).Start(t)
		cfg := config(srv, email.TLSStartTLS)
		cfg.RootCAs = nil // system roots do not know the test certificate
		if err := email.Check(ctx, cfg); err == nil || !strings.Contains(err.Error(), "STARTTLS failed") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("server needs sign-in", func(t *testing.T) {
		srv := (&smtptest.Server{StartTLS: true, Username: "alex", Password: "s3cret"}).Start(t)
		_, err := channel(config(srv, email.TLSStartTLS), "").Send(ctx, "alex@example.com", reminder)
		if err == nil || !strings.Contains(err.Error(), "530") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("recipient refused", func(t *testing.T) {
		srv := (&smtptest.Server{RejectRcpt: true}).Start(t)
		_, err := channel(config(srv, email.TLSNone), "").Send(ctx, "alex@example.com", reminder)
		if err == nil || !strings.Contains(err.Error(), "refused the recipient") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("nothing listening", func(t *testing.T) {
		srv := (&smtptest.Server{}).Start(t)
		cfg := config(srv, email.TLSNone)
		cfg.Port = 1
		if err := email.Check(ctx, cfg); err == nil || !strings.Contains(err.Error(), "could not connect") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("not set up", func(t *testing.T) {
		ch := &email.Channel{Config: func(context.Context) (email.Config, bool) { return email.Config{}, false }}
		if _, err := ch.Send(ctx, "alex@example.com", reminder); err != email.ErrNotConfigured {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestNormalize(t *testing.T) {
	ok := func(c email.Config) email.Config {
		t.Helper()
		out, err := c.Normalize()
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if c := ok(email.Config{Host: " smtp.example.com ", From: "a@example.com"}); c.TLS != email.TLSStartTLS || c.Port != 587 || c.Host != "smtp.example.com" {
		t.Fatalf("defaults = %+v", c)
	}
	if c := ok(email.Config{Host: "smtp.example.com", From: "a@example.com", TLS: "tls"}); c.Port != 465 {
		t.Fatalf("port = %d", c.Port)
	}
	if c := ok(email.Config{Host: "localhost", From: "a@example.com", TLS: "none", Password: "x"}); c.Port != 25 || c.Password != "" {
		t.Fatalf("local relay = %+v", c)
	}
	for name, c := range map[string]email.Config{
		"no host":              {From: "a@example.com"},
		"host with a path":     {Host: "smtp.example.com/x", From: "a@example.com"},
		"bad from":             {Host: "smtp.example.com", From: "nobody"},
		"bad tls":              {Host: "smtp.example.com", From: "a@example.com", TLS: "ssl3"},
		"bad port":             {Host: "smtp.example.com", From: "a@example.com", Port: 70000},
		"password in the open": {Host: "smtp.example.com", From: "a@example.com", TLS: "none", Username: "u", Password: "p"},
	} {
		if _, err := c.Normalize(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAddress(t *testing.T) {
	if a, err := email.Address(" Alex@Example.com "); err != nil || a != "alex@example.com" {
		t.Fatalf("Address = %q, %v", a, err)
	}
	for _, bad := range []string{"", "alex", "Alex <alex@example.com>", "a@example.com\r\nBcc: x@example.com", "a@example.com, b@example.com"} {
		if _, err := email.Address(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
