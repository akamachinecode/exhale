// Command exhale is the Exhale server and CLI.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/agents"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/assistant"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/awake"
	"github.com/akamachinecode/exhale/internal/backup"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/email"
	"github.com/akamachinecode/exhale/internal/channels/notifiers"
	"github.com/akamachinecode/exhale/internal/channels/slack"
	"github.com/akamachinecode/exhale/internal/channels/telegram"
	"github.com/akamachinecode/exhale/internal/fx"
	"github.com/akamachinecode/exhale/internal/httpapi"
	"github.com/akamachinecode/exhale/internal/platform"
	"github.com/akamachinecode/exhale/internal/scheduler"
	"github.com/akamachinecode/exhale/internal/store"
)

// version is set at build time by goreleaser.
var version = "dev"

type globals struct {
	DataDir   string `name:"data-dir" env:"EXHALE_DATA_DIR" help:"Directory for the database, logos and backups." type:"path"`
	LogLevel  string `name:"log-level" env:"EXHALE_LOG_LEVEL" default:"info" enum:"debug,info,warn,error" help:"Log level."`
	LogFormat string `name:"log-format" env:"EXHALE_LOG_FORMAT" default:"text" enum:"text,json" help:"Log format."`
}

type cli struct {
	globals
	Serve   serveCmd   `cmd:"" default:"withargs" help:"Run the server (web UI, API, MCP, reminders)."`
	Init    initCmd    `cmd:"" help:"Create the household on first run."`
	Mcp     mcpCmd     `cmd:"" help:"Serve MCP over stdio, for local AI clients (Claude Desktop, Claude Code, Cursor…)."`
	Token   tokenCmd   `cmd:"" help:"Manage personal access tokens for scripts and AI agents."`
	Link    linkCmd    `cmd:"" help:"Link a chat account (Telegram, Slack) to the household owner."`
	User    userCmd    `cmd:"" help:"Manage web accounts (password recovery)."`
	Setup   setupCmd   `cmd:"" name:"setup-code" help:"Print a new one-time code for the web setup wizard."`
	Backup  backupCmd  `cmd:"" help:"Write a consistent backup of the database (safe while the server runs)."`
	Restore restoreCmd `cmd:"" help:"Put a backup back as the database. Stop the server first."`
	Openapi openapiCmd `cmd:"" help:"Print the OpenAPI document."`
	Version versionCmd `cmd:"" help:"Print the version."`
}

