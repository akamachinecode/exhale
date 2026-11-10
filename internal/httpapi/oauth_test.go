package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/oauth"
)

// oauthEnv is an Exhale with Alex signed in to the web in b, and a clock
// the test moves.
type oauthEnv struct {
	t   *testing.T
	srv *httptest.Server
	svc *app.Service
	oa  *oauth.Service
	b   *browser

	mu  sync.Mutex
	now time.Time
}

const testRedirect = "https://assistant.example/callback"

func newOAuthEnv(t *testing.T, opt func(cfg *Config, self string)) *oauthEnv {
	t.Helper()
	e := &oauthEnv{t: t, svc: newService(t), now: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}
	e.oa = oauth.New(e.svc.Store())
	e.oa.Now = func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }
	if _, err := auth.Setup(context.Background(), e.svc, alex, true); err != nil {
		t.Fatal(err)
	}
	e.srv = serve(t, e.svc, nil, func(cfg *Config, self string) {
		cfg.OAuth = e.oa
		if opt != nil {
			opt(cfg, self)
		}
	})
	e.b = newBrowser(t, e.srv.URL)
	if code, _ := e.b.do("POST", "/api/v1/auth/login", map[string]string{"email": alex.Email, "password": alex.Password}); code != 200 {
		t.Fatalf("login: %d", code)
	}
	return e
}

func (e *oauthEnv) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func (e *oauthEnv) resource() string { return e.srv.URL + "/mcp" }

// call makes a request the way a server-side application does: no cookies,
// no browser headers.
func (e *oauthEnv) call(method, path string, body string, h http.Header) (int, map[string]any, http.Header) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	for k, v := range h {
		req.Header[k] = v
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out, res.Header
}

func (e *oauthEnv) register(body map[string]any) (int, map[string]any) {
	e.t.Helper()
	b, _ := json.Marshal(body)
	code, out, _ := e.call("POST", "/oauth/register", string(b), http.Header{"Content-Type": {"application/json"}})
	return code, out
}

// client registers a public client and returns its id.
func (e *oauthEnv) client(name string) string {
	e.t.Helper()
	code, out := e.register(map[string]any{"client_name": name, "redirect_uris": []string{testRedirect}, "grant_types": []string{"authorization_code", "refresh_token"}})
	if code != 201 || out["client_id"] == nil || out["client_secret"] != nil || out["token_endpoint_auth_method"] != "none" {
		e.t.Fatalf("register: %d %v", code, out)
	}
	return out["client_id"].(string)
}

func (e *oauthEnv) form(path string, v url.Values, h http.Header) (int, map[string]any) {
	e.t.Helper()
	if h == nil {
		h = http.Header{}
	}
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	code, out, _ := e.call("POST", path, v.Encode(), h)
	return code, out
}

func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type authz struct {
	clientID, redirect, scope, state, resource string
	noPKCE                                     bool
}

// start opens the authorization URL in the signed-in browser and returns
// where Exhale sent it: the consent page or an error page.
func (e *oauthEnv) start(a authz, verifier string) *url.URL {
	e.t.Helper()
	q := url.Values{"response_type": {"code"}, "client_id": {a.clientID}, "redirect_uri": {a.redirect}, "state": {a.state}}
	if !a.noPKCE {
		q.Set("code_challenge", challenge(verifier))
		q.Set("code_challenge_method", "S256")
	}
	if a.scope != "" {
		q.Set("scope", a.scope)
	}
	if a.resource != "-" {
		r := a.resource
		if r == "" {
			r = e.resource()
		}
		q.Set("resource", r)
	}
	res := e.b.hop("/oauth/authorize?" + q.Encode())
	if res.StatusCode != http.StatusFound {
		e.t.Fatalf("authorize: %d", res.StatusCode)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	if loc.Host != "" || loc.Path != "/authorize" {
		e.t.Fatalf("authorize must land on Exhale's consent page, got %s", loc)
	}
	return loc
}

func (e *oauthEnv) decide(id string, approve bool, scope string) (int, map[string]any) {
	e.t.Helper()
	body := map[string]any{"approve": approve}
	if scope != "" {
		body["scope"] = scope
	}
	return e.b.do("POST", "/api/v1/auth/oauth/requests/"+id, body)
}

// approve goes through the consent page and returns the redirect with the
// code.
func (e *oauthEnv) approve(a authz, verifier, scope string) *url.URL {
	e.t.Helper()
	loc := e.start(a, verifier)
	id := loc.Query().Get("request")
	if id == "" {
		e.t.Fatalf("no consent request: %s", loc)
	}
	code, out := e.decide(id, true, scope)
	if code != 200 {
		e.t.Fatalf("approve: %d %v", code, out)
	}
	to, _ := url.Parse(out["redirectTo"].(string))
	return to
}

func (e *oauthEnv) exchange(clientID, code, verifier, resource string) (int, map[string]any) {
	e.t.Helper()
	v := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "redirect_uri": {testRedirect}, "code_verifier": {verifier}}
	if resource != "" {
		v.Set("resource", resource)
	}
	return e.form("/oauth/token", v, nil)
}

