package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/oauth"
)

// Remote MCP (ADR 0018 milestone 3, ADR 0024): Exhale is the OAuth 2.1
// authorization server of its own MCP endpoint.
//
//	GET  /.well-known/oauth-protected-resource[/mcp]  protected resource metadata (RFC 9728)
//	GET  /.well-known/oauth-authorization-server      authorization server metadata (RFC 8414)
//	POST /oauth/register                              dynamic client registration (RFC 7591)
//	GET  /oauth/authorize                             → the consent page of the web
//	POST /oauth/token                                 codes and refresh tokens
//	POST /oauth/revoke                                revocation (RFC 7009)
//
// The consent page and the list of connected apps talk to session-only
// endpoints under /api/v1/auth: an agent cannot approve itself.

const (
	prmPath       = "/.well-known/oauth-protected-resource"
	asMetaPath    = "/.well-known/oauth-authorization-server"
	oauthRegister = "/oauth/register"
	oauthAuthz    = "/oauth/authorize"
	oauthToken    = "/oauth/token" //nolint:gosec // G101: a path, not a secret
	oauthRevoke   = "/oauth/revoke"
	mcpPath       = "/mcp"
	consentPage   = "/authorize"
	docsURL       = "https://github.com/akamachinecode/exhale/blob/main/docs/guides/remote-agents.md"
	maxOAuthBody  = 64 << 10
)

type oauthAPI struct {
	a   *authAPI
	svc *oauth.Service
	// Throttles per IP. Registration is rare; token requests come once an
	// hour per connection; authorization requests may fetch a document.
	registers, tokens, authorizes limiter
}

func newOAuthAPI(a *authAPI) *oauthAPI {
	svc := a.cfg.OAuth
	if svc == nil {
		svc = oauth.New(a.svc.Store())
	}
	return &oauthAPI{
		a: a, svc: svc,
		registers:  limiter{every: time.Minute, burst: 10},
		tokens:     limiter{every: 2 * time.Second, burst: 30},
		authorizes: limiter{every: 3 * time.Second, burst: 20},
	}
}

// base is this server's origin, the issuer: --public-url, or this machine's
// own address. A Host header from elsewhere must not decide the audience
// of tokens.
func (o *oauthAPI) base(r *http.Request) (string, bool) { return o.a.publicBase(r) }

func resourceOf(base string) string {
	r, _ := oauth.NormalizeResource(base + mcpPath)
	return r
}

func registerOAuth(api huma.API, mux *http.ServeMux, o *oauthAPI) {
	mux.HandleFunc("GET "+prmPath, o.resourceMetadata)
	mux.HandleFunc("GET "+prmPath+mcpPath, o.resourceMetadata)
	mux.HandleFunc("GET "+asMetaPath, o.serverMetadata)
	mux.HandleFunc("POST "+oauthRegister, o.register)
	mux.HandleFunc("GET "+oauthAuthz, o.authorize)
	mux.HandleFunc("POST "+oauthToken, o.token)
	mux.HandleFunc("POST "+oauthRevoke, o.revoke)
	for _, p := range []string{prmPath, prmPath + mcpPath, asMetaPath, oauthRegister, oauthToken, oauthRevoke} {
		mux.HandleFunc("OPTIONS "+p, preflight)
	}
	registerConsent(api, o)
}

// cors lets browser-based MCP clients (MCP Inspector) read these
// endpoints. None of them uses cookies, so any origin is fine.
func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
}

func preflight(w http.ResponseWriter, _ *http.Request) {
	cors(w)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch oauth.Code(err) {
	case "invalid_client":
		status = http.StatusUnauthorized
	case "server_error":
		status = http.StatusInternalServerError
	case "temporarily_unavailable":
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]string{"error": oauth.Code(err), "error_description": oauth.Description(err)})
}

func (o *oauthAPI) noBase(w http.ResponseWriter) {
	o.a.log().Warn("remote MCP needs --public-url (EXHALE_PUBLIC_URL) to be used from another machine")
	writeProblem(w, http.StatusNotFound, "remote access is not set up: the server needs EXHALE_PUBLIC_URL")
}

func (o *oauthAPI) resourceMetadata(w http.ResponseWriter, r *http.Request) {
	cors(w)
	base, ok := o.base(r)
	if !ok {
		o.noBase(w)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 resourceOf(base),
		"authorization_servers":    []string{base},
		"scopes_supported":         oauth.Scopes,
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Exhale",
		"resource_documentation":   docsURL,
	})
}