func main() {
	var c cli
	kctx := kong.Parse(&c, kong.Name("exhale"), kong.Description("Exhale — breathe out, your subscriptions are handled."), kong.UsageOnError())
	if c.DataDir == "" {
		c.DataDir = platform.DefaultDataDir()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	kctx.BindTo(ctx, (*context.Context)(nil))
	kctx.FatalIfErrorf(kctx.Run(&c.globals))
}

type env struct {
	log *slog.Logger
	st  *store.Store
	svc *app.Service
}

func open(ctx context.Context, g *globals) (*env, error) {
	log := platform.NewLogger(os.Stderr, g.LogLevel, g.LogFormat)
	st, err := store.Open(ctx, g.DataDir, log)
	if err != nil {
		return nil, err
	}
	return &env{log: log, st: st, svc: app.New(st, bus.New(), log, nil)}, nil
}

func owner(ctx context.Context, e *env) (app.Actor, error) {
	a, err := e.svc.Owner(ctx)
	if err != nil {
		return a, fmt.Errorf("no household yet: open the web UI to set it up, or run `exhale init` (%w)", err)
	}
	return a, nil
}

// --- serve ---------------------------------------------------------------------

type serveCmd struct {
	Listen        string `env:"EXHALE_LISTEN" default:"127.0.0.1:8484" help:"Address to listen on (use 0.0.0.0:8484 in Docker)."`
	TelegramToken string `name:"telegram-token" env:"EXHALE_TELEGRAM_TOKEN" help:"Bot token from @BotFather. Optional: usually set from the web (Settings → Telegram)."`
	TelegramAPI   string `name:"telegram-api" env:"EXHALE_TELEGRAM_API" hidden:"" help:"Bot API base URL (tests)."`
	PublicURL     string `name:"public-url" env:"EXHALE_PUBLIC_URL" help:"Address people open Exhale at, e.g. https://exhale.example.com. Emails, ntfy and webhooks link to it, and single sign-on from another machine needs it."`
	SlackBotToken string `name:"slack-bot-token" env:"EXHALE_SLACK_BOT_TOKEN" help:"Slack bot token (xoxb-…). Optional: usually set from the web (Settings → Slack)."`
	SlackAppToken string `name:"slack-app-token" env:"EXHALE_SLACK_APP_TOKEN" help:"Slack app-level token (xapp-…) for Socket Mode. Needed with --slack-bot-token."`
	SlackAPI      string `name:"slack-api" env:"EXHALE_SLACK_API" hidden:"" help:"Slack Web API base URL (tests)."`
	TrustLocal    bool   `name:"trust-local" env:"EXHALE_TRUST_LOCAL" help:"Let requests from this machine act as the owner without signing in (development, scripts)."`
	SecureCookies bool   `name:"secure-cookies" env:"EXHALE_SECURE_COOKIES" help:"Mark the session cookie Secure. Set it when Exhale is served over HTTPS."`
	BackupDir     string `name:"backup-dir" env:"EXHALE_BACKUP_DIR" type:"path" help:"Directory for the daily automatic backups (default: <data dir>/backups)."`
	BackupKeep    int    `name:"backup-keep" env:"EXHALE_BACKUP_KEEP" default:"14" help:"Automatic backups to keep; older ones are deleted. 0 keeps all."`
	NoBackups     bool   `name:"no-backups" env:"EXHALE_NO_BACKUPS" help:"Do not back up the database automatically."`
	// The built-in assistant is off unless a provider is set here or in the
	// web (Settings → Built-in assistant). Values set here win.
	AssistantProvider string `name:"assistant-provider" env:"EXHALE_ASSISTANT_PROVIDER" enum:",anthropic,openai" default:"" help:"Model provider of the built-in assistant, anthropic or openai (any OpenAI-compatible endpoint). Optional, usually set from the web."`
	AssistantAPIKey   string `name:"assistant-api-key" env:"EXHALE_ASSISTANT_API_KEY" help:"API key for the assistant's provider."`
	AssistantModel    string `name:"assistant-model" env:"EXHALE_ASSISTANT_MODEL" help:"Model id for the assistant."`
	AssistantBaseURL  string `name:"assistant-base-url" env:"EXHALE_ASSISTANT_BASE_URL" help:"Address of the assistant's endpoint, for example http://localhost:11434/v1 for Ollama."`
	Dev               bool   `help:"Development mode."`
	oidcFlags

	// Email, ntfy and webhooks. All optional: owners usually set them in
	// the web (Settings → Other channels). Set here, they win and the web
	// shows them as read-only.
	SMTPHost     string `name:"smtp-host" env:"EXHALE_SMTP_HOST" group:"Other channels" help:"SMTP server for email reminders."`
	SMTPPort     int    `name:"smtp-port" env:"EXHALE_SMTP_PORT" group:"Other channels" help:"SMTP port (default: 587 with starttls, 465 with tls, 25 with none)."`
	SMTPUsername string `name:"smtp-username" env:"EXHALE_SMTP_USERNAME" group:"Other channels" help:"SMTP user name."`
	SMTPPassword string `name:"smtp-password" env:"EXHALE_SMTP_PASSWORD" group:"Other channels" help:"SMTP password (prefer the environment variable)."`
	SMTPFrom     string `name:"smtp-from" env:"EXHALE_SMTP_FROM" group:"Other channels" help:"Sender address of the emails."`
	SMTPTLS      string `name:"smtp-tls" env:"EXHALE_SMTP_TLS" group:"Other channels" default:"starttls" enum:"starttls,tls,none" help:"starttls, tls (implicit, port 465) or none (local relays)."`
	NtfyURL      string `name:"ntfy-url" env:"EXHALE_NTFY_URL" group:"Other channels" help:"ntfy server (default https://ntfy.sh)."`
	NtfyToken    string `name:"ntfy-token" env:"EXHALE_NTFY_TOKEN" group:"Other channels" help:"ntfy access token, for servers that need one to publish."`
	// A pointer: unset leaves the choice to the owner in the web.
	AllowPrivateDestinations *bool `name:"allow-private-destinations" env:"EXHALE_ALLOW_PRIVATE_DESTINATIONS" negatable:"" group:"Other channels" help:"Let webhooks and ntfy reach private-network addresses (a server on your LAN). Loopback and cloud metadata addresses stay blocked."`
}

func (s *serveCmd) Run(ctx context.Context, g *globals) error {
	e, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer e.st.Close()
	if req, err := auth.SetupRequired(ctx, e.st); err == nil && req {
		if auth.HasSetupCode(ctx, e.st) {
			e.log.Warn("no account yet: open the web UI to create yours", "url", "http://"+s.Listen,
				"note", "the setup code printed earlier still works; `exhale setup-code` prints a new one")
		} else {
			code, err := auth.NewSetupCode(ctx, e.st)
			if err != nil {
				return err
			}
			e.log.Warn("no account yet: open the web UI to create yours", "url", "http://"+s.Listen, "setup_code", code,
				"note", "only asked when the wizard is opened from another machine; `exhale setup-code` prints a new one")
		}
	}
	sso, err := s.provider(ctx, e.log, s.PublicURL)
	if err != nil {
		return err
	}
	tg := telegram.NewManager(s.TelegramToken, s.TelegramAPI, e.svc, e.log)
	// Email, ntfy and webhooks only notify; Telegram stays the channel
	// with buttons (ADR 0007).
	others, err := notifiers.New(e.st, notifiers.Options{
		PublicURL:    s.PublicURL,
		SMTP:         email.Config{Host: s.SMTPHost, Port: s.SMTPPort, Username: s.SMTPUsername, Password: s.SMTPPassword, From: s.SMTPFrom, TLS: s.SMTPTLS},
		NtfyServer:   s.NtfyURL,
		NtfyToken:    s.NtfyToken,
		AllowPrivate: s.AllowPrivateDestinations,
		Version:      version,
	}, e.log)
	if err != nil {
		return err
	}
	sl := slack.NewManager(s.SlackBotToken, s.SlackAppToken, s.SlackAPI, e.svc, e.log)
	asst, err := assistant.New(e.svc, e.log, assistant.Env{Provider: s.AssistantProvider, APIKey: s.AssistantAPIKey, Model: s.AssistantModel, BaseURL: s.AssistantBaseURL})
	if err != nil {
		return err
	}
	// Keeps this computer from sleeping when the owner asks (ADR 0028).
	keepAwake := app.NewKeepAwake(e.svc, awake.New(), e.log)
	handler, _, err := httpapi.New(e.svc, httpapi.Config{
		Version: version, TrustLocal: s.TrustLocal, SecureCookies: s.SecureCookies, Log: e.log, Telegram: tg, Slack: sl, Channels: others, Assistant: asst,
		PublicURL: s.PublicURL, OIDC: sso, DisablePasswordLogin: s.DisablePasswordLogin,
	})
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: s.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	httpSvc := platform.Func{Name: "http", Fn: func(ctx context.Context) error {
		ln, err := net.Listen("tcp", s.Listen)
		if err != nil {
			return err
		}
		e.log.Info("exhale is listening", "url", "http://"+ln.Addr().String(), "version", version, "data", g.DataDir)
		errc := make(chan error, 1)
		go func() { errc <- srv.Serve(ln) }()
		select {
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return srv.Shutdown(sctx)
		case err := <-errc:
			return err
		}
	}}
	services := []platform.Service{httpSvc}
	// The bot comes from EXHALE_TELEGRAM_TOKEN or from the web (Settings →
	// Telegram); the manager starts and swaps it at runtime.
	// The Slack app works the same way (Settings → Slack, or
	// EXHALE_SLACK_BOT_TOKEN and EXHALE_SLACK_APP_TOKEN).
	services = append(services, tg, sl)
	eng := actions.New(e.svc, e.log, append([]actions.Channel{tg, sl}, others.Channels()...)...)
	eng.SetAssistant(asst) // free text in chat (ADR 0018, part B)
	eng.SetPublicURL(s.PublicURL) // links from chat to the web (import review)
	tg.SetEngine(eng)
	others.SetPushActions(eng) // buttons of push notifications (ADR 0026)
	sl.SetEngine(eng)
	services = append(services,
		&fx.Updater{St: e.st, Log: e.log},
		// Slack also posts reminders of shared subscriptions to the team channel.
		scheduler.New(e.svc, e.log, sl.Deliverer(eng)),
		platform.Func{Name: "message-sync", Fn: eng.Sync},
		keepAwake,
	)
	if !s.NoBackups {
		services = append(services, &backup.Service{DB: e.st.DB, Dir: backupDir(g, s.BackupDir), Keep: s.BackupKeep, Log: e.log})
	}
	return platform.Run(ctx, e.log, services...)
}

// --- init ----------------------------------------------------------------------

type initCmd struct {
	Name     string `default:"Home" help:"Household name."`
	Owner    string `required:"" help:"Your name."`
	Email    string `help:"Your email (optional)."`
	Currency string `default:"EUR" help:"Main currency (ISO 4217)."`
	Timezone string `default:"Local" help:"IANA time zone, e.g. Europe/Madrid."`
	Locale   string `default:"en" help:"Language for messages: a registered code such as en or es."`
}

func (c *initCmd) Run(ctx context.Context, g *globals) error {
	e, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer e.st.Close()
	tz := c.Timezone
	if tz == "Local" {
		tz = localZone()
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("unknown time zone %q", tz)
	}
	a, err := e.svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: c.Name, OwnerName: c.Owner, OwnerEmail: c.Email, Currency: c.Currency, Timezone: tz, Locale: c.Locale})
	if err != nil {
		return err
	}
	fmt.Printf("Household ready. Owner: %s (%s), time zone %s.\n", a.Label, a.MemberID, tz)
	return nil
}