func (e *oauthEnv) refresh(clientID, tok string) (int, map[string]any) {
	e.t.Helper()
	return e.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {tok}, "resource": {e.resource()}}, nil)
}

// connect runs the whole flow for a new client and returns its tokens.
func (e *oauthEnv) connect(name, scope string) (string, map[string]any) {
	e.t.Helper()
	id := e.client(name)
	verifier := oauth2.GenerateVerifier()
	to := e.approve(authz{clientID: id, redirect: testRedirect, state: "s1"}, verifier, scope)
	code, tok := e.exchange(id, to.Query().Get("code"), verifier, e.resource())
	if code != 200 {
		e.t.Fatalf("token: %d %v", code, tok)
	}
	return id, tok
}

// mcpStatus is the status of an MCP initialize request with a token.
func (e *oauthEnv) mcpStatus(tok string) (int, http.Header) {
	e.t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	h := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json, text/event-stream"}}
	if tok != "" {
		h.Set("Authorization", "Bearer "+tok)
	}
	code, _, hdr := e.call("POST", "/mcp", body, h)
	return code, hdr
}

func (e *oauthEnv) mcp(tok string) *mcp.ClientSession {
	e.t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "chat-client", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: e.resource(), HTTPClient: &http.Client{Transport: bearer{tok}}}, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { cs.Close() })
	return cs
}

func (e *oauthEnv) apps() []any {
	e.t.Helper()
	code, out := e.b.do("GET", "/api/v1/auth/apps", nil)
	if code != 200 {
		e.t.Fatalf("apps: %d %v", code, out)
	}
	list, _ := out["apps"].([]any)
	return list
}

// audit returns "label action" for every row of the activity timeline.
func (e *oauthEnv) audit() []string {
	e.t.Helper()
	log, err := e.svc.ListAudit(context.Background(), app.Actor{}, app.AuditLogInput{Limit: 100})
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, a := range log.Entries {
		out = append(out, a.ActorLabel+" "+a.Action)
	}
	return out
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// --- tests --------------------------------------------------------------------

func TestOAuthDiscovery(t *testing.T) {
	e := newOAuthEnv(t, nil)
	code, hdr := e.mcpStatus("")
	want := `Bearer realm="exhale", resource_metadata="` + e.srv.URL + `/.well-known/oauth-protected-resource/mcp", scope="read write"`
	if code != 401 || hdr.Get("WWW-Authenticate") != want {
		t.Fatalf("401 challenge: %d %q", code, hdr.Get("WWW-Authenticate"))
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"} {
		code, prm, h := e.call("GET", p, "", nil)
		if code != 200 || prm["resource"] != e.resource() || prm["authorization_servers"].([]any)[0] != e.srv.URL || h.Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s: %d %v", p, code, prm)
		}
	}
	code, as, _ := e.call("GET", "/.well-known/oauth-authorization-server", "", nil)
	if code != 200 || as["issuer"] != e.srv.URL || as["token_endpoint"] != e.srv.URL+"/oauth/token" ||
		as["client_id_metadata_document_supported"] != true || as["authorization_response_iss_parameter_supported"] != true {
		t.Fatalf("server metadata: %d %v", code, as)
	}
	if sc := as["scopes_supported"].([]any); len(sc) != 3 || sc[2] != "offline_access" {
		t.Fatalf("scopes: %v", sc)
	}
	if m := as["code_challenge_methods_supported"].([]any); len(m) != 1 || m[0] != "S256" {
		t.Fatalf("only S256: %v", m)
	}

	// From another machine, without --public-url, Exhale does not know its
	// own address and does not guess it from the Host header.
	remote := http.Header{"X-Forwarded-For": {"203.0.113.9"}}
	if code, _, _ := e.call("GET", "/.well-known/oauth-authorization-server", "", remote); code != 404 {
		t.Fatalf("metadata without a public URL: %d", code)
	}
}

