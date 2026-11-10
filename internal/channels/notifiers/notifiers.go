// Package notifiers runs the notify-only channels (email, ntfy, webhooks,
// Web Push): it owns their instance settings and hands the channels to the
// action layer.
//
// Settings come from the environment (EXHALE_SMTP_*, EXHALE_NTFY_*,
// EXHALE_ALLOW_PRIVATE_DESTINATIONS) or, more usually, from the web, where
// they are saved in the settings table like the Telegram token. The
// environment wins. Every message reads the current settings, so a change
// applies at once, without a restart. Secrets go in and never come back
// out: Status only says whether one is saved.
package notifiers

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/channels/email"
	"github.com/akamachinecode/exhale/internal/channels/notify"
	"github.com/akamachinecode/exhale/internal/channels/ntfy"
	"github.com/akamachinecode/exhale/internal/channels/webhook"
	"github.com/akamachinecode/exhale/internal/channels/webpush"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/netguard"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Keys of the settings table.
const (
	keySMTP         = "smtp"                       // JSON email.Config
	keyNtfy         = "ntfy"                       // JSON ntfy.Config
	keyAllowPrivate = "allow_private_destinations" // "true" or "false"
	keyTemplate     = "webhook_template:"          // + hash of the URL → body template
)

// Sources of a setting.
const (
	SourceEnv      = "env"
	SourceSettings = "settings"
)

// Options is what the server was started with.
type Options struct {
	// PublicURL is the address people open Exhale at (--public-url). Links
	// in messages need it; without it they are left out.
	PublicURL string
	// SMTP comes from EXHALE_SMTP_*; it applies when Host is set.
	SMTP email.Config
	// NtfyServer and NtfyToken come from EXHALE_NTFY_URL and _TOKEN.
	NtfyServer, NtfyToken string
	// AllowPrivate comes from EXHALE_ALLOW_PRIVATE_DESTINATIONS; nil when
	// it is not set.
	AllowPrivate *bool
	// Version goes in the User-Agent of webhooks.
	Version string

	// Guard replaces the network rules' resolver and dialer (tests).
	Guard *netguard.Guard
	// SMTPRoots replaces the system certificates for SMTP (tests).
	SMTPRoots *x509.CertPool
	// PushRoots replaces the system certificates for push services (tests).
	PushRoots *x509.CertPool
}

// Manager configures the channels and sends through them.
type Manager struct {
	st        *store.Store
	log       *slog.Logger
	opts      Options
	publicURL string
	guard     *netguard.Guard
	channels  map[string]actions.Channel
	order     []actions.Channel

	// Web Push (webpush.go).
	pushMu      sync.Mutex
	vapid       *webpush.VAPID
	actionKey   []byte
	pushActions PushActions
}

// New checks the options and returns a manager.
func New(st *store.Store, opts Options, log *slog.Logger) (*Manager, error) {
	m := &Manager{st: st, log: log, opts: opts}
	if strings.TrimSpace(opts.PublicURL) != "" {
		if m.publicURL = notify.PublicURL(opts.PublicURL); m.publicURL == "" {
			return nil, fmt.Errorf("the public URL %q is not an http or https address", opts.PublicURL)
		}
	}
	if opts.SMTP.Host != "" {
		cfg, err := opts.SMTP.Normalize()
		if err != nil {
			return nil, fmt.Errorf("EXHALE_SMTP_*: %w", err)
		}
		m.opts.SMTP = cfg
	}
	if opts.NtfyServer != "" {
		server, err := cleanServer(opts.NtfyServer)
		if err != nil {
			return nil, fmt.Errorf("EXHALE_NTFY_URL: %w", err)
		}
		m.opts.NtfyServer = server
	}
	m.guard = &netguard.Guard{AllowPrivate: m.allowPrivate}
	if opts.Guard != nil {
		m.guard.Resolver, m.guard.Dial = opts.Guard.Resolver, opts.Guard.Dial
	}
	m.order = []actions.Channel{
		&email.Channel{
			Config:    func(ctx context.Context) (email.Config, bool) { cfg, _ := m.smtp(ctx); return cfg, cfg.Host != "" },
			PublicURL: m.publicURL,
		},
		&ntfy.Channel{
			Config:    func(ctx context.Context) ntfy.Config { cfg, _ := m.ntfy(ctx); return cfg },
			Client:    m.guard.Client(15 * time.Second),
			PublicURL: m.publicURL,
		},
		&webhook.Channel{
			Client:    m.guard.Client(10 * time.Second),
			Template:  m.template,
			PublicURL: m.publicURL,
			UserAgent: "Exhale/" + opts.Version,
		},
		m.newPush(),
	}
	m.channels = map[string]actions.Channel{}
	for _, c := range m.order {
		m.channels[c.Name()] = c
	}
	return m, nil
}