func (o *oauthAPI) serverMetadata(w http.ResponseWriter, r *http.Request) {
	cors(w)
	base, ok := o.base(r)
	if !ok {
		o.noBase(w)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	auth := []string{"none", "client_secret_basic", "client_secret_post"}
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                 base,
		"authorization_endpoint": base + oauthAuthz,
		"token_endpoint":         base + oauthToken,
		"registration_endpoint":  base + oauthRegister,
		"revocation_endpoint":    base + oauthRevoke,
		// offline_access only tells clients that refresh tokens exist
		// (they always do); it is not a scope of the resource.
		"scopes_supported":                               append(append([]string{}, oauth.Scopes...), "offline_access"),
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          auth,
		"revocation_endpoint_auth_methods_supported":     auth,
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
		"service_documentation":                          docsURL,
	})
}

func (o *oauthAPI) register(w http.ResponseWriter, r *http.Request) {
	cors(w)
	noStore(w)
	if !o.registers.allow(clientIP(r.Context())) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "temporarily_unavailable", "error_description": "too many registrations; wait a minute"})
		return
	}
	if _, ok := o.base(r); !ok {
		o.noBase(w)
		return
	}
	var in oauth.Registration
	dec := json.NewDecoder(io.LimitReader(r.Body, maxOAuthBody))
	if err := dec.Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata", "error_description": "the body must be a JSON object"})
		return
	}
	c, err := o.svc.Register(r.Context(), in)
	if err != nil {
		o.a.log().Info("oauth: registration refused", "err", err)
		oauthError(w, err)
		return
	}
	o.a.log().Info("oauth: application registered", "client", c.Name, "redirect", c.RedirectURIs[0])
	out := map[string]any{
		"client_id":                  c.ID,
		"client_id_issued_at":        c.CreatedAt.Unix(),
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": c.AuthMethod,
	}
	if c.URI != "" {
		out["client_uri"] = c.URI
	}
	if c.Secret != "" {
		out["client_secret"] = c.Secret
		out["client_secret_expires_at"] = 0
	}
	writeJSON(w, http.StatusCreated, out)
}

// Why the consent page could not be shown; the web turns these into
// sentences (web/src/routes/authorize.tsx). Nothing the application wrote
// is shown on Exhale's own page.
const (
	authzClient    = "client"    // unknown client, or its document could not be read
	authzRequest   = "request"   // the request itself is wrong
	authzConfig    = "config"    // no public URL
	authzThrottled = "throttled" // too many attempts
	authzServer    = "server"    // our fault
)

func (o *oauthAPI) authorize(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	fail := func(code string) { http.Redirect(w, r, consentPage+"?error="+code, http.StatusFound) }
	if !o.authorizes.allow(clientIP(r.Context())) {
		fail(authzThrottled)
		return
	}
	base, ok := o.base(r)
	if !ok {
		o.a.log().Warn("remote MCP needs --public-url (EXHALE_PUBLIC_URL) to be used from another machine")
		fail(authzConfig)
		return
	}
	q := r.URL.Query()
	p := oauth.AuthorizeParams{
		ResponseType: q.Get("response_type"), ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"),
		Scope: q.Get("scope"), State: q.Get("state"), CodeChallenge: q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"), Resource: q["resource"],
	}
	req, err := o.svc.Authorize(r.Context(), p, base, resourceOf(base))
	if err != nil {
		o.a.log().Info("oauth: authorization request refused", "client", clipText(p.ClientID), "err", err)
		// Errors are shown here, never sent to the redirect URI before the
		// person has seen who is asking: an address anyone can register
		// must not turn Exhale into an open redirector.
		switch {
		case req == nil && oauth.Code(err) == "invalid_client":
			fail(authzClient)
		case oauth.Code(err) == "server_error":
			fail(authzServer)
		default:
			fail(authzRequest)
		}
		return
	}
	http.Redirect(w, r, consentPage+"?request="+url.QueryEscape(req.ID), http.StatusFound)
}