func TestOAuthPublicURL(t *testing.T) {
	e := newOAuthEnv(t, func(cfg *Config, _ string) { cfg.PublicURL = "https://exhale.example.com/" })
	remote := http.Header{"X-Forwarded-For": {"203.0.113.9"}}
	_, prm, _ := e.call("GET", "/.well-known/oauth-protected-resource/mcp", "", remote)
	if prm["resource"] != "https://exhale.example.com/mcp" || prm["authorization_servers"].([]any)[0] != "https://exhale.example.com" {
		t.Fatalf("metadata behind a proxy: %v", prm)
	}
	_, out := e.b.do("GET", "/api/v1/auth/apps", nil)
	if out["mcpUrl"] != "https://exhale.example.com/mcp" {
		t.Fatalf("the Agents page shows the address to paste: %v", out)
	}
	// Tokens are only issued for the public MCP address.
	id := e.client("Assistant")
	loc := e.start(authz{clientID: id, redirect: testRedirect, resource: e.resource()}, oauth2.GenerateVerifier())
	if loc.Query().Get("error") != "request" {
		t.Fatalf("a resource that is not this server's public MCP address: %s", loc)
	}
}

// TestOAuthWithTheGoSDKClient connects the official MCP Go SDK client to
// Exhale with nothing but the MCP URL: discovery, dynamic registration,
// PKCE, consent, the issuer check (RFC 9207), tokens and tool calls.
func TestOAuthWithTheGoSDKClient(t *testing.T) {
	e := newOAuthEnv(t, nil)
	fetch := func(ctx context.Context, args *mcpauth.AuthorizationArgs) (*mcpauth.AuthorizationResult, error) {
		u, _ := url.Parse(args.URL)
		if got := u.Query().Get("resource"); got != e.resource() {
			t.Errorf("the client asks for resource %q", got)
		}
		loc := e.b.hop(u.RequestURI()).Header.Get("Location")
		id := strings.TrimPrefix(loc, "/authorize?request=")
		code, info := e.b.do("GET", "/api/v1/auth/oauth/requests/"+id, nil)
		if code != 200 || info["clientName"] != "Test assistant" || info["verified"] != false || info["redirectHost"] != "127.0.0.1" || info["loopback"] != true || info["maxScope"] != "write" || info["memberName"] != "Alex" {
			t.Errorf("consent screen: %d %v", code, info)
		}
		_, out := e.decide(id, true, "write")
		to, _ := url.Parse(out["redirectTo"].(string))
		q := to.Query()
		return &mcpauth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}, nil
	}
	h, err := mcpauth.NewAuthorizationCodeHandler(&mcpauth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &mcpauth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName: "Test assistant", RedirectURIs: []string{"http://127.0.0.1:43111/callback"},
			GrantTypes: []string{"authorization_code", "refresh_token"},
		}},
		AuthorizationCodeFetcher: fetch,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "sdk-client", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: e.resource(), OAuthHandler: h}, nil)
	if err != nil {
		t.Fatalf("connect with OAuth: %v", err)
	}
	defer cs.Close()
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_category", Arguments: map[string]any{"name": "AI tools"}})
	if err != nil || res.IsError {
		t.Fatalf("write over OAuth: %v %+v", err, res)
	}
	log := e.audit()
	if !has(log, "Test assistant category.create") || !has(log, "Test assistant app.connect") {
		t.Fatalf("the timeline names the application: %v", log)
	}
	apps := e.apps()
	if len(apps) != 1 || apps[0].(map[string]any)["name"] != "Test assistant" || apps[0].(map[string]any)["scope"] != "write" {
		t.Fatalf("connected apps: %v", apps)
	}
}

