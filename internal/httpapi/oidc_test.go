package httpapi

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/store"
)

// --- a fake OpenID Connect provider -----------------------------------------

var b64 = base64.RawURLEncoding

// idpKey is generated once: RSA keys are slow to make.
var idpKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

// idpUser is who is signed in at the fake provider.
type idpUser struct {
	Sub      string
	Email    string
	Verified any // true, false, "true" or nil (claim absent)
	Name     string
}

type idpGrant struct {
	user                         idpUser
	challenge, nonce, redirectTo string
}

// fakeIdP serves discovery, JWKS, an authorization endpoint that signs the
// current user in without asking, a token endpoint that checks the client
// secret and PKCE and signs RS256 ID tokens, and userinfo.
type fakeIdP struct {
	t        *testing.T
	srv      *httptest.Server
	clientID string
	secret   string

	mu           sync.Mutex
	user         idpUser
	grants       map[string]idpGrant // by code
	tokens       map[string]idpUser  // by access token
	lastAuth     url.Values
	tamper       string // "", nonce, audience, expired, issuer, signature
	deny         bool   // the person cancels at the provider
	userinfoOnly bool   // email and name only from /userinfo
	pkceFailures int
}

func newIdP(t *testing.T) *fakeIdP {
	t.Helper()
	p := &fakeIdP{t: t, clientID: "exhale", secret: "s3cret-" + auth.Random(8), grants: map[string]idpGrant{}, tokens: map[string]idpUser{}}
	mux := http.NewServeMux()
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	iss := p.srv.URL
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": iss, "authorization_endpoint": iss + "/authorize", "token_endpoint": iss + "/token",
			"jwks_uri": iss + "/jwks", "userinfo_endpoint": iss + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		pub := idpKey().PublicKey
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": b64.EncodeToString(pub.N.Bytes()), "e": b64.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("GET /userinfo", p.userinfo)
	return p
}

func (p *fakeIdP) set(f func(*fakeIdP)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(p)
}