// Channels returns the email, ntfy, webhook and Web Push channels for the
// action layer.
func (m *Manager) Channels() []actions.Channel { return m.order }

// Notifies reports whether channel is one of the notify-only channels.
func (m *Manager) Notifies(channel string) bool { _, ok := m.channels[channel]; return ok }

func (m *Manager) setting(ctx context.Context, key string) string {
	v, err := m.st.Q.GetSetting(ctx, key)
	if err != nil {
		return ""
	}
	return v
}

func (m *Manager) put(ctx context.Context, key, value string) error {
	return m.st.Q.PutSetting(ctx, db.PutSettingParams{Key: key, Value: value})
}

func (m *Manager) smtp(ctx context.Context) (cfg email.Config, source string) {
	if m.opts.SMTP.Host != "" {
		cfg, source = m.opts.SMTP, SourceEnv
	} else if raw := m.setting(ctx, keySMTP); raw != "" && json.Unmarshal([]byte(raw), &cfg) == nil && cfg.Host != "" {
		source = SourceSettings
	} else {
		return email.Config{}, ""
	}
	cfg.RootCAs = m.opts.SMTPRoots
	return cfg, source
}

func (m *Manager) ntfy(ctx context.Context) (cfg ntfy.Config, source string) {
	if m.opts.NtfyServer != "" || m.opts.NtfyToken != "" {
		cfg, source = ntfy.Config{Server: m.opts.NtfyServer, Token: m.opts.NtfyToken}, SourceEnv
	} else if raw := m.setting(ctx, keyNtfy); raw != "" && json.Unmarshal([]byte(raw), &cfg) == nil && cfg.Server != "" {
		source = SourceSettings
	}
	if cfg.Server == "" {
		cfg.Server = ntfy.DefaultServer
	}
	return cfg, source
}

func (m *Manager) allowPrivate(ctx context.Context) bool {
	if m.opts.AllowPrivate != nil {
		return *m.opts.AllowPrivate
	}
	return m.setting(ctx, keyAllowPrivate) == "true"
}

// SMTPStatus describes the email setup without its password.
type SMTPStatus struct {
	Configured  bool   `json:"configured"`
	Source      string `json:"source,omitempty" enum:"env,settings" doc:"env: set by EXHALE_SMTP_* on the server and not editable in the web"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	Username    string `json:"username,omitempty"`
	From        string `json:"from,omitempty"`
	TLS         string `json:"tls,omitempty" enum:"starttls,tls,none"`
	HasPassword bool   `json:"hasPassword" doc:"a password is saved; it is never returned"`
}

// NtfyStatus describes the ntfy setup without its access token.
type NtfyStatus struct {
	Source   string `json:"source,omitempty" enum:"env,settings" doc:"empty: the default public server"`
	Server   string `json:"server" doc:"where members subscribe to their topic"`
	HasToken bool   `json:"hasToken" doc:"an access token is saved; it is never returned"`
}

// ChannelsStatus is what the web shows about the notify-only channels.
type ChannelsStatus struct {
	PublicURL          string     `json:"publicUrl,omitempty" doc:"address used for links in messages; empty when the server was started without --public-url"`
	AllowPrivate       bool       `json:"allowPrivate" doc:"webhooks and ntfy may reach private-network addresses"`
	AllowPrivateSource string     `json:"allowPrivateSource,omitempty" enum:"env,settings"`
	SMTP               SMTPStatus `json:"smtp"`
	Ntfy               NtfyStatus `json:"ntfy"`
}

// Status reports the setup. Members who are not owners see whether email
// works and which ntfy server to use, not how the mail server is reached.
func (m *Manager) Status(ctx context.Context, owner bool) ChannelsStatus {
	st := ChannelsStatus{PublicURL: m.publicURL, AllowPrivate: m.allowPrivate(ctx)}
	switch {
	case m.opts.AllowPrivate != nil:
		st.AllowPrivateSource = SourceEnv
	case m.setting(ctx, keyAllowPrivate) != "":
		st.AllowPrivateSource = SourceSettings
	}
	if cfg, src := m.smtp(ctx); src != "" {
		st.SMTP = SMTPStatus{Configured: true, Source: src}
		if owner {
			st.SMTP.Host, st.SMTP.Port, st.SMTP.Username = cfg.Host, cfg.Port, cfg.Username
			st.SMTP.From, st.SMTP.TLS, st.SMTP.HasPassword = cfg.From, cfg.TLS, cfg.Password != ""
		}
	}
	cfg, src := m.ntfy(ctx)
	st.Ntfy = NtfyStatus{Source: src, Server: cfg.Server, HasToken: cfg.Token != ""}
	return st
}

func invalid(err error) error {
	if errors.Is(err, domain.ErrValidation) {
		return err
	}
	msg := strings.TrimPrefix(err.Error(), netguard.ErrBlocked.Error()+": ")
	return fmt.Errorf("%w: %s", domain.ErrValidation, msg)
}

func fromEnv(what, vars string) error {
	return fmt.Errorf("%w: %s is set by %s on the server; change it there", domain.ErrConflict, what, vars)
}

// SMTPInput is the email form. An empty password keeps the saved one.
type SMTPInput struct {
	Host     string `json:"host" doc:"SMTP server, e.g. smtp.example.com"`
	Port     int    `json:"port,omitempty" doc:"default: 587 with starttls, 465 with tls, 25 with none"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty" doc:"leave empty to keep the saved password"`
	From     string `json:"from" doc:"sender address, e.g. reminders@example.com"`
	TLS      string `json:"tls,omitempty" enum:"starttls,tls,none" doc:"starttls (default), tls (implicit, port 465) or none (local relays)"`
}