func TestOAuthCodeFlowFailures(t *testing.T) {
	e := newOAuthEnv(t, nil)
	id := e.client("Assistant")
	verifier := oauth2.GenerateVerifier()
	to := e.approve(authz{clientID: id, redirect: testRedirect, state: "xyz"}, verifier, "write")
	q := to.Query()
	if to.Host != "assistant.example" || q.Get("state") != "xyz" || q.Get("iss") != e.srv.URL || q.Get("code") == "" {
		t.Fatalf("redirect with the code: %s", to)
	}
	codeVal := q.Get("code")

	if code, out := e.exchange(id, codeVal, oauth2.GenerateVerifier(), ""); code != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("wrong verifier: %d %v", code, out)
	}
	if code, out := e.exchange(id, codeVal, verifier, "https://other.example/mcp"); code != 400 || out["error"] != "invalid_target" {
		t.Fatalf("wrong resource: %d %v", code, out)
	}
	other := e.client("Other")
	if code, out := e.exchange(other, codeVal, verifier, ""); code != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("another client's code: %d %v", code, out)
	}
	if code, out := e.exchange("oac_unknown", codeVal, verifier, ""); code != 401 || out["error"] != "invalid_client" {
		t.Fatalf("unknown client: %d %v", code, out)
	}
	code, tok := e.exchange(id, codeVal, verifier, e.resource())
	if code != 200 || tok["token_type"] != "Bearer" || tok["scope"] != "read write" || tok["expires_in"] != float64(3600) ||
		!strings.HasPrefix(tok["access_token"].(string), oauth.AccessPrefix) || !strings.HasPrefix(tok["refresh_token"].(string), oauth.RefreshPrefix) {
		t.Fatalf("token: %d %v", code, tok)
	}
	access := tok["access_token"].(string)
	if code, _ := e.mcpStatus(access); code != 200 {
		t.Fatalf("MCP with the access token: %d", code)
	}
	// The audience is the MCP endpoint, nothing else.
	if code, _, _ := e.call("GET", "/api/v1/household", "", http.Header{"Authorization": {"Bearer " + access}}); code != 401 {
		t.Fatalf("an OAuth token must not open the REST API: %d", code)
	}

	// A code used twice was copied: the second use fails and ends what the
	// first one obtained.
	if code, out := e.exchange(id, codeVal, verifier, ""); code != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("reused code: %d %v", code, out)
	}
	code, hdr := e.mcpStatus(access)
	if code != 401 || !strings.Contains(hdr.Get("WWW-Authenticate"), `error="invalid_token"`) || !strings.Contains(hdr.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatalf("tokens of a reused code must stop working: %d %q", code, hdr.Get("WWW-Authenticate"))
	}

	// An expired code.
	verifier = oauth2.GenerateVerifier()
	to = e.approve(authz{clientID: id, redirect: testRedirect}, verifier, "read")
	e.advance(oauth.CodeTTL + time.Second)
	if code, out := e.exchange(id, to.Query().Get("code"), verifier, ""); code != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("expired code: %d %v", code, out)
	}

	// A redirect_uri that differs from the authorization request.
	verifier = oauth2.GenerateVerifier()
	to = e.approve(authz{clientID: id, redirect: testRedirect}, verifier, "read")
	v := url.Values{"grant_type": {"authorization_code"}, "client_id": {id}, "code": {to.Query().Get("code")}, "redirect_uri": {"https://assistant.example/other"}, "code_verifier": {verifier}}
	if code, out := e.form("/oauth/token", v, nil); code != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("other redirect_uri: %d %v", code, out)
	}
}