func (p *fakeIdP) authorize(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	q := r.URL.Query()
	p.lastAuth = q
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || q.Get("client_id") != p.clientID || q.Get("response_type") != "code" ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	out := url.Values{"state": {q.Get("state")}}
	if p.deny {
		out.Set("error", "access_denied")
	} else {
		code := auth.Random(16)
		p.grants[code] = idpGrant{user: p.user, challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirectTo: q.Get("redirect_uri")}
		out.Set("code", code)
	}
	back.RawQuery = out.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (p *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = r.ParseForm()
	refuse := func(code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": code})
	}
	id, secret, basic := r.BasicAuth()
	if !basic {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != p.clientID || secret != p.secret {
		refuse("invalid_client")
		return
	}
	g, ok := p.grants[r.PostForm.Get("code")]
	delete(p.grants, r.PostForm.Get("code"))
	if !ok || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("redirect_uri") != g.redirectTo {
		refuse("invalid_grant")
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if b64.EncodeToString(sum[:]) != g.challenge {
		p.pkceFailures++
		refuse("invalid_grant")
		return
	}
	now := time.Now()
	claims := map[string]any{
		"iss": p.srv.URL, "sub": g.user.Sub, "aud": p.clientID, "nonce": g.nonce,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
	}
	if !p.userinfoOnly {
		claims["email"], claims["name"] = g.user.Email, g.user.Name
		if g.user.Verified != nil {
			claims["email_verified"] = g.user.Verified
		}
	}
	switch p.tamper {
	case "nonce":
		claims["nonce"] = "someone-elses-nonce"
	case "audience":
		claims["aud"] = "another-app"
	case "expired":
		claims["exp"] = now.Add(-time.Hour).Unix()
	case "issuer":
		claims["iss"] = "https://evil.example"
	}
	access := auth.Random(16)
	p.tokens[access] = g.user
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": access, "token_type": "Bearer", "expires_in": 300,
		"id_token": p.sign(claims, p.tamper == "signature"),
	})
}

func (p *fakeIdP) userinfo(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	u, ok := p.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	out := map[string]any{"sub": u.Sub, "email": u.Email, "name": u.Name}
	if u.Verified != nil {
		out["email_verified"] = u.Verified
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// sign returns an RS256 JWT. forged signs with a key the provider does not
// publish.
func (p *fakeIdP) sign(claims map[string]any, forged bool) string {
	key := idpKey()
	if forged {
		key, _ = rsa.GenerateKey(rand.Reader, 2048)
	}
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	body, _ := json.Marshal(claims)
	signed := b64.EncodeToString(head) + "." + b64.EncodeToString(body)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		p.t.Fatal(err)
	}
	return signed + "." + b64.EncodeToString(sig)
}

// --- an Exhale server wired to the fake provider -----------------------------

type ssoEnv struct {
	srv *httptest.Server
	svc *app.Service
	idp *fakeIdP
}

func newService(t *testing.T) *app.Service {
	t.Helper()
	st, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return app.New(st, bus.New(), nil, nil)
}

// serve starts an Exhale server over svc that signs in with idp. opt may
// change the config; it gets the server's own URL.
func serve(t *testing.T, svc *app.Service, idp *fakeIdP, opt func(cfg *Config, self string)) *httptest.Server {
	t.Helper()
	cfg := Config{Version: "test", Telegram: fakeTelegram{}}
	if idp != nil {
		sso, err := auth.NewOIDC(auth.OIDCConfig{Issuer: idp.srv.URL, ClientID: idp.clientID, ClientSecret: idp.secret, Name: "Authentik"})
		if err != nil {
			t.Fatal(err)
		}
		cfg.OIDC = sso
	}
	srv := httptest.NewUnstartedServer(nil)
	if opt != nil {
		opt(&cfg, "http://"+srv.Listener.Addr().String())
	}
	h, _, err := New(svc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = h
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func ssoServer(t *testing.T, opt func(cfg *Config, self string)) *ssoEnv {
	t.Helper()
	e := &ssoEnv{svc: newService(t), idp: newIdP(t)}
	e.srv = serve(t, e.svc, e.idp, opt)
	return e
}

// withOwner creates Alex, the owner, with a password.
func (e *ssoEnv) withOwner(t *testing.T) *ssoEnv {
	t.Helper()
	if _, err := auth.Setup(context.Background(), e.svc, alex, true); err != nil {
		t.Fatal(err)
	}
	return e
}

// invite returns an invite code made by the owner.
func (e *ssoEnv) invite(t *testing.T) string {
	t.Helper()
	owner, err := e.svc.Owner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := auth.CreateInvite(context.Background(), e.svc.Store(), owner, "member")
	if err != nil {
		t.Fatal(err)
	}
	return code
}

const ssoStartPath = "/api/v1/auth/oidc/start"

// nav opens a page like a browser's address bar does, following redirects
// through the provider and back. It returns where it ended (path and query).
func (b *browser) nav(target string) string {
	b.t.Helper()
	if strings.HasPrefix(target, "/") {
		target = b.base + target
	}
	req, _ := http.NewRequest("GET", target, nil)
	for k, v := range b.hdr {
		req.Header[k] = v
	}
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	if got := "http://" + res.Request.URL.Host; got != b.base {
		b.t.Fatalf("navigation to %s ended outside Exhale, at %s", target, res.Request.URL)
	}
	if res.StatusCode != 200 {
		b.t.Fatalf("navigation to %s ended with status %d at %s", target, res.StatusCode, res.Request.URL)
	}
	return res.Request.URL.RequestURI()
}

// hop makes one request without following its redirect.
func (b *browser) hop(target string) *http.Response {
	b.t.Helper()
	if strings.HasPrefix(target, "/") {
		target = b.base + target
	}
	c := &http.Client{Jar: b.c.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", target, nil)
	for k, v := range b.hdr {
		req.Header[k] = v
	}
	res, err := c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	res.Body.Close()
	return res
}

// user returns the signed-in person, or nil.
func (b *browser) user() map[string]any {
	b.t.Helper()
	_, s := b.do("GET", "/api/v1/auth/session", nil)
	u, _ := s["user"].(map[string]any)
	return u
}

func (b *browser) signedIn() bool {
	code, _ := b.do("GET", "/api/v1/household", nil)
	return code == 200
}

func (b *browser) members() int {
	b.t.Helper()
	_, h := b.do("GET", "/api/v1/household", nil)
	list, _ := h["members"].([]any)
	return len(list)
}

func idpHost(e *ssoEnv) string { return strings.TrimPrefix(e.idp.srv.URL, "http://") }

// --- tests --------------------------------------------------------------------

// A member with a password signs in through the provider: the verified
// email finds the account and links it.
func TestSSOLinksByVerifiedEmail(t *testing.T) {
	e := ssoServer(t, nil).withOwner(t)
	b := newBrowser(t, e.srv.URL)
	if _, s := b.do("GET", "/api/v1/auth/session", nil); s["sso"].(map[string]any)["name"] != "Authentik" || s["passwordLoginDisabled"] != false {
		t.Fatalf("the session must announce single sign-on: %v", s)
	}
	e.idp.set(func(p *fakeIdP) {
		p.user = idpUser{Sub: "u-alex", Email: "Alex@Example.com", Verified: true, Name: "Alex at work"}
	})
	if got := b.nav(ssoStartPath); got != "/" {
		t.Fatalf("sign-in should land on the app, got %s", got)
	}
	u := b.user()
	if u == nil || u["email"] != "alex@example.com" || u["name"] != "Alex" || u["role"] != "owner" || u["ssoHost"] != idpHost(e) || u["hasPassword"] != true {
		t.Fatalf("should be Alex, linked to the provider: %v", u)
	}
	if b.members() != 1 {
		t.Fatal("linking must not create a member")
	}

	// What went to the provider: code flow, PKCE S256, state, nonce and a
	// redirect URI on this server.
	q := e.idp.lastAuth
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) < 43 ||
		len(q.Get("state")) < 32 || len(q.Get("nonce")) < 32 || q.Get("redirect_uri") != e.srv.URL+"/api/v1/auth/oidc/callback" ||
		!strings.Contains(q.Get("scope"), "openid") || !strings.Contains(q.Get("scope"), "email") {
		t.Fatalf("authorization request: %v", q)
	}
	if e.idp.pkceFailures != 0 {
		t.Fatal("the verifier sent to the token endpoint must match the challenge")
	}

	// The password keeps working next to it.
	b.do("POST", "/api/v1/auth/logout", nil)
	if code, _ := b.do("POST", "/api/v1/auth/login", map[string]string{"email": alex.Email, "password": alex.Password}); code != 200 {
		t.Fatalf("password login after linking: %d", code)
	}
}

func TestSSOUnverifiedEmailIsRefused(t *testing.T) {
	for name, verified := range map[string]any{"false": false, "absent": nil, "string false": "false"} {
		t.Run(name, func(t *testing.T) {
			e := ssoServer(t, nil).withOwner(t)
			b := newBrowser(t, e.srv.URL)
			e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-mallory", Email: alex.Email, Verified: verified} })
			if got := b.nav(ssoStartPath); got != "/?error=sso_unverified" {
				t.Fatalf("an unverified email must not reach Alex's account, got %s", got)
			}
			if b.signedIn() {
				t.Fatal("signed in with an unverified email")
			}
			// Not even with an invite: the email is someone's account.
			if got := b.nav(ssoStartPath + "?invite=" + e.invite(t)); !strings.HasSuffix(got, "?error=sso_unverified") || b.signedIn() {
				t.Fatalf("invite plus an unverified email of an existing account: %s", got)
			}
			// Nothing was linked by the refusals: the real Alex still gets in.
			e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-alex", Email: alex.Email, Verified: "true"} })
			if got := b.nav(ssoStartPath); got != "/" || b.user()["role"] != "owner" {
				t.Fatalf("verified sign-in after a refused one: %s", got)
			}
		})
	}
}

func TestSSOUnknownEmailNeedsAnInvite(t *testing.T) {
	e := ssoServer(t, nil).withOwner(t)
	b := newBrowser(t, e.srv.URL)
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-stranger", Email: "stranger@example.com", Verified: true} })
	if got := b.nav(ssoStartPath); got != "/?error=sso_no_account" {
		t.Fatalf("a stranger must be told to ask for an invite, got %s", got)
	}
	if b.signedIn() {
		t.Fatal("the provider signing someone in must not create an account")
	}
	owner := newBrowser(t, e.srv.URL)
	owner.do("POST", "/api/v1/auth/login", map[string]string{"email": alex.Email, "password": alex.Password})
	if owner.members() != 1 {
		t.Fatal("a member was created for a stranger")
	}
	// No email at all is no different.
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-nobody"} })
	if got := b.nav(ssoStartPath); got != "/?error=sso_no_account" {
		t.Fatalf("an identity without email: %s", got)
	}
}

func TestSSOJoinWithInvite(t *testing.T) {
	e := ssoServer(t, nil).withOwner(t)
	code := e.invite(t)
	guest := newBrowser(t, e.srv.URL)
	// The invite is what lets Marta in, so her email needs no verification.
	e.idp.set(func(p *fakeIdP) {
		p.user = idpUser{Sub: "u-marta", Email: "Marta@Example.com", Verified: false, Name: "Marta"}
	})
	if got := guest.nav(ssoStartPath + "?invite=" + code); got != "/" {
		t.Fatalf("join with an invite: %s", got)
	}
	u := guest.user()
	if u == nil || u["name"] != "Marta" || u["email"] != "marta@example.com" || u["role"] != "member" || u["hasPassword"] != false || u["ssoHost"] != idpHost(e) {
		t.Fatalf("Marta's account: %v", u)
	}
	if guest.members() != 2 {
		t.Fatal("the household should have two members")
	}

	// The invite worked once.
	other := newBrowser(t, e.srv.URL)
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-other", Email: "other@example.com", Verified: true} })
	if got := other.nav(ssoStartPath + "?invite=" + code); got != "/?error=sso_invite" || other.signedIn() {
		t.Fatalf("a used invite: %s", got)
	}
	if got := other.nav(ssoStartPath + "?invite=../../etc"); got != "/?error=sso_invite" {
		t.Fatalf("a malformed invite: %s", got)
	}

	// Marta comes back without the invite, and an invite she is handed
	// later is not spent on her.
	spare := e.invite(t)
	guest.do("POST", "/api/v1/auth/logout", nil)
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-marta", Email: "marta@example.com", Verified: false} })
	if got := guest.nav(ssoStartPath + "?invite=" + spare); got != "/" || guest.user()["name"] != "Marta" || guest.members() != 2 {
		t.Fatalf("Marta signing in again: %s", got)
	}
	if _, err := auth.LookupInvite(context.Background(), e.svc.Store(), spare); err != nil {
		t.Fatalf("the spare invite was spent: %v", err)
	}
}

func TestSSOFirstRunSetup(t *testing.T) {
	e := ssoServer(t, nil)
	b := newBrowser(t, e.srv.URL)
	e.idp.set(func(p *fakeIdP) {
		p.user = idpUser{Sub: "u-alex", Email: "alex@example.com", Verified: true, Name: "Alex"}
	})

	// The household needs a currency and a time zone, which the web sends.
	if got := b.nav(ssoStartPath); got != "/?error=sso_setup" || b.signedIn() {
		t.Fatalf("setup without currency: %s", got)
	}
	if got := b.nav(ssoStartPath + "?currency=EUR&timezone=Europe/Madrid&locale=es"); got != "/" {
		t.Fatalf("first-run setup: %s", got)
	}
	u := b.user()
	if u == nil || u["role"] != "owner" || u["name"] != "Alex" || u["email"] != "alex@example.com" || u["hasPassword"] != false {
		t.Fatalf("the first person in is the owner: %v", u)
	}
	_, h := b.do("GET", "/api/v1/household", nil)
	if hh := h["household"].(map[string]any); hh["currency"] != "EUR" || hh["timezone"] != "Europe/Madrid" {
		t.Fatalf("household: %v", hh)
	}
	if _, s := b.do("GET", "/api/v1/auth/session", nil); s["setupRequired"] != false {
		t.Fatal("setup must be done once an identity is linked")
	}

	// Setup is over: the next stranger does not become anything.
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-second", Email: "second@example.com", Verified: true} })
	second := newBrowser(t, e.srv.URL)
	if got := second.nav(ssoStartPath + "?currency=USD&timezone=UTC"); got != "/?error=sso_no_account" {
		t.Fatalf("second person after setup: %s", got)
	}
}

// From another machine the first run asks for the setup code, as the
// password wizard does.
func TestSSOFirstRunSetupNeedsCodeRemotely(t *testing.T) {
	e := ssoServer(t, func(cfg *Config, self string) { cfg.PublicURL = self })
	b := newBrowser(t, e.srv.URL)
	b.hdr.Set("X-Forwarded-For", "203.0.113.7")
	e.idp.set(func(p *fakeIdP) {
		p.user = idpUser{Sub: "u-alex", Email: "alex@example.com", Verified: true, Name: "Alex"}
	})
	start := ssoStartPath + "?currency=EUR&timezone=Europe/Madrid"

	if got := b.nav(start); got != "/?error=sso_setup_code" {
		t.Fatalf("remote setup without the code: %s", got)
	}
	if got := b.nav(start + "&setup_code=wrongwrongwrong"); got != "/?error=sso_setup_code" {
		t.Fatalf("remote setup with a wrong code: %s", got)
	}
	if e.idp.lastAuth != nil {
		t.Fatal("a wrong setup code should not even reach the provider")
	}
	// The code is checked again when the provider sends the person back: one
	// that stopped being valid in between does not finish the setup.
	stale, _ := auth.NewSetupCode(context.Background(), e.svc.Store())
	cb := b.follow(start + "&setup_code=" + stale)
	code, _ := auth.NewSetupCode(context.Background(), e.svc.Store())
	if got := b.nav(cb.String()); got != "/?error=sso_setup_code" || b.signedIn() {
		t.Fatalf("a setup code replaced during the sign-in: %s", got)
	}
	if got := b.nav(start + "&setup_code=" + code); got != "/" || b.user()["role"] != "owner" {
		t.Fatalf("remote setup with the code: %s", got)
	}
}

// A household made with `exhale init` has an owner without login: the
// first person through the provider becomes that owner.
func TestSSOFirstRunKeepsInitHousehold(t *testing.T) {
	e := ssoServer(t, nil)
	owner, err := e.svc.Bootstrap(context.Background(), app.BootstrapInput{HouseholdName: "Casa", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid"})
	if err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, e.srv.URL)
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-alex", Email: "alex@example.com", Name: "A. Lex"} })
	if got := b.nav(ssoStartPath); got != "/" {
		t.Fatalf("setup on an existing household: %s", got)
	}
	if u := b.user(); u["id"] != owner.MemberID || u["name"] != "Alex" || u["email"] != "alex@example.com" || b.members() != 1 {
		t.Fatalf("the existing owner should have been linked: %v", u)
	}
}

// follow walks start → provider and returns the callback URL the provider
// sends the browser to, without visiting it.
func (b *browser) follow(start string) *url.URL {
	b.t.Helper()
	toIdP := b.hop(start)
	if toIdP.StatusCode != http.StatusFound {
		b.t.Fatalf("start: status %d", toIdP.StatusCode)
	}
	back := b.hop(toIdP.Header.Get("Location"))
	u, err := url.Parse(back.Header.Get("Location"))
	if err != nil || u.Query().Get("code") == "" {
		b.t.Fatalf("the provider did not send a code: %q", back.Header.Get("Location"))
	}
	return u
}

func TestSSOStateAndPKCE(t *testing.T) {
	e := ssoServer(t, nil).withOwner(t)
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-alex", Email: alex.Email, Verified: true} })

	t.Run("state mismatch", func(t *testing.T) {
		b := newBrowser(t, e.srv.URL)
		cb := b.follow(ssoStartPath)
		good := cb.String()
		q := cb.Query()
		q.Set("state", "forged-state")
		cb.RawQuery = q.Encode()
		if got := b.nav(cb.String()); got != "/?error=sso_failed" || b.signedIn() {
			t.Fatalf("a callback with another state must be refused: %s", got)
		}
		// The attempt is spent: the genuine callback no longer works either.
		if got := b.nav(good); got != "/?error=sso_expired" || b.signedIn() {
			t.Fatalf("an attempt must work once: %s", got)
		}
	})

	t.Run("callback without an attempt", func(t *testing.T) {
		victim := newBrowser(t, e.srv.URL)
		cb := newBrowser(t, e.srv.URL).follow(ssoStartPath) // the attacker's own, valid callback
		if got := victim.nav(cb.String()); got != "/?error=sso_expired" || victim.signedIn() {
			t.Fatalf("a callback in a browser that did not start it must be refused: %s", got)
		}
	})

	t.Run("replayed callback", func(t *testing.T) {
		b := newBrowser(t, e.srv.URL)
		cb := b.follow(ssoStartPath)
		cookies := b.c.Jar.Cookies(cb)
		if got := b.nav(cb.String()); got != "/" {
			t.Fatalf("callback: %s", got)
		}
		b.do("POST", "/api/v1/auth/logout", nil)
		if got := b.nav(cb.String()); got != "/?error=sso_expired" || b.signedIn() {
			t.Fatalf("a callback must not work twice: %s", got)
		}
		// Not even for someone who kept the attempt's cookie: the server
		// forgot the attempt when it was used.
		for _, c := range cookies {
			if c.Name == oidcCookie {
				c.Path = oidcPrefix
				b.c.Jar.SetCookies(cb, []*http.Cookie{c})
			}
		}
		if len(b.c.Jar.Cookies(cb)) == 0 {
			t.Fatal("the test should have put the cookie back")
		}
		if got := b.nav(cb.String()); got != "/?error=sso_expired" || b.signedIn() {
			t.Fatalf("a callback must not work twice with its cookie restored: %s", got)
		}
	})

	t.Run("stolen code fails PKCE", func(t *testing.T) {
		victim, attacker := newBrowser(t, e.srv.URL), newBrowser(t, e.srv.URL)
		stolen := victim.follow(ssoStartPath).Query().Get("code")
		own := attacker.follow(ssoStartPath)
		q := own.Query()
		q.Set("code", stolen) // the attacker's state, the victim's code
		own.RawQuery = q.Encode()
		before := e.idp.pkceFailures
		if got := attacker.nav(own.String()); got != "/?error=sso_failed" || attacker.signedIn() {
			t.Fatalf("a code from another attempt must be refused: %s", got)
		}
		if e.idp.pkceFailures != before+1 {
			t.Fatal("the provider should have refused the code for its PKCE verifier")
		}
	})

	t.Run("cancelled at the provider", func(t *testing.T) {
		b := newBrowser(t, e.srv.URL)
		e.idp.set(func(p *fakeIdP) { p.deny = true })
		defer e.idp.set(func(p *fakeIdP) { p.deny = false })
		if got := b.nav(ssoStartPath); got != "/?error=sso_denied" || b.signedIn() {
			t.Fatalf("cancelled sign-in: %s", got)
		}
	})
}

// The ID token must be for this attempt (nonce), this app (audience), this
// provider (issuer, signature) and still valid.
func TestSSOIDTokenIsVerified(t *testing.T) {
	for _, tamper := range []string{"nonce", "audience", "expired", "issuer", "signature"} {
		t.Run(tamper, func(t *testing.T) {
			e := ssoServer(t, nil).withOwner(t)
			b := newBrowser(t, e.srv.URL)
			e.idp.set(func(p *fakeIdP) {
				p.user = idpUser{Sub: "u-alex", Email: alex.Email, Verified: true}
				p.tamper = tamper
			})
			if got := b.nav(ssoStartPath); got != "/?error=sso_failed" || b.signedIn() {
				t.Fatalf("an ID token with a wrong %s must be refused: %s", tamper, got)
			}
			e.idp.set(func(p *fakeIdP) { p.tamper = "" })
			if got := b.nav(ssoStartPath); got != "/" {
				t.Fatalf("the untampered token should work: %s", got)
			}
		})
	}
}

func TestSSONextIsAPathOnThisSite(t *testing.T) {
	e := ssoServer(t, nil).withOwner(t)
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-alex", Email: alex.Email, Verified: true} })
	for _, next := range []string{"https://evil.example/", "//evil.example/", `/\evil.example`, `\\evil.example`, "javascript:alert(1)", "evil.example", "/ok\n/evil", "/\t/evil.example", "http:/evil.example"} {
		b := newBrowser(t, e.srv.URL)
		res := b.hop(ssoStartPath + "?next=" + url.QueryEscape(next))
		if res.StatusCode != http.StatusBadRequest || res.Header.Get("Location") != "" {
			t.Errorf("next=%q must be refused, got %d → %q", next, res.StatusCode, res.Header.Get("Location"))
		}
	}
	b := newBrowser(t, e.srv.URL)
	if got := b.nav(ssoStartPath + "?next=" + url.QueryEscape("/subscriptions?status=active")); got != "/subscriptions?status=active" || !b.signedIn() {
		t.Fatalf("a path on this site should be where sign-in lands: %s", got)
	}

	for in, want := range map[string]string{"": "/", "/": "/", "/calendar?month=2026-11": "/calendar?month=2026-11", "/a b": "/a%20b"} {
		if got, ok := safeNext(in); !ok || got != want {
			t.Errorf("safeNext(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// Once linked, the identity is the member: the provider can change the
// email, even to someone else's, and nobody else can take the link.
func TestSSOLinkSurvivesEmailChange(t *testing.T) {
	e := ssoServer(t, nil).withOwner(t)
	alexB := newBrowser(t, e.srv.URL)
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-alex", Email: alex.Email, Verified: true} })
	alexB.nav(ssoStartPath)
	alexID := alexB.user()["id"]

	marta := newBrowser(t, e.srv.URL)
	e.idp.set(func(p *fakeIdP) {
		p.user = idpUser{Sub: "u-marta", Email: "marta@example.com", Verified: true, Name: "Marta"}
	})
	if got := marta.nav(ssoStartPath + "?invite=" + e.invite(t)); got != "/" {
		t.Fatalf("Marta joins: %s", got)
	}
	martaID := marta.user()["id"]

	for _, email := range []string{"alex@new-job.example", "marta@example.com", ""} {
		alexB.do("POST", "/api/v1/auth/logout", nil)
		e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-alex", Email: email, Verified: false} })
		if got := alexB.nav(ssoStartPath); got != "/" {
			t.Fatalf("sign-in after the email became %q: %s", email, got)
		}
		if u := alexB.user(); u["id"] != alexID || u["email"] != "alex@example.com" {
			t.Fatalf("still Alex, with the email Exhale knows, after it became %q: %v", email, u)
		}
	}

	// Another identity of the same provider that now carries Alex's
	// verified email does not get Alex's account.
	thief := newBrowser(t, e.srv.URL)
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-thief", Email: alex.Email, Verified: true} })
	if got := thief.nav(ssoStartPath); got != "/?error=sso_email_taken" || thief.signedIn() {
		t.Fatalf("a second identity with a linked account's email: %s", got)
	}
	if marta.user()["id"] != martaID || alexB.members() != 2 {
		t.Fatal("the household changed")
	}
}

// After moving to another provider the old links are dead, and members are
// matched again by verified email.
func TestSSOMovingProviders(t *testing.T) {
	e := ssoServer(t, nil).withOwner(t)
	b := newBrowser(t, e.srv.URL)
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "old-alex", Email: alex.Email, Verified: true} })
	b.nav(ssoStartPath)
	id := b.user()["id"]

	keycloak := newIdP(t)
	moved := serve(t, e.svc, keycloak, nil)
	nb := newBrowser(t, moved.URL)
	keycloak.set(func(p *fakeIdP) { p.user = idpUser{Sub: "new-alex", Email: alex.Email, Verified: false} })
	if got := nb.nav(ssoStartPath); got != "/?error=sso_unverified" {
		t.Fatalf("the new provider must verify the email too: %s", got)
	}
	keycloak.set(func(p *fakeIdP) { p.user = idpUser{Sub: "new-alex", Email: alex.Email, Verified: true} })
	if got := nb.nav(ssoStartPath); got != "/" || nb.user()["id"] != id {
		t.Fatalf("Alex at the new provider: %s", got)
	}
	if got := strings.TrimPrefix(keycloak.srv.URL, "http://"); nb.user()["ssoHost"] != got {
		t.Fatalf("the link should now point to the new provider: %v", nb.user())
	}
}

func TestSSOPasswordLoginDisabled(t *testing.T) {
	e := ssoServer(t, func(cfg *Config, _ string) { cfg.DisablePasswordLogin = true }).withOwner(t)
	b := newBrowser(t, e.srv.URL)
	if _, s := b.do("GET", "/api/v1/auth/session", nil); s["passwordLoginDisabled"] != true {
		t.Fatalf("the session must say passwords are off: %v", s)
	}
	if code, out := b.do("POST", "/api/v1/auth/login", map[string]string{"email": alex.Email, "password": alex.Password}); code != 403 || !strings.Contains(out["detail"].(string), "Authentik") {
		t.Fatalf("password login must be refused, got %d %v", code, out)
	}
	join := map[string]string{"code": e.invite(t), "name": "Marta", "email": "marta@example.com", "password": "another long password"}
	if code, _ := b.do("POST", "/api/v1/auth/join", join); code != 403 {
		t.Fatalf("joining with a password must be refused, got %d", code)
	}
	if b.signedIn() {
		t.Fatal("signed in with a password")
	}
	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-alex", Email: alex.Email, Verified: true} })
	if got := b.nav(ssoStartPath); got != "/" || !b.signedIn() {
		t.Fatalf("single sign-on with passwords off: %s", got)
	}

	// A fresh instance cannot be set up with a password either.
	fresh := ssoServer(t, func(cfg *Config, _ string) { cfg.DisablePasswordLogin = true })
	if code, _ := newBrowser(t, fresh.srv.URL).do("POST", "/api/v1/auth/setup", alex); code != 403 {
		t.Fatalf("password setup must be refused, got %d", code)
	}

	// Without a provider the flag is ignored, so nobody is locked out.
	svc := newService(t)
	if _, err := auth.Setup(context.Background(), svc, alex, true); err != nil {
		t.Fatal(err)
	}
	plain := serve(t, svc, nil, func(cfg *Config, _ string) { cfg.DisablePasswordLogin = true })
	pb := newBrowser(t, plain.URL)
	if _, s := pb.do("GET", "/api/v1/auth/session", nil); s["passwordLoginDisabled"] != false || s["sso"] != nil {
		t.Fatalf("no provider: passwords stay on: %v", s)
	}
	if code, _ := pb.do("POST", "/api/v1/auth/login", map[string]string{"email": alex.Email, "password": alex.Password}); code != 200 {
		t.Fatalf("password login without a provider: %d", code)
	}
	if res := pb.hop(ssoStartPath); res.StatusCode != http.StatusNotFound {
		t.Fatalf("start without a provider: %d", res.StatusCode)
	}
}

// A member made through the provider has no password; bumping its
// session_version (the CLI's reset-password does) signs it out everywhere.
func TestSSOSessionVersionSignsOut(t *testing.T) {
	e := ssoServer(t, nil).withOwner(t)
	b := newBrowser(t, e.srv.URL)
	e.idp.set(func(p *fakeIdP) {
		p.user = idpUser{Sub: "u-marta", Email: "marta@example.com", Verified: true, Name: "Marta"}
	})
	if got := b.nav(ssoStartPath + "?invite=" + e.invite(t)); got != "/" || !b.signedIn() {
		t.Fatalf("join: %s", got)
	}
	other := newBrowser(t, e.srv.URL)
	other.nav(ssoStartPath)
	if !other.signedIn() {
		t.Fatal("second session")
	}
	// No password to guess: password login finds nothing to compare with.
	if code, _ := newBrowser(t, e.srv.URL).do("POST", "/api/v1/auth/login", map[string]string{"email": "marta@example.com", "password": ""}); code != 401 {
		t.Fatalf("an account without password must not accept an empty one: %d", code)
	}

	ctx := context.Background()
	if err := auth.SetPassword(ctx, e.svc.Store(), b.user()["id"].(string), "break glass password"); err != nil {
		t.Fatal(err)
	}
	if b.signedIn() || other.signedIn() {
		t.Fatal("a session_version bump must sign the member out everywhere")
	}
	if got := b.nav(ssoStartPath); got != "/" || !b.signedIn() || other.signedIn() {
		t.Fatalf("signing in again: %s", got)
	}
}

// Where the provider sends the code back comes from --public-url, or from
// this machine's own address; never from a Host header someone else chose.
func TestSSORedirectURI(t *testing.T) {
	e := ssoServer(t, nil).withOwner(t)
	remote := newBrowser(t, e.srv.URL)
	remote.hdr.Set("X-Forwarded-For", "203.0.113.7")
	if res := remote.hop(ssoStartPath); res.Header.Get("Location") != "/?error=sso_config" {
		t.Fatalf("behind a proxy without a public URL: %q", res.Header.Get("Location"))
	}
	spoof, _ := http.NewRequest("GET", e.srv.URL+ssoStartPath, nil)
	spoof.Host = "evil.example"
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(spoof)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Header.Get("Location") != "/?error=sso_config" {
		t.Fatalf("a foreign Host header must not become the redirect URI: %q", res.Header.Get("Location"))
	}

	pub := ssoServer(t, func(cfg *Config, _ string) { cfg.PublicURL = "https://exhale.example.com/" })
	pb := newBrowser(t, pub.srv.URL)
	pb.hdr.Set("X-Forwarded-For", "203.0.113.7")
	res = pb.hop(ssoStartPath + "?timezone=UTC&currency=EUR&setup_code=" + func() string {
		c, _ := auth.NewSetupCode(context.Background(), pub.svc.Store())
		return c
	}())
	to, _ := url.Parse(res.Header.Get("Location"))
	if got := to.Query().Get("redirect_uri"); got != "https://exhale.example.com/api/v1/auth/oidc/callback" {
		t.Fatalf("redirect URI with a public URL: %q (%s)", got, res.Header.Get("Location"))
	}
	var flow *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == oidcCookie {
			flow = c
		}
	}
	if flow == nil || !flow.HttpOnly || !flow.Secure || flow.SameSite != http.SameSiteLaxMode || flow.MaxAge <= 0 || flow.MaxAge > 600 || flow.Path != "/api/v1/auth/oidc/" {
		t.Fatalf("the attempt's cookie: %+v", flow)
	}
	for _, v := range []string{to.Query().Get("state"), to.Query().Get("nonce")} {
		if v == "" || strings.Contains(flow.Value, v) {
			t.Fatal("state and nonce must not be readable from the cookie")
		}
	}

	for _, bad := range []string{"exhale.example.com", "https://example.com/exhale", "ftp://exhale.example.com"} {
		if _, _, err := New(newService(t), Config{Version: "test", PublicURL: bad}); err == nil {
			t.Fatalf("the public URL %q must be refused at startup", bad)
		}
	}
}

// Providers that keep email out of the ID token (Authelia) and issuers
// written with or without the trailing slash both work.
func TestSSOUserinfoAndIssuerSlash(t *testing.T) {
	svc := newService(t)
	if _, err := auth.Setup(context.Background(), svc, alex, true); err != nil {
		t.Fatal(err)
	}
	idp := newIdP(t)
	srv := serve(t, svc, nil, func(cfg *Config, _ string) {
		sso, err := auth.NewOIDC(auth.OIDCConfig{Issuer: idp.srv.URL + "/", ClientID: idp.clientID, ClientSecret: idp.secret})
		if err != nil {
			t.Fatal(err)
		}
		cfg.OIDC = sso
	})
	b := newBrowser(t, srv.URL)
	if _, s := b.do("GET", "/api/v1/auth/session", nil); s["sso"].(map[string]any)["name"] != "Single sign-on" {
		t.Fatalf("default button name: %v", s["sso"])
	}
	idp.set(func(p *fakeIdP) {
		p.user = idpUser{Sub: "u-alex", Email: alex.Email, Verified: true}
		p.userinfoOnly = true
	})
	if got := b.nav(ssoStartPath); got != "/" || b.user()["role"] != "owner" {
		t.Fatalf("claims from userinfo: %s", got)
	}
}

func TestSSOStartIsThrottled(t *testing.T) {
	e := ssoServer(t, nil).withOwner(t)
	b := newBrowser(t, e.srv.URL)
	last := ""
	for range 12 {
		last = b.hop(ssoStartPath).Header.Get("Location")
	}
	if last != "/?error=sso_throttled" {
		t.Fatalf("12 quick starts should be throttled, last went to %q", last)
	}
}

func TestOIDCConfigIsValidated(t *testing.T) {
	for _, c := range []auth.OIDCConfig{
		{Issuer: "https://id.example.com"},
		{ClientID: "exhale"},
		{Issuer: "id.example.com", ClientID: "exhale"},
		{Issuer: "https://id.example.com?x=1", ClientID: "exhale"},
	} {
		if _, err := auth.NewOIDC(c); err == nil {
			t.Errorf("%+v should be refused", c)
		}
	}
}