// clientCredentials reads client authentication from the Authorization
// header (client_secret_basic) or the body (client_secret_post, or a
// public client's client_id).
func clientCredentials(r *http.Request) (id, secret string, err error) {
	id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	if u, p, ok := r.BasicAuth(); ok {
		if secret != "" {
			return "", "", &oauth.Error{Code: "invalid_request", Description: "send client credentials once, in the header or in the body"}
		}
		// RFC 6749 section 2.3.1: both are form-encoded inside Basic.
		bu, err1 := url.QueryUnescape(u)
		bp, err2 := url.QueryUnescape(p)
		if err1 != nil || err2 != nil || (id != "" && id != bu) {
			return "", "", &oauth.Error{Code: "invalid_client", Description: "malformed client credentials"}
		}
		return bu, bp, nil
	}
	return id, secret, nil
}

func parseForm(r *http.Request) error {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "application/x-www-form-urlencoded" {
		return &oauth.Error{Code: "invalid_request", Description: "the body must be application/x-www-form-urlencoded"}
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxOAuthBody)
	if err := r.ParseForm(); err != nil {
		return &oauth.Error{Code: "invalid_request", Description: "the body could not be read"}
	}
	for k, v := range r.PostForm {
		if len(v) > 1 && k != "resource" {
			return &oauth.Error{Code: "invalid_request", Description: k + " is repeated"}
		}
	}
	return nil
}

