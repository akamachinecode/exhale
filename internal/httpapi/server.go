// Package httpapi (piece ①) serves the REST API (huma, OpenAPI 3.1), live
// events (SSE), the MCP endpoint, health checks and the embedded web UI.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akamachinecode/exhale/internal/agents"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/assistant"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/channels/notifiers"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/oauth"
	"github.com/akamachinecode/exhale/web"
)

// Config configures the HTTP server.
type Config struct {
	Version string
	// TrustLocal lets requests from this machine act as the household owner
	// without signing in (development and scripts). Off by default since web
	// sign-in shipped.
	TrustLocal bool
	// SecureCookies marks the session cookie Secure; set it when Exhale is
	// served over HTTPS (behind a reverse proxy).
	SecureCookies bool
	// Telegram configures and reports the household's bot (nil when not
	// available, e.g. in tests).
	Telegram TelegramControl
	// Channels runs the notify-only channels: email, ntfy and webhooks
	// (nil when not available, e.g. when exporting the OpenAPI document).
	Channels *notifiers.Manager
	// Slack configures and reports the team's Slack app (nil when not
	// available, e.g. in tests).
	Slack SlackControl
	// Assistant is the model Exhale itself uses (ADR 0018, part B). When
	// nil, one is made that is off until an owner sets it up in the web.
	Assistant *assistant.Assistant
	Log       *slog.Logger
	// PublicURL is the address people reach Exhale at, e.g.
	// https://exhale.example.com. Single sign-on builds its redirect URI
	// from it; without it only this machine's own address works.
	PublicURL string
	// OIDC enables sign-in with an OpenID Connect provider (nil: off).
	OIDC *auth.OIDC
	// DisablePasswordLogin turns password sign-in off. Only honoured when
	// OIDC is set.
	DisablePasswordLogin bool
	// OAuth is the authorization server of remote MCP (ADR 0024). When nil
	// one is made over the service's store; tests pass their own clock.
	OAuth *oauth.Service
}

type ctxKey struct{}

func actorFrom(ctx context.Context) (app.Actor, bool) {
	a, ok := ctx.Value(ctxKey{}).(app.Actor)
	return a, ok
}

// New returns the HTTP handler and the huma API (for exporting OpenAPI).
func New(svc *app.Service, cfg Config) (http.Handler, huma.API, error) {
	mux := http.NewServeMux()
	hc := huma.DefaultConfig("Exhale API", cfg.Version)
	hc.Info.Description = "Self-hosted subscription manager. Every operation is also available to AI agents over MCP at /mcp."
	hc.OpenAPIPath, hc.DocsPath, hc.SchemasPath = "/api/openapi", "/api/docs", "/api/schemas"
	hc.Components.SecuritySchemes = map[string]*huma.SecurityScheme{"bearer": {Type: "http", Scheme: "bearer", Description: "Personal access token (exhale_pat_…)"}}
	// A domain.Text travels as its web form: a key and arguments.
	hc.Components.Schemas.RegisterTypeAlias(reflect.TypeFor[domain.Text](), reflect.TypeFor[domain.Wire]())
	api := humago.New(mux, hc)
	registerAll(api, svc)
	if cfg.Assistant == nil {
		a, err := assistant.New(svc, cfg.Log, assistant.Env{})
		if err != nil {
			return nil, nil, err
		}
		cfg.Assistant = a
	}
	sm := auth.NewSessions(svc.Store(), cfg.SecureCookies)
	aa := &authAPI{svc: svc, sm: sm, cfg: cfg}
	registerAuth(api, aa)
	registerSettings(api, aa)
	registerSlack(api, aa)
	registerAssistant(api, aa)
	mux.HandleFunc("POST /api/v1/assistant/messages/stream", assistantStream(aa))
	registerData(api, mux, svc, cfg.Assistant)
	registerFiles(api, mux, svc, cfg.Assistant)
	registerMembers(api, aa)
	if err := registerOIDC(mux, aa); err != nil {
		return nil, nil, err
	}
	registerChannels(api, aa)
	registerTwoFactor(api, aa)
	oa := newOAuthAPI(aa)
	registerOAuth(api, mux, oa)
	registerPush(api, aa)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		// The desktop app compares it with its own to restart an older
		// server (desktop.VersionHeader).
		w.Header().Set("X-Exhale-Version", cfg.Version)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/v1/events", events(svc))

	mcpSrv, err := agents.NewServer(svc, cfg.Version, func(ctx context.Context, extra *mcp.RequestExtra, session *mcp.ServerSession) (app.Actor, error) {
		a, ok := actorFrom(ctx)
		if !ok {
			return app.Actor{}, auth.ErrUnauthorized
		}
		// An OAuth connection is already named after its application.
		if name := agents.ClientName(session); name != "" && a.Kind == app.ActorAgent && !viaOAuth(ctx) {
			a.Label = name + " (" + a.Label + ")"
		}
		return a, nil
	})
	if err != nil {
		return nil, nil, err
	}
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil))
	mux.Handle("/", spa())

	// CSRF: browsers' cross-origin writes are rejected (Sec-Fetch-Site and
	// Origin checks); the session cookie is also SameSite=Lax.
	csrf := http.NewCrossOriginProtection()
	// The OAuth endpoints use no cookies, and browser-based MCP clients
	// call them from another origin.
	for _, p := range []string{oauthRegister, oauthToken, oauthRevoke} {
		csrf.AddInsecureBypassPattern("POST " + p)
	}
	return csrf.Handler(sm.LoadAndSave(authenticate(svc, sm, cfg, oa, mux))), api, nil
}

