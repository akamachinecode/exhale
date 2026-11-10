// Package email sends reminders by email over SMTP (ADR 0007). It is a
// notify-only channel: the message carries the reminder and one button that
// opens the subscription in the web.
//
// It uses the standard library (net/smtp, crypto/tls): one message per
// reminder to one recipient needs nothing more.
package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/channels/notify"
	"github.com/akamachinecode/exhale/internal/domain"
)

// Name is the channel name in channel_links.
const Name = "email"

// TLS modes.
const (
	TLSStartTLS = "starttls" // plain connection upgraded with STARTTLS (port 587)
	TLSImplicit = "tls"      // TLS from the first byte (port 465)
	TLSNone     = "none"     // no encryption: local relays only
)

// ErrNotConfigured is returned while the owner has not set up SMTP.
var ErrNotConfigured = errors.New("email is not set up")

// Config is how to reach the SMTP server.
type Config struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// From is the sender: "reminders@example.com" or "Exhale <reminders@example.com>".
	From string `json:"from"`
	TLS  string `json:"tls"`
	// RootCAs replaces the system certificates (tests).
	RootCAs *x509.CertPool `json:"-"`
}

// Normalize trims the fields, fills the defaults (STARTTLS, the usual port
// of the TLS mode) and reports what is wrong with the rest.
func (c Config) Normalize() (Config, error) {
	c.Host, c.Username, c.From = strings.TrimSpace(c.Host), strings.TrimSpace(c.Username), strings.TrimSpace(c.From)
	c.TLS = strings.ToLower(strings.TrimSpace(c.TLS))
	bad := func(format string, args ...any) (Config, error) {
		return Config{}, fmt.Errorf("%w: %s", domain.ErrValidation, fmt.Sprintf(format, args...))
	}
	if c.Host == "" || strings.ContainsAny(c.Host, " /\r\n") {
		return bad("the SMTP host is missing; it looks like smtp.example.com")
	}
	switch c.TLS {
	case "":
		c.TLS = TLSStartTLS
	case TLSStartTLS, TLSImplicit, TLSNone:
	default:
		return bad("unknown TLS mode %q; use starttls, tls or none", c.TLS)
	}
	if c.Port == 0 {
		c.Port = map[string]int{TLSStartTLS: 587, TLSImplicit: 465, TLSNone: 25}[c.TLS]
	}
	if c.Port < 1 || c.Port > 65535 {
		return bad("the port must be between 1 and 65535")
	}
	if _, err := c.sender(); err != nil {
		return bad("the from address is not an email address; it looks like reminders@example.com")
	}
	if c.Username == "" {
		c.Password = ""
	} else if c.TLS == TLSNone && !isLocal(c.Host) {
		return bad("a password is never sent without encryption; use STARTTLS or TLS, or leave the username empty")
	}
	return c, nil
}

func (c Config) sender() (*mail.Address, error) {
	a, err := mail.ParseAddress(c.From)
	if err != nil {
		return nil, err
	}
	if a.Name == "" {
		a.Name = "Exhale"
	}
	return a, nil
}

// Address validates an address a member typed and returns it in the form
// accounts use: bare (no display name) and lower case.
func Address(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	a, err := mail.ParseAddress(raw)
	if err != nil || a.Address != raw || len(raw) > 254 {
		return "", fmt.Errorf("%w: that is not an email address", domain.ErrValidation)
	}
	return a.Address, nil
}