func (o *oauthAPI) token(w http.ResponseWriter, r *http.Request) {
	cors(w)
	noStore(w)
	w.Header().Set("Pragma", "no-cache")
	if !o.tokens.allow(clientIP(r.Context())) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "temporarily_unavailable", "error_description": "too many requests; wait a moment"})
		return
	}
	if err := parseForm(r); err != nil {
		oauthError(w, err)
		return
	}
	id, secret, err := clientCredentials(r)
	if err != nil {
		oauthError(w, err)
		return
	}
	f := r.PostForm
	out, err := o.svc.Token(r.Context(), oauth.TokenRequest{
		GrantType: f.Get("grant_type"), ClientID: id, ClientSecret: secret, Code: f.Get("code"),
		RedirectURI: f.Get("redirect_uri"), CodeVerifier: f.Get("code_verifier"),
		RefreshToken: f.Get("refresh_token"), Scope: f.Get("scope"), Resource: f["resource"],
	})
	if err != nil {
		o.a.log().Info("oauth: token request refused", "grant", clipText(f.Get("grant_type")), "err", err)
		if _, _, basic := r.BasicAuth(); basic && oauth.Code(err) == "invalid_client" {
			w.Header().Set("WWW-Authenticate", `Basic realm="exhale"`)
		}
		oauthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (o *oauthAPI) revoke(w http.ResponseWriter, r *http.Request) {
	cors(w)
	noStore(w)
	if !o.tokens.allow(clientIP(r.Context())) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "temporarily_unavailable", "error_description": "too many requests; wait a moment"})
		return
	}
	if err := parseForm(r); err != nil {
		oauthError(w, err)
		return
	}
	id, secret, err := clientCredentials(r)
	if err != nil {
		oauthError(w, err)
		return
	}
	tok := r.PostForm.Get("token")
	if tok == "" {
		oauthError(w, &oauth.Error{Code: "invalid_request", Description: "token is missing"})
		return
	}
	if err := o.svc.Revoke(r.Context(), id, secret, tok); err != nil {
		oauthError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// challenge is the WWW-Authenticate header of a 401 from /mcp: where the
// metadata is, and the scopes to ask for (MCP authorization spec).
func (o *oauthAPI) challenge(r *http.Request, errCode string) string {
	base, ok := o.base(r)
	if !ok {
		return `Bearer realm="exhale"`
	}
	h := `Bearer realm="exhale", resource_metadata="` + base + prmPath + mcpPath + `", scope="` + strings.Join(oauth.Scopes, " ") + `"`
	if errCode != "" {
		h += `, error="` + errCode + `"`
	}
	return h
}

// accessActor resolves an OAuth access token presented at /mcp. Its
// audience must be this server's MCP endpoint.
func (o *oauthAPI) accessActor(r *http.Request, tok string) (app.Actor, error) {
	base, ok := o.base(r)
	if !ok {
		return app.Actor{}, oauth.ErrInvalidToken
	}
	return o.svc.Access(r.Context(), tok, resourceOf(base))
}

// Consent screen and connected apps: for a person signed in to the web.

// OAuthRequestInfo is what the consent screen shows.
type OAuthRequestInfo struct {
	ClientName   string `json:"clientName"`
	Verified     bool   `json:"verified" doc:"the application is identified by an https address Exhale fetched; otherwise it registered itself and its name is unconfirmed"`
	ClientHost   string `json:"clientHost,omitempty" doc:"host of that address, when verified"`
	ClientURI    string `json:"clientUri,omitempty" doc:"the application's home page, as it describes itself"`
	RedirectHost string `json:"redirectHost" doc:"where the browser goes after the answer"`
	Loopback     bool   `json:"loopback" doc:"the answer goes to a program on this computer"`
	MaxScope     string `json:"maxScope" enum:"read,write" doc:"the most the application asked for; the person may give less"`
	MemberName   string `json:"memberName" doc:"the application will act as this member"`
}

type oauthDecision struct {
	Approve bool   `json:"approve"`
	Scope   string `json:"scope,omitempty" enum:"read,write" doc:"required when approving"`
}

type oauthRedirect struct {
	RedirectTo string `json:"redirectTo" doc:"send the browser here: the application's address with the answer"`
}

// ConnectedApps lists the applications connected over OAuth.
type ConnectedApps struct {
	Apps []oauth.App `json:"apps"`
	// MCPURL is the address to give a cloud assistant; empty when the
	// server has no public URL.
	MCPURL string `json:"mcpUrl" doc:"the MCP address to give Claude.ai, ChatGPT or Grok; empty without EXHALE_PUBLIC_URL"`
}

func registerConsent(api huma.API, o *oauthAPI) {
	a := o.a
	huma.Register(api, authOp("auth_oauth_request", http.MethodGet, "/oauth/requests/{id}", "An application asking to connect"),
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*resp[OAuthRequestInfo], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			req, ok := o.svc.Pending(in.ID)
			if !ok {
				return nil, huma.Error404NotFound(oauth.ErrRequestGone.Error())
			}
			most := req.MaxScope
			if actor.Scope == app.ScopeRead {
				most = app.ScopeRead
			}
			return &resp[OAuthRequestInfo]{Body: OAuthRequestInfo{
				ClientName: req.Client.Name, Verified: req.Client.Verified(), ClientHost: req.Client.Host(), ClientURI: req.Client.URI,
				RedirectHost: req.RedirectHost(), Loopback: req.Loopback(), MaxScope: string(most), MemberName: actor.Label,
			}}, nil
		})

	huma.Register(api, authOp("auth_oauth_decide", http.MethodPost, "/oauth/requests/{id}", "Allow or deny an application"),
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body oauthDecision
		}) (*resp[oauthRedirect], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			to, err := o.svc.Decide(ctx, in.ID, actor, in.Body.Approve, app.Scope(in.Body.Scope))
			if errors.Is(err, oauth.ErrRequestGone) {
				return nil, huma.Error404NotFound(err.Error())
			}
			if err != nil {
				return nil, httpError(err)
			}
			if in.Body.Approve {
				a.log().Info("oauth: application allowed", "member", actor.MemberID, "scope", in.Body.Scope)
			}
			return &resp[oauthRedirect]{Body: oauthRedirect{RedirectTo: to}}, nil
		})

	huma.Register(api, authOp("auth_list_apps", http.MethodGet, "/apps", "Applications connected to your account"),
		func(ctx context.Context, _ *struct{}) (*resp[ConnectedApps], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			list, err := o.svc.Apps(ctx, actor.MemberID)
			if err != nil {
				return nil, httpError(err)
			}
			out := ConnectedApps{Apps: list}
			if a.cfg.PublicURL != "" {
				out.MCPURL = resourceOf(strings.TrimRight(a.cfg.PublicURL, "/"))
			}
			return &resp[ConnectedApps]{Body: out}, nil
		})

	huma.Register(api, authOp("auth_disconnect_app", http.MethodPost, "/apps/disconnect", "Disconnect an application"),
		func(ctx context.Context, in *struct {
			Body struct {
				ID string `json:"id"`
			}
		}) (*resp[empty], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			if err := o.svc.Disconnect(ctx, actor.MemberID, in.Body.ID); err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return nil, huma.Error404NotFound("no such connected app")
				}
				return nil, httpError(err)
			}
			return &resp[empty]{}, nil
		})
}