func localZone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	if p, err := os.Readlink("/etc/localtime"); err == nil {
		for i := 0; i+len("zoneinfo/") <= len(p); i++ {
			if p[i:i+len("zoneinfo/")] == "zoneinfo/" {
				return p[i+len("zoneinfo/"):]
			}
		}
	}
	return "UTC"
}

// --- mcp -------------------------------------------------------------------------

type mcpCmd struct {
	Scope string `default:"write" enum:"read,write,trusted" help:"What the local AI client may do."`
}

func (m *mcpCmd) Run(ctx context.Context, g *globals) error {
	g.LogFormat = "json" // stdout is the MCP channel; logs go to stderr
	e, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer e.st.Close()
	o, err := owner(ctx, e)
	if err != nil {
		return err
	}
	srv, err := agents.NewServer(e.svc, version, func(_ context.Context, _ *mcp.RequestExtra, session *mcp.ServerSession) (app.Actor, error) {
		a := o
		a.Kind, a.Scope = app.ActorAgent, app.Scope(m.Scope)
		a.Label = "local MCP client"
		if name := agents.ClientName(session); name != "" {
			a.Label = name + " (MCP)"
		}
		return a, nil
	})
	if err != nil {
		return err
	}
	return srv.Run(ctx, &mcp.StdioTransport{})
}