type oauthKey struct{}

// viaOAuth reports whether the request came with an OAuth access token.
func viaOAuth(ctx context.Context) bool { v, _ := ctx.Value(oauthKey{}).(bool); return v }

// authenticate resolves the actor of every API and MCP request: an API
// token (agents, scripts) or, at /mcp only, an OAuth access token (remote
// assistants), else the web session, else (with TrustLocal) the owner for
// requests from this machine.
func authenticate(svc *app.Service, sm *scs.SessionManager, cfg Config, oa *oauthAPI, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), localKey{}, isLoopback(r))
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ctx = context.WithValue(ctx, ipKey{}, host)
		r = r.WithContext(ctx)
		isMCP := r.URL.Path == mcpPath
		protected := strings.HasPrefix(r.URL.Path, "/api/v1") || isMCP
		if !protected {
			next.ServeHTTP(w, r)
			return
		}
		var actor app.Actor
		ok := false
		if tok := auth.BearerToken(r.Header); tok != "" {
			var a app.Actor
			var err error
			switch {
			case strings.HasPrefix(tok, oauth.AccessPrefix) && isMCP:
				a, err = oa.accessActor(r, tok)
				ctx = context.WithValue(ctx, oauthKey{}, true)
			case strings.HasPrefix(tok, oauth.AccessPrefix):
				// Its audience is the MCP endpoint (RFC 8707), nothing else.
				err = errors.New("an OAuth token only works at /mcp")
			default:
				a, err = auth.ResolveToken(ctx, svc.Store(), tok)
			}
			if err != nil {
				if isMCP {
					w.Header().Set("WWW-Authenticate", oa.challenge(r, "invalid_token"))
				}
				writeProblem(w, http.StatusUnauthorized, "invalid token")
				return
			}
			actor, ok = a, true
		} else if a, found := auth.SessionActor(ctx, sm, svc.Store()); found {
			actor, ok = a, true
		} else if cfg.TrustLocal && isLoopback(r) {
			if a, err := svc.Owner(ctx); err == nil {
				actor, ok = a, true
			}
		}
		if ok {
			r = r.WithContext(context.WithValue(ctx, ctxKey{}, actor))
		} else if !publicPath(r.URL.Path) {
			if isMCP {
				w.Header().Set("WWW-Authenticate", oa.challenge(r, ""))
			}
			writeProblem(w, http.StatusUnauthorized, "sign in or send a bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-For") == ""
}

// writeJSONProblem writes a problem outside huma, with its text.
func writeJSONProblem(w http.ResponseWriter, p *problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"status": status, "title": http.StatusText(status), "detail": detail})
}

type resp[T any] struct{ Body T }

func registerAll(api huma.API, svc *app.Service) {
	register(api, svc, app.OpGetContext)
	register(api, svc, app.OpGetHousehold)
	register(api, svc, app.OpUpdateHousehold)
	register(api, svc, app.OpListSubscriptions)
	register(api, svc, app.OpGetSubscription)
	register(api, svc, app.OpCreateSubscription)
	register(api, svc, app.OpUpdateSubscription)
	register(api, svc, app.OpSetSubscriptionStatus)
	register(api, svc, app.OpDeleteSubscription)
	register(api, svc, app.OpListCharges)
	register(api, svc, app.OpCalendar)
	register(api, svc, app.OpNeedsAttention)
	register(api, svc, app.OpChangeChargeDate)
	register(api, svc, app.OpChangeChargeAmount)
	register(api, svc, app.OpSetChargeStatus)
	register(api, svc, app.OpDecideRenewal)
	register(api, svc, app.OpPreviewSchedule)
	register(api, svc, app.OpSummary)
	register(api, svc, app.OpSpendHistory)
	register(api, svc, app.OpBalances)
	register(api, svc, app.OpAudit)
	register(api, svc, app.OpSavePaymentMethod)
	register(api, svc, app.OpReassignPaymentMethod)
	register(api, svc, app.OpDeletePaymentMethod)
	register(api, svc, app.OpUpdateCategory)
	register(api, svc, app.OpDeleteCategory)
	register(api, svc, app.OpExport)
	register(api, svc, app.OpImport)
	register(api, svc, app.OpReminders)
	register(api, svc, app.OpCreateCategory)
	register(api, svc, app.OpListAudit)
	register(api, svc, app.OpUndo)
	register(api, svc, app.OpBulkUpdate)
	register(api, svc, app.OpRenameTag)
	register(api, svc, app.OpDeleteTag)
	register(api, svc, app.OpSnooze)
	register(api, svc, app.OpDismissDuplicate)
	register(api, svc, app.OpListDocuments)
	register(api, svc, app.OpDeleteDocument)
	register(api, svc, app.OpRemoveLogo)
	register(api, svc, app.OpMatchStatement)
	register(api, svc, app.OpGetKeepAwake)
	register(api, svc, app.OpSetKeepAwake)
	register(api, svc, app.OpKeepAwakeSession)
	register(api, svc, app.OpSetupLidClosed)
	register(api, svc, app.OpGetUpdate)
	register(api, svc, app.OpApplyUpdate)
	register(api, svc, app.OpGlance)
}