func isLocal(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// Mail is one message with a plain-text and an HTML version.
type Mail struct {
	Subject string
	Text    string
	HTML    string
}

// step names the part of the conversation that failed, in words the owner
// can act on. SMTP errors carry no secrets: the password is never echoed.
func step(what string, err error) error {
	var te *textproto.Error
	if errors.As(err, &te) {
		return fmt.Errorf("%s: the server answered %d %s", what, te.Code, te.Msg)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("%s: timed out", what)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// connect opens the connection, negotiates TLS as configured and signs in.
// STARTTLS is never skipped: a server that does not offer it is an error,
// not a reason to continue in the clear.
func connect(ctx context.Context, cfg Config) (*smtp.Client, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, step("could not connect to "+addr, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12, RootCAs: cfg.RootCAs}
	if cfg.TLS == TLSImplicit {
		tc := tls.Client(conn, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, step("TLS with "+addr+" failed (for port 587 choose STARTTLS)", err)
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		if cfg.TLS != TLSImplicit {
			return nil, step(addr+" did not answer as an SMTP server (for port 465 choose TLS)", err)
		}
		return nil, step(addr+" did not answer as an SMTP server", err)
	}
	if cfg.TLS == TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			_ = c.Close()
			return nil, errors.New("the server does not offer STARTTLS; choose TLS (port 465), or none for a local relay")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			_ = c.Close()
			return nil, step("STARTTLS failed", err)
		}
	}
	if cfg.Username != "" {
		a, err := chooseAuth(c, cfg)
		if err == nil {
			err = c.Auth(a)
		}
		if err != nil {
			_ = c.Close()
			var te *textproto.Error
			if errors.As(err, &te) && (te.Code == 535 || te.Code == 534) {
				return nil, errors.New("the server rejected the username or password")
			}
			return nil, step("sign-in failed", err)
		}
	}
	return c, nil
}

func chooseAuth(c *smtp.Client, cfg Config) (smtp.Auth, error) {
	ok, list := c.Extension("AUTH")
	if !ok {
		return nil, errors.New("the server does not accept a username and password; leave the username empty")
	}
	mechs := strings.Fields(strings.ToUpper(list))
	switch {
	case slices.Contains(mechs, "PLAIN"):
		return smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host), nil
	case slices.Contains(mechs, "LOGIN"):
		return &loginAuth{cfg.Username, cfg.Password, cfg.Host}, nil
	case slices.Contains(mechs, "CRAM-MD5"):
		return smtp.CRAMMD5Auth(cfg.Username, cfg.Password), nil
	}
	return nil, fmt.Errorf("the server only offers sign-in methods Exhale does not support (%s)", list)
}

// loginAuth is the LOGIN mechanism, the only one some providers offer
// (Outlook). Like PLAIN in net/smtp it refuses to run without TLS.
type loginAuth struct{ username, password, host string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLocal(server.Name) {
		return "", nil, errors.New("unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimRight(string(fromServer), ": ")) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, errors.New("unexpected question from the server")
}

// Check connects and signs in without sending anything, so a wrong setting
// shows when it is saved rather than when the first reminder is due.
func Check(ctx context.Context, cfg Config) error {
	c, err := connect(ctx, cfg)
	if err != nil {
		return err
	}
	return c.Quit()
}

// Send delivers one message to one recipient.
func Send(ctx context.Context, cfg Config, to string, m Mail) error {
	from, err := cfg.sender()
	if err != nil {
		return fmt.Errorf("the from address is not valid: %w", err)
	}
	if _, err := Address(to); err != nil {
		return err
	}
	raw, err := build(from, to, m, time.Now())
	if err != nil {
		return err
	}
	c, err := connect(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if err := c.Mail(from.Address); err != nil {
		return step("the server refused the sender "+from.Address, err)
	}
	if err := c.Rcpt(to); err != nil {
		return step("the server refused the recipient", err)
	}
	w, err := c.Data()
	if err != nil {
		return step("the server refused the message", err)
	}
	if _, err := w.Write(raw); err != nil {
		return step("sending the message failed", err)
	}
	if err := w.Close(); err != nil {
		return step("the server refused the message", err)
	}
	_ = c.Quit()
	return nil
}

// build writes the message: multipart/alternative, plain text first and
// HTML second, both UTF-8 in quoted-printable.
func build(from *mail.Address, to string, m Mail, now time.Time) ([]byte, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, part := range []struct{ typ, content string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		if part.content == "" {
			continue
		}
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.typ + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		qp := quotedprintable.NewWriter(pw)
		if _, err := qp.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(part.content, "\r\n", "\n"), "\n", "\r\n"))); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domainPart := from.Address[strings.LastIndex(from.Address, "@")+1:]
	subject := strings.Join(strings.Fields(m.Subject), " ") // one line: no header injection
	var msg bytes.Buffer
	for _, h := range [][2]string{
		{"From", from.String()},
		{"To", (&mail.Address{Address: to}).String()},
		{"Subject", mime.QEncoding.Encode("utf-8", subject)},
		{"Date", now.Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + domainPart + ">"},
		{"MIME-Version", "1.0"},
		{"Auto-Submitted", "auto-generated"},
		{"Content-Type", "multipart/alternative; boundary=" + mw.Boundary()},
	} {
		msg.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	msg.WriteString("\r\n")
	msg.Write(body.Bytes())
	return msg.Bytes(), nil
}

// Channel is the email channel. Its chat id is the recipient's address.
type Channel struct {
	notify.OneWay
	// Config returns the current SMTP settings; ok is false while email is
	// not set up. It is read for every message, so a change applies at once.
	Config func(ctx context.Context) (cfg Config, ok bool)
	// PublicURL is the instance's public address ("" if unknown).
	PublicURL string
}

func (c *Channel) Name() string { return Name }

// Send emails the message to one address.
func (c *Channel) Send(ctx context.Context, to string, m actions.Message) (string, error) {
	cfg, ok := c.Config(ctx)
	if !ok {
		return "", ErrNotConfigured
	}
	link := notify.Link(c.PublicURL, m)
	return "", Send(ctx, cfg, to, Mail{Subject: notify.Subject(m), Text: notify.Text(m, link), HTML: notify.HTML(m, link, notify.SettingsLink(c.PublicURL))})
}