// --- token -----------------------------------------------------------------------

type tokenCmd struct {
	Create tokenCreateCmd `cmd:"" help:"Create a token (shown once)."`
}

type tokenCreateCmd struct {
	Name  string `required:"" help:"Who will use it, e.g. \"Claude Desktop\" or \"Home Assistant\"."`
	Scope string `default:"write" enum:"read,write,trusted" help:"read: only read; write: change with confirmation for destructive actions; trusted: no confirmation."`
}

func (t *tokenCreateCmd) Run(ctx context.Context, g *globals) error {
	e, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer e.st.Close()
	o, err := owner(ctx, e)
	if err != nil {
		return err
	}
	tok, err := auth.CreateToken(ctx, e.st, o.MemberID, t.Name, app.Scope(t.Scope))
	if err != nil {
		return err
	}
	fmt.Println(tok)
	fmt.Fprintln(os.Stderr, "Store it now: it will not be shown again. Use it as: Authorization: Bearer <token>")
	return nil
}

// --- link ------------------------------------------------------------------------

type linkCmd struct {
	Channel string `arg:"" enum:"telegram,slack" help:"Channel to link."`
}

func (l *linkCmd) Run(ctx context.Context, g *globals) error {
	e, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer e.st.Close()
	o, err := owner(ctx, e)
	if err != nil {
		return err
	}
	code, err := auth.CreateLinkCode(ctx, e.st, o.MemberID, l.Channel)
	if err != nil {
		return err
	}
	if l.Channel == slack.Name {
		fmt.Printf("Type this in Slack within 10 minutes, anywhere:\n\n  /exhale link %s\n\nor send the code to the Exhale app in a direct message.\n", code)
		return nil
	}
	fmt.Printf("Send this message to your Telegram bot within 10 minutes:\n\n  /start %s\n\nor open https://t.me/<your_bot>?start=%s\n", code, code)
	return nil
}

// --- user / setup-code -------------------------------------------------------------

type userCmd struct {
	ResetPassword userResetCmd    `cmd:"" help:"Set a new password for an account and sign it out everywhere."`
	Reset2FA      userReset2FACmd `cmd:"" name:"reset-2fa" help:"Turn off two-factor sign-in for an account that lost its phone and backup codes."`
}

type userResetCmd struct {
	Email string `required:"" help:"Email of the account."`
}

