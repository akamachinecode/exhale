// Command exhale is the Exhale server and CLI.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/agents"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/telegram"
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
	Link    linkCmd    `cmd:"" help:"Link a chat account (Telegram) to the household owner."`
	Backup  backupCmd  `cmd:"" help:"Write a consistent backup of the database."`
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
	a, err := e.svc.Bootstrap(ctx, app.BootstrapInput{})
	if err != nil {
		return a, fmt.Errorf("no household yet: run `exhale init` first (%w)", err)
	}
	return a, nil
}

// --- serve ---------------------------------------------------------------------

type serveCmd struct {
	Listen        string `env:"EXHALE_LISTEN" default:"127.0.0.1:8484" help:"Address to listen on (use 0.0.0.0:8484 in Docker)."`
	TelegramToken string `name:"telegram-token" env:"EXHALE_TELEGRAM_TOKEN" help:"Bot token from @BotFather. Enables Telegram reminders."`
	TelegramAPI   string `name:"telegram-api" env:"EXHALE_TELEGRAM_API" hidden:"" help:"Bot API base URL (tests)."`
	TrustLocal    bool   `name:"trust-local" env:"EXHALE_TRUST_LOCAL" default:"true" negatable:"" help:"Let requests from this machine act as the owner without a token."`
	Dev           bool   `help:"Development mode."`
}

func (s *serveCmd) Run(ctx context.Context, g *globals) error {
	e, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer e.st.Close()
	if _, err := owner(ctx, e); err != nil {
		e.log.Warn("household not initialized; the API will answer 503 until you run `exhale init`")
	}
	handler, _, err := httpapi.New(e.svc, httpapi.Config{Version: version, TrustLocal: s.TrustLocal, Log: e.log})
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
	var channels []actions.Channel
	if s.TelegramToken != "" {
		tg, err := telegram.New(s.TelegramToken, s.TelegramAPI, e.svc, e.log)
		if err != nil {
			return err
		}
		channels = append(channels, tg)
		services = append(services, tg)
		e.log.Info("telegram enabled")
	} else {
		e.log.Info("telegram disabled: set EXHALE_TELEGRAM_TOKEN to enable reminders in Telegram")
	}
	eng := actions.New(e.svc, e.log, channels...)
	for _, c := range channels {
		if tg, ok := c.(*telegram.Adapter); ok {
			tg.SetEngine(eng)
		}
	}
	services = append(services,
		scheduler.New(e.svc, e.log, eng),
		platform.Func{Name: "message-sync", Fn: eng.Sync},
	)
	return platform.Run(ctx, e.log, services...)
}

// --- init ----------------------------------------------------------------------

type initCmd struct {
	Name     string `default:"Home" help:"Household name."`
	Owner    string `required:"" help:"Your name."`
	Email    string `help:"Your email (optional)."`
	Currency string `default:"EUR" help:"Main currency (ISO 4217)."`
	Timezone string `default:"Local" help:"IANA time zone, e.g. Europe/Madrid."`
	Locale   string `default:"en" help:"Language for messages: en or es."`
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
	Channel string `arg:"" enum:"telegram" help:"Channel to link."`
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
	fmt.Printf("Send this message to your Telegram bot within 10 minutes:\n\n  /start %s\n\nor open https://t.me/<your_bot>?start=%s\n", code, code)
	return nil
}

// --- backup / openapi / version --------------------------------------------------

type backupCmd struct{}

func (backupCmd) Run(ctx context.Context, g *globals) error {
	e, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer e.st.Close()
	p, err := e.st.Backup(ctx, "manual")
	if err != nil {
		return err
	}
	fmt.Println(p)
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