func TestOAuthRefreshRotationAndExpiry(t *testing.T) {
	e := newOAuthEnv(t, nil)
	id, tok := e.connect("Assistant", "write")
	r1 := tok["refresh_token"].(string)

	e.advance(oauth.AccessTTL + time.Second)
	if code, _ := e.mcpStatus(tok["access_token"].(string)); code != 401 {
		t.Fatalf("expired access token: %d", code)
	}
	code, tok2 := e.refresh(id, r1)
	if code != 200 || tok2["refresh_token"] == r1 || tok2["scope"] != "read write" {
		t.Fatalf("refresh: %d %v", code, tok2)
	}
	if code, _ := e.mcpStatus(tok2["access_token"].(string)); code != 200 {
		t.Fatalf("new access token: %d", code)
	}
	if code, out := e.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {tok2["refresh_token"].(string)}, "resource": {"https://other.example/mcp"}}, nil); code != 400 || out["error"] != "invalid_target" {
		t.Fatalf("refresh for another resource: %d %v", code, out)
	}
	other := e.client("Other")
	if code, out := e.refresh(other, tok2["refresh_token"].(string)); code != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("another client's refresh token: %d %v", code, out)
	}

	// Presenting a used refresh token again means it was copied: the whole
	// connection ends, including the tokens issued after it.
	if code, out := e.refresh(id, r1); code != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("reused refresh token: %d %v", code, out)
	}
	if code, _ := e.mcpStatus(tok2["access_token"].(string)); code != 401 {
		t.Fatalf("access after refresh reuse: %d", code)
	}
	if code, _ := e.refresh(id, tok2["refresh_token"].(string)); code != 400 {
		t.Fatalf("the newer refresh token must die with the connection: %d", code)
	}
	if len(e.apps()) != 0 || !has(e.audit(), "Assistant app.disconnect") {
		t.Fatalf("the ended connection: apps %v, timeline %v", e.apps(), e.audit())
	}

	// A refresh token nobody used for its lifetime.
	id, tok = e.connect("Sleepy", "read")
	e.advance(oauth.RefreshTTL + time.Second)
	// By then the connection and the registration were cleaned up: either
	// answer tells the application to connect again.
	if code, out := e.refresh(id, tok["refresh_token"].(string)); out["error"] != "invalid_grant" && out["error"] != "invalid_client" {
		t.Fatalf("expired refresh token: %d %v", code, out)
	}
}

func TestOAuthRevocation(t *testing.T) {
	e := newOAuthEnv(t, nil)
	id, tok := e.connect("Assistant", "write")
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)

	// Another client cannot revoke it, and is not told whether it exists.
	other := e.client("Other")
	if code, _ := e.form("/oauth/revoke", url.Values{"client_id": {other}, "token": {refresh}}, nil); code != 200 {
		t.Fatalf("revoke by another client: %d", code)
	}
	if code, _ := e.mcpStatus(access); code != 200 {
		t.Fatalf("another client must not revoke: %d", code)
	}
	if code, _ := e.form("/oauth/revoke", url.Values{"client_id": {id}, "token": {access}}, nil); code != 200 {
		t.Fatalf("revoke access: %d", code)
	}
	if code, _ := e.mcpStatus(access); code != 401 {
		t.Fatalf("revoked access token: %d", code)
	}
	if code, _ := e.refresh(id, refresh); code != 200 {
		t.Fatalf("revoking an access token keeps the connection: %d", code)
	}
	e.advance(time.Minute)
	_, tok = e.connect("Assistant 2", "read")
	if code, _ := e.form("/oauth/revoke", url.Values{"client_id": {"x"}, "token": {"nonsense"}}, nil); code != 401 {
		t.Fatalf("unknown client: %d", code)
	}
	if n := len(e.apps()); n != 2 {
		t.Fatalf("two connected apps, got %d", n)
	}

	// Disconnecting in the web ends everything.
	app0 := e.apps()[0].(map[string]any) // the newest first
	if app0["name"] != "Assistant 2" {
		t.Fatalf("apps are listed newest first: %v", e.apps())
	}
	if code, _ := e.b.do("POST", "/api/v1/auth/apps/disconnect", map[string]string{"id": app0["id"].(string)}); code != 200 {
		t.Fatalf("disconnect: %d", code)
	}
	if code, _ := e.mcpStatus(tok["access_token"].(string)); code != 401 {
		t.Fatalf("access after disconnect: %d", code)
	}
	if code, _ := e.b.do("POST", "/api/v1/auth/apps/disconnect", map[string]string{"id": app0["id"].(string)}); code != 404 {
		t.Fatalf("disconnect twice: %d", code)
	}
}