func (u *userResetCmd) Run(ctx context.Context, g *globals) error {
	e, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer e.st.Close()
	email, err := auth.NormalizeEmail(u.Email)
	if err != nil {
		return err
	}
	m, err := e.st.Q.MemberByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("no account with email %s", email)
	}
	pw, err := readPassword("New password: ")
	if err != nil {
		return err
	}
	if again, err := readPassword("Repeat it: "); err != nil || again != pw {
		return errors.New("the passwords do not match")
	}
	if err := auth.SetPassword(ctx, e.st, m.ID, pw); err != nil {
		return err
	}
	fmt.Printf("Password updated for %s. Every session of this account was signed out.\n", m.Name)
	return nil
}

type userReset2FACmd struct {
	Email string `required:"" help:"Email of the account."`
}

func (u *userReset2FACmd) Run(ctx context.Context, g *globals) error {
	e, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer e.st.Close()
	email, err := auth.NormalizeEmail(u.Email)
	if err != nil {
		return err
	}
	m, err := e.st.Q.MemberByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("no account with email %s", email)
	}
	was, err := auth.ResetTwoFactorFromServer(ctx, e.st, m.ID)
	if err != nil {
		return err
	}
	if !was {
		fmt.Printf("%s does not use two-factor sign-in. Nothing changed.\n", m.Name)
		return nil
	}
	fmt.Printf("Two-factor sign-in is off for %s: the password alone signs in now. They can turn it on again in Settings.\n", m.Name)
	return nil
}

func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

type setupCmd struct{}

func (setupCmd) Run(ctx context.Context, g *globals) error {
	e, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer e.st.Close()
	if req, err := auth.SetupRequired(ctx, e.st); err != nil || !req {
		return errors.New("setup is already done: sign in, or use `exhale user reset-password`")
	}
	code, err := auth.NewSetupCode(ctx, e.st)
	if err != nil {
		return err
	}
	fmt.Println(code)
	fmt.Fprintln(os.Stderr, "Enter it in the web setup wizard within 24 hours. Earlier codes no longer work.")
	return nil
}

// --- backup / restore / openapi / version ----------------------------------------

// backupDir is where backups go: dir if set, else <data dir>/backups.
func backupDir(g *globals, dir string) string {
	if dir != "" {
		return dir
	}
	return filepath.Join(g.DataDir, "backups")
}

type backupCmd struct {
	Out string `name:"out" env:"EXHALE_BACKUP_DIR" type:"path" help:"Directory to write the backup to (default: <data dir>/backups)."`
}

func (b *backupCmd) Run(ctx context.Context, g *globals) error {
	e, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer e.st.Close()
	p, err := backup.Snapshot(ctx, e.st.DB, backupDir(g, b.Out), backup.KindManual, time.Now())
	if err != nil {
		return err
	}
	fmt.Println(p)
	return nil
}

type restoreCmd struct {
	File  string `arg:"" type:"existingfile" help:"Backup file to restore (made by Exhale: auto-*.db, manual-*.db, pre-migrate-*.db)."`
	Force bool   `help:"Replace the database that is already in the data directory. It is kept next to the new one, renamed."`
}

// Run never opens the store: restoring must not migrate or touch a database
// that a running server is using.
func (r *restoreCmd) Run(ctx context.Context, g *globals) error {
	dst, replaced, err := backup.Restore(ctx, r.File, g.DataDir, r.Force, time.Now())
	switch {
	case errors.Is(err, backup.ErrExists):
		return fmt.Errorf("%s already has a database. Stop Exhale and run the command again with --force: the current database is kept, renamed", g.DataDir)
	case errors.Is(err, backup.ErrInUse):
		return fmt.Errorf("the database in %s is in use. Stop Exhale first, then restore", g.DataDir)
	case err != nil:
		return err
	}
	fmt.Printf("Restored %s to %s.\n", r.File, dst)
	if replaced != "" {
		fmt.Printf("The previous database was kept as %s; delete it once you are sure.\n", replaced)
	}
	fmt.Println("Start Exhale again.")
	return nil
}

type openapiCmd struct{}

func (openapiCmd) Run(ctx context.Context, _ *globals) error {
	st, err := store.OpenMemory(ctx)
	if err != nil {
		return err
	}
	defer st.Close()
	_, api, err := httpapi.New(app.New(st, bus.New(), nil, nil), httpapi.Config{Version: version})
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

type versionCmd struct{}

func (versionCmd) Run() error { fmt.Println("exhale", version); return nil }
