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
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akamachinecode/exhale/internal/agents"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/web"
)

// Config configures the HTTP server.
type Config struct {
	Version string
	// TrustLocal lets requests from this machine act as the household owner
	// without a token. Until web sign-in ships this is how the UI works
	// locally; remote callers always need a token.
	TrustLocal bool
	Log        *slog.Logger
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
	api := humago.New(mux, hc)
	registerAll(api, svc)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/v1/events", events(svc))

	mcpSrv, err := agents.NewServer(svc, cfg.Version, func(ctx context.Context, extra *mcp.RequestExtra, session *mcp.ServerSession) (app.Actor, error) {
		a, ok := actorFrom(ctx)
		if !ok {
			return app.Actor{}, auth.ErrUnauthorized
		}
		if name := agents.ClientName(session); name != "" && a.Kind == app.ActorAgent {
			a.Label = name + " (" + a.Label + ")"
		}
		return a, nil
	})
	if err != nil {
		return nil, nil, err
	}
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil))
	mux.Handle("/", spa())

	return authenticate(svc, cfg, mux), api, nil
}

// authenticate resolves the actor of every API and MCP request.
func authenticate(svc *app.Service, cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protected := strings.HasPrefix(r.URL.Path, "/api/v1") || r.URL.Path == "/mcp"
		if !protected {
			next.ServeHTTP(w, r)
			return
		}
		var actor app.Actor
		if tok := auth.BearerToken(r.Header); tok != "" {
			a, err := auth.ResolveToken(r.Context(), svc.Store(), tok)
			if err != nil {
				writeProblem(w, http.StatusUnauthorized, "invalid token")
				return
			}
			actor = a
		} else if cfg.TrustLocal && isLoopback(r) {
			a, err := svc.Bootstrap(r.Context(), app.BootstrapInput{})
			if err != nil {
				writeProblem(w, http.StatusServiceUnavailable, "household not initialized: run `exhale init`")
				return
			}
			actor = a
		} else {
			w.Header().Set("WWW-Authenticate", `Bearer realm="exhale"`)
			writeProblem(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, actor)))
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

func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"status": status, "title": http.StatusText(status), "detail": detail})
}

type resp[T any] struct{ Body T }

func registerAll(api huma.API, svc *app.Service) {
	register(api, svc, app.OpGetContext)
	register(api, svc, app.OpGetHousehold)
	register(api, svc, app.OpListSubscriptions)
	register(api, svc, app.OpGetSubscription)
	register(api, svc, app.OpCreateSubscription)
	register(api, svc, app.OpUpdateSubscription)
	register(api, svc, app.OpSetSubscriptionStatus)
	register(api, svc, app.OpDeleteSubscription)
	register(api, svc, app.OpListCharges)
	register(api, svc, app.OpNeedsAttention)
	register(api, svc, app.OpChangeChargeDate)
	register(api, svc, app.OpChangeChargeAmount)
	register(api, svc, app.OpSetChargeStatus)
	register(api, svc, app.OpPreviewSchedule)
	register(api, svc, app.OpSummary)
	register(api, svc, app.OpSavePaymentMethod)
	register(api, svc, app.OpCreateCategory)
	register(api, svc, app.OpListAudit)
	register(api, svc, app.OpUndo)
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
	switch {
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, app.ErrForbidden):
		return huma.Error403Forbidden(err.Error())
	}
	return huma.Error500InternalServerError("internal error", err)
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
				f.Close()
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
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