// SetSMTP checks the settings by connecting and signing in, then saves
// them.
func (m *Manager) SetSMTP(ctx context.Context, in SMTPInput) error {
	if m.opts.SMTP.Host != "" {
		return fromEnv("email", "EXHALE_SMTP_*")
	}
	cfg, err := email.Config{Host: in.Host, Port: in.Port, Username: in.Username, Password: in.Password, From: in.From, TLS: in.TLS}.Normalize()
	if err != nil {
		return err
	}
	if cfg.Username != "" && cfg.Password == "" {
		// The saved password is only ever sent to the server and user it
		// was saved for.
		old, _ := m.smtp(ctx)
		if old.Password == "" || old.Host != cfg.Host || old.Username != cfg.Username {
			return fmt.Errorf("%w: enter the password", domain.ErrValidation)
		}
		cfg.Password = old.Password
	}
	cfg.RootCAs = m.opts.SMTPRoots
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := email.Check(cctx, cfg); err != nil {
		return invalid(err)
	}
	// The password is stored with the rest, in the settings table next to
	// the Telegram token; it is never marshaled into a response (SMTPStatus
	// has no password field).
	raw, err := json.Marshal(cfg) //nolint:gosec // G117: saving the secret is the point
	if err != nil {
		return err
	}
	if err := m.put(ctx, keySMTP, string(raw)); err != nil {
		return err
	}
	m.log.Info("email configured from the web", "host", cfg.Host, "port", cfg.Port, "tls", cfg.TLS)
	return nil
}

// RemoveSMTP forgets the email settings saved from the web.
func (m *Manager) RemoveSMTP(ctx context.Context) error {
	if m.opts.SMTP.Host != "" {
		return fromEnv("email", "EXHALE_SMTP_*")
	}
	return m.put(ctx, keySMTP, "")
}

// NtfyInput is the ntfy form. An empty token keeps the saved one unless
// RemoveToken is set or the server changes.
type NtfyInput struct {
	Server      string `json:"server,omitempty" doc:"base address of the ntfy server; default https://ntfy.sh"`
	Token       string `json:"token,omitempty" doc:"access token (tk_…), for servers that need one to publish; leave empty to keep the saved one"`
	RemoveToken bool   `json:"removeToken,omitempty" doc:"forget the saved access token"`
}

// cleanServer reduces an address to scheme, host and path.
func cleanServer(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("%w: the server address should look like https://ntfy.example.com", domain.ErrValidation)
	}
	if u.User != nil {
		return "", fmt.Errorf("%w: put the access token in its own field, not in the address", domain.ErrValidation)
	}
	return strings.TrimRight((&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String(), "/"), nil
}