func TestOAuthConsentRules(t *testing.T) {
	e := newOAuthEnv(t, nil)
	id := e.client("Assistant")

	// The member may give less than asked, never more.
	loc := e.start(authz{clientID: id, redirect: testRedirect, scope: "read"}, oauth2.GenerateVerifier())
	req := loc.Query().Get("request")
	if _, info := e.b.do("GET", "/api/v1/auth/oauth/requests/"+req, nil); info["maxScope"] != "read" {
		t.Fatalf("consent for a read request: %v", info)
	}
	if code, _ := e.decide(req, true, "write"); code != 422 {
		t.Fatalf("more than asked: %d", code)
	}
	if code, _ := e.decide(req, true, "trusted"); code != 422 {
		t.Fatalf("trusted is never offered: %d", code)
	}

	// An agent with a token cannot approve on the member's behalf.
	owner, _ := e.svc.Owner(context.Background())
	pat, _ := auth.CreateToken(context.Background(), e.svc.Store(), owner.MemberID, "Agent", app.ScopeTrusted)
	agent := newBrowser(t, e.srv.URL)
	agent.hdr.Set("Authorization", "Bearer "+pat)
	if code, _ := agent.do("POST", "/api/v1/auth/oauth/requests/"+req, map[string]any{"approve": true, "scope": "read"}); code != 401 {
		t.Fatalf("approval by an agent token: %d", code)
	}
	// Nor can a cross-site page in the member's browser.
	e.b.hdr.Set("Sec-Fetch-Site", "cross-site")
	if code, _ := e.b.do("POST", "/api/v1/auth/oauth/requests/"+req, map[string]any{"approve": true, "scope": "read"}); code != 403 {
		t.Fatalf("cross-site approval: %d", code)
	}
	e.b.hdr.Del("Sec-Fetch-Site")

	// Read access: the application sees everything and changes nothing.
	verifier := oauth2.GenerateVerifier()
	to := e.approve(authz{clientID: id, redirect: testRedirect}, verifier, "read")
	_, tok := e.exchange(id, to.Query().Get("code"), verifier, "")
	if tok["scope"] != "read" {
		t.Fatalf("read token: %v", tok)
	}
	cs := e.mcp(tok["access_token"].(string))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_category", Arguments: map[string]any{"name": "Nope"}})
	if err != nil || !res.IsError {
		t.Fatalf("a read connection must not write: %v %+v", err, res)
	}
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_subscriptions", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if code, out := e.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {tok["refresh_token"].(string)}, "scope": {"read write"}}, nil); code != 400 || out["error"] != "invalid_scope" {
		t.Fatalf("refresh asking for more: %d %v", code, out)
	}

	// Deny: the application hears access_denied, with state and issuer.
	loc = e.start(authz{clientID: id, redirect: testRedirect, state: "st"}, oauth2.GenerateVerifier())
	req = loc.Query().Get("request")
	_, out := e.decide(req, false, "")
	to, _ = url.Parse(out["redirectTo"].(string))
	if to.Query().Get("error") != "access_denied" || to.Query().Get("state") != "st" || to.Query().Get("iss") != e.srv.URL {
		t.Fatalf("deny: %s", to)
	}
	if code, _ := e.decide(req, true, "read"); code != 404 {
		t.Fatalf("a request is answered once: %d", code)
	}

	// A consent screen left open too long.
	loc = e.start(authz{clientID: id, redirect: testRedirect}, oauth2.GenerateVerifier())
	e.advance(oauth.RequestTTL + time.Second)
	if code, _ := e.decide(loc.Query().Get("request"), true, "read"); code != 404 {
		t.Fatalf("expired request: %d", code)
	}
}