func operation(m app.Meta) huma.Operation {
	return huma.Operation{
		OperationID: m.Name, Method: m.Method, Path: "/api/v1" + m.Path, Summary: m.Title,
		Description: m.Description, Tags: []string{m.Tag}, Security: []map[string][]string{{"bearer": {}}},
	}
}

// register exposes one use case. GET operations read their input from path
// and query parameters; the others from a JSON body.
func register[In, Out any](api huma.API, svc *app.Service, op app.Op[In, Out]) {
	run := func(ctx context.Context, in In) (*resp[Out], error) {
		actor, ok := actorFrom(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("missing credentials")
		}
		out, err := op.Run(svc, ctx, actor, in)
		if err != nil {
			return nil, httpError(err)
		}
		return &resp[Out]{Body: out}, nil
	}
	if op.Method == http.MethodGet {
		huma.Register(api, operation(op.Meta), func(ctx context.Context, in *In) (*resp[Out], error) { return run(ctx, *in) })
		return
	}
	huma.Register(api, operation(op.Meta), func(ctx context.Context, in *struct{ Body In }) (*resp[Out], error) { return run(ctx, in.Body) })
}

func httpError(err error) error {
	status := 0
	var sentinel error
	switch {
	case errors.Is(err, domain.ErrValidation):
		status, sentinel = http.StatusUnprocessableEntity, domain.ErrValidation
	case errors.Is(err, domain.ErrNotFound):
		status, sentinel = http.StatusNotFound, domain.ErrNotFound
	case errors.Is(err, domain.ErrConflict):
		status, sentinel = http.StatusConflict, domain.ErrConflict
	case errors.Is(err, app.ErrForbidden):
		status, sentinel = http.StatusForbidden, app.ErrForbidden
	default:
		return huma.Error500InternalServerError("internal error", err)
	}
	if t, ok := domain.TextOf(err); ok {
		return newProblem(status, t)
	}
	return huma.NewError(status, humanize(err, sentinel))
}

// problem is an RFC 9457 problem whose sentence the web can translate:
// detail is English, text the same sentence as a key and its arguments
// (web/src/locales/server).
type problem struct {
	huma.ErrorModel
	Text *domain.Text `json:"text,omitempty" doc:"the detail as a translatable text: key and arguments"`
}

func newProblem(status int, t domain.Text) *problem {
	return &problem{ErrorModel: huma.ErrorModel{Status: status, Title: http.StatusText(status), Detail: domain.Capitalize(t.String())}, Text: &t}
}

// humanize drops the sentinel prefix ("validation failed: …") so the detail
// reads as a sentence people can be shown.
func humanize(err, sentinel error) string {
	msg := strings.TrimPrefix(err.Error(), sentinel.Error()+": ")
	if msg == "" {
		return err.Error()
	}
	return strings.ToUpper(msg[:1]) + msg[1:]
}

// events streams bus events of the caller's household as Server-Sent Events.
// Clients refetch everything on reconnect, since events may be missed
// (ADR 0013).
func events(svc *app.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFrom(r.Context())
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "missing credentials")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeProblem(w, http.StatusInternalServerError, "streaming unsupported")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		ch, stop := svc.Bus().Subscribe(64)
		defer stop()
		fmt.Fprint(w, "retry: 3000\nevent: ready\ndata: {}\n\n")
		flusher.Flush()
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ping.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case e, ok := <-ch:
				if !ok {
					return
				}
				if e.HouseholdID != actor.HouseholdID {
					continue
				}
				b, _ := json.Marshal(e)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
				flusher.Flush()
			}
		}
	}
}

// spa serves the embedded UI, falling back to index.html for client routes.
func spa() http.Handler {
	dist, _ := fs.Sub(web.Dist, "dist")
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := dist.Open(p); err == nil {
				_ = f.Close()
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				if p == "sw.js" {
					// The service worker updates as soon as a new binary ships.
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><title>Exhale</title><body style="font-family:system-ui;padding:3rem"><h1>Exhale</h1><p>The API is running (<a href="/api/docs">API docs</a>). The web UI is not built into this binary: run <code>mise run build</code>.</p>`)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