// SetNtfy saves the ntfy server after checking its address against the
// network rules.
func (m *Manager) SetNtfy(ctx context.Context, in NtfyInput) error {
	if m.opts.NtfyServer != "" || m.opts.NtfyToken != "" {
		return fromEnv("ntfy", "EXHALE_NTFY_URL or EXHALE_NTFY_TOKEN")
	}
	if strings.TrimSpace(in.Server) == "" {
		in.Server = ntfy.DefaultServer
	}
	server, err := cleanServer(in.Server)
	if err != nil {
		return err
	}
	if _, err := m.guard.CheckURL(ctx, server); err != nil {
		return invalid(err)
	}
	cfg := ntfy.Config{Server: server, Token: strings.TrimSpace(in.Token)}
	if strings.ContainsAny(cfg.Token, " \t\r\n") {
		return fmt.Errorf("%w: that does not look like an access token; ntfy's look like tk_…", domain.ErrValidation)
	}
	if cfg.Token == "" && !in.RemoveToken {
		// The saved token is only ever sent to the server it was saved for.
		if old, _ := m.ntfy(ctx); old.Server == server {
			cfg.Token = old.Token
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := m.put(ctx, keyNtfy, string(raw)); err != nil {
		return err
	}
	m.log.Info("ntfy configured from the web", "host", netguard.Host(server), "token", cfg.Token != "")
	return nil
}

// RemoveNtfy goes back to the default public server without a token.
func (m *Manager) RemoveNtfy(ctx context.Context) error {
	if m.opts.NtfyServer != "" || m.opts.NtfyToken != "" {
		return fromEnv("ntfy", "EXHALE_NTFY_URL or EXHALE_NTFY_TOKEN")
	}
	return m.put(ctx, keyNtfy, "")
}

// SetAllowPrivate lets webhooks and ntfy reach private-network addresses
// (a server on the LAN), or stops them. Loopback, link-local and cloud
// metadata addresses stay out of reach either way.
func (m *Manager) SetAllowPrivate(ctx context.Context, allow bool) error {
	if m.opts.AllowPrivate != nil {
		return fromEnv("this", "EXHALE_ALLOW_PRIVATE_DESTINATIONS")
	}
	m.log.Info("private destinations setting changed from the web", "allow", allow)
	return m.put(ctx, keyAllowPrivate, fmt.Sprint(allow))
}

// Validate checks a destination a member typed (an email address, an ntfy
// topic, a webhook URL with an optional body template) and returns it in
// the form it is saved in.
func (m *Manager) Validate(ctx context.Context, channel, destination, template string) (string, error) {
	if channel != webhook.Name && strings.TrimSpace(template) != "" {
		return "", fmt.Errorf("%w: only webhooks take a body template", domain.ErrValidation)
	}
	switch channel {
	case email.Name:
		return email.Address(destination)
	case ntfy.Name:
		return ntfy.Topic(destination)
	case webhook.Name:
		destination = strings.TrimSpace(destination)
		if len(destination) > 2048 {
			return "", fmt.Errorf("%w: the address is too long", domain.ErrValidation)
		}
		u, err := m.guard.CheckURL(ctx, destination)
		if err != nil {
			return "", invalid(err)
		}
		if err := webhook.ValidateTemplate(template); err != nil {
			return "", err
		}
		return u.String(), nil
	}
	return "", fmt.Errorf("%w: unknown channel %q; use email, ntfy or webhook", domain.ErrValidation, channel)
}

func templateKey(url string) string {
	sum := sha256.Sum256([]byte(url))
	return keyTemplate + hex.EncodeToString(sum[:16])
}

func (m *Manager) template(ctx context.Context, url string) string {
	return m.setting(ctx, templateKey(url))
}

// SetTemplate saves the body template of a webhook ("" for the default
// body). Call it once the destination is linked to the member.
func (m *Manager) SetTemplate(ctx context.Context, url, template string) error {
	template = strings.TrimSpace(template)
	if template == "" && m.template(ctx, url) == "" {
		return nil
	}
	return m.put(ctx, templateKey(url), template)
}

// Forget clears what is kept about a destination besides its link.
func (m *Manager) Forget(ctx context.Context, channel, destination string) error {
	if channel != webhook.Name {
		return nil
	}
	return m.SetTemplate(ctx, destination, "")
}

// Describe returns how a destination is shown to its member: the address,
// the topic, or only the host of a webhook (the rest of its URL is a
// secret), and whether it has a body template.
func (m *Manager) Describe(ctx context.Context, channel, destination string) (text string, hasTemplate bool) {
	if channel == webhook.Name {
		return netguard.Host(destination), m.template(ctx, destination) != ""
	}
	if channel == webpush.Name {
		return m.deviceName(ctx, destination), false // the endpoint is a secret
	}
	return destination, false
}

// Test sends a sample reminder to a destination now and reports what went
// wrong, if anything.
func (m *Manager) Test(ctx context.Context, channel, destination string) error {
	ch, ok := m.channels[channel]
	if !ok {
		return fmt.Errorf("%w: test messages are for email, ntfy, webhooks and devices", domain.ErrValidation)
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	due := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	_, err := ch.Send(ctx, destination, notify.Test(due))
	if errors.Is(err, actions.ErrGone) {
		return fmt.Errorf("%w: this device stopped accepting notifications and was removed; turn them on again there", domain.ErrNotFound)
	}
	return err
}