func TestOAuthAuthorizationRequestChecks(t *testing.T) {
	e := newOAuthEnv(t, nil)
	id := e.client("Assistant")
	for name, c := range map[string]struct {
		a    authz
		want string
	}{
		"unknown client":       {authz{clientID: "oac_nope", redirect: testRedirect}, "client"},
		"unregistered address": {authz{clientID: id, redirect: "https://evil.example/callback"}, "request"},
		"no PKCE":              {authz{clientID: id, redirect: testRedirect, noPKCE: true}, "request"},
		"other resource":       {authz{clientID: id, redirect: testRedirect, resource: "https://other.example/mcp"}, "request"},
	} {
		if loc := e.start(c.a, oauth2.GenerateVerifier()); loc.Query().Get("error") != c.want {
			t.Errorf("%s: %s", name, loc)
		}
	}
	// A request without resource gets this server's MCP endpoint.
	verifier := oauth2.GenerateVerifier()
	to := e.approve(authz{clientID: id, redirect: testRedirect, resource: "-"}, verifier, "read")
	if code, out := e.exchange(id, to.Query().Get("code"), verifier, ""); code != 200 {
		t.Fatalf("no resource: %d %v", code, out)
	}
	// A resource spelled with capitals and a trailing slash is the same.
	verifier = oauth2.GenerateVerifier()
	to = e.approve(authz{clientID: id, redirect: testRedirect, resource: strings.Replace(e.resource(), "http://", "HTTP://", 1) + "/"}, verifier, "read")
	if code, out := e.exchange(id, to.Query().Get("code"), verifier, ""); code != 200 {
		t.Fatalf("resource spelling: %d %v", code, out)
	}
}

func TestOAuthRegistration(t *testing.T) {
	e := newOAuthEnv(t, nil)
	for name, body := range map[string]map[string]any{
		"plain http":      {"redirect_uris": []string{"http://assistant.example/cb"}},
		"custom scheme":   {"redirect_uris": []string{"javascript:alert(1)"}},
		"fragment":        {"redirect_uris": []string{"https://assistant.example/cb#x"}},
		"no redirect":     {"client_name": "X"},
		"implicit grant":  {"redirect_uris": []string{testRedirect}, "grant_types": []string{"implicit"}},
		"private key jwt": {"redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "private_key_jwt"},
	} {
		if code, out := e.register(body); code != 400 {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if code, out := e.register(map[string]any{"redirect_uris": []string{"http://localhost:5555/cb"}}); code != 201 || out["client_name"] != "localhost" {
		t.Fatalf("loopback redirect: %d %v", code, out)
	}

	// A confidential client: the secret is required at the token endpoint.
	code, reg := e.register(map[string]any{"client_name": "Cloud", "redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "client_secret_basic"})
	if code != 201 || reg["client_secret"] == nil {
		t.Fatalf("confidential registration: %d %v", code, reg)
	}
	id, secret := reg["client_id"].(string), reg["client_secret"].(string)
	verifier := oauth2.GenerateVerifier()
	to := e.approve(authz{clientID: id, redirect: testRedirect}, verifier, "read")
	v := url.Values{"grant_type": {"authorization_code"}, "code": {to.Query().Get("code")}, "redirect_uri": {testRedirect}, "code_verifier": {verifier}}
	if code, out := e.form("/oauth/token", url.Values{"client_id": {id}, "grant_type": v["grant_type"], "code": v["code"], "redirect_uri": v["redirect_uri"], "code_verifier": v["code_verifier"]}, nil); code != 401 || out["error"] != "invalid_client" {
		t.Fatalf("no secret: %d %v", code, out)
	}
	h := http.Header{}
	req := &http.Request{Header: h}
	req.SetBasicAuth(url.QueryEscape(id), url.QueryEscape(secret))
	if code, out := e.form("/oauth/token", v, h); code != 200 {
		t.Fatalf("basic auth: %d %v", code, out)
	}

	// Registration is throttled per address.
	got429 := false
	for range 12 {
		if code, _ := e.register(map[string]any{"redirect_uris": []string{testRedirect}}); code == 429 {
			got429 = true
		}
	}
	if !got429 {
		t.Fatal("registration is not throttled")
	}
}

// Cloud assistants register again every time they connect: the same app
// connecting again replaces its earlier connection.
func TestOAuthReconnectReplaces(t *testing.T) {
	e := newOAuthEnv(t, nil)
	_, first := e.connect("ChatGPT", "read")
	_, second := e.connect("ChatGPT", "write")
	if apps := e.apps(); len(apps) != 1 || apps[0].(map[string]any)["scope"] != "write" {
		t.Fatalf("one row per app: %v", apps)
	}
	if code, _ := e.mcpStatus(first["access_token"].(string)); code != 401 {
		t.Fatalf("the replaced connection must end: %d", code)
	}
	if code, _ := e.mcpStatus(second["access_token"].(string)); code != 200 {
		t.Fatalf("the new connection: %d", code)
	}
	e.connect("Claude", "read")
	if n := len(e.apps()); n != 2 {
		t.Fatalf("another app is another row: %d", n)
	}
}

func TestOAuthTokenEndpointIsThrottled(t *testing.T) {
	e := newOAuthEnv(t, nil)
	for range 40 {
		if code, _ := e.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {"x"}, "refresh_token": {"y"}}, nil); code == 429 {
			return
		}
	}
	t.Fatal("the token endpoint is not throttled")
}

func TestOAuthPasswordChangeEndsConnections(t *testing.T) {
	e := newOAuthEnv(t, nil)
	id, tok := e.connect("Assistant", "write")
	if code, _ := e.b.do("POST", "/api/v1/auth/password", map[string]string{"current": alex.Password, "new": "another long password"}); code != 200 {
		t.Fatalf("password change: %d", code)
	}
	if code, _ := e.mcpStatus(tok["access_token"].(string)); code != 401 {
		t.Fatalf("access after a password change: %d", code)
	}
	if code, _ := e.refresh(id, tok["refresh_token"].(string)); code != 400 {
		t.Fatalf("refresh after a password change: %d", code)
	}
	if len(e.apps()) != 0 {
		t.Fatalf("apps after a password change: %v", e.apps())
	}
}

// TestOAuthClientMetadataDocument: an application identified by the https
// URL of its metadata document, which Exhale fetches.
func TestOAuthClientMetadataDocument(t *testing.T) {
	var docURL string
	docs := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id": docURL, "client_name": "Claude", "client_uri": "https://claude.example",
			"redirect_uris": []string{testRedirect}, "grant_types": []string{"authorization_code", "refresh_token"},
			"token_endpoint_auth_method": "none",
		})
	}))
	t.Cleanup(docs.Close)
	docURL = docs.URL + "/oauth/client.json"
	e := newOAuthEnv(t, nil)
	e.oa.HTTP = docs.Client()

	verifier := oauth2.GenerateVerifier()
	loc := e.start(authz{clientID: docURL, redirect: testRedirect}, verifier)
	req := loc.Query().Get("request")
	_, info := e.b.do("GET", "/api/v1/auth/oauth/requests/"+req, nil)
	if info["clientName"] != "Claude" || info["verified"] != true || info["clientHost"] != "127.0.0.1" || info["redirectHost"] != "assistant.example" || info["loopback"] != false {
		t.Fatalf("consent for a metadata document client: %v", info)
	}
	_, out := e.decide(req, true, "write")
	to, _ := url.Parse(out["redirectTo"].(string))
	if code, tok := e.exchange(docURL, to.Query().Get("code"), verifier, e.resource()); code != 200 {
		t.Fatalf("token: %d %v", code, tok)
	}
	if apps := e.apps(); len(apps) != 1 || apps[0].(map[string]any)["verified"] != true {
		t.Fatalf("apps: %v", apps)
	}

	// The default fetcher refuses addresses on the server's own network.
	e.oa.HTTP = oauth.New(e.svc.Store()).HTTP
	if loc := e.start(authz{clientID: "https://127.0.0.1:1/client.json", redirect: testRedirect}, verifier); loc.Query().Get("error") != "client" {
		t.Fatalf("a private address as client id: %s", loc)
	}
}
