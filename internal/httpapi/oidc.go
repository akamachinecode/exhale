package httpapi

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/oauth2"

	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/domain"
)

// Sign-in with an OpenID Connect provider (ADR 0006). Two plain routes, as
// the browser navigates to them instead of calling them:
//
//	GET /api/v1/auth/oidc/start     → redirect to the provider
//	GET /api/v1/auth/oidc/callback  → verify, start the session, redirect to the web
//
// What ties the callback to the browser that started is a short-lived
// cookie holding a random id; state, nonce and the PKCE verifier stay in
// the server's memory and work once.

const (
	oidcPrefix   = "/api/v1/auth/oidc/"
	oidcStart    = oidcPrefix + "start"
	oidcCallback = oidcPrefix + "callback"
	oidcCookie   = "exhale_oidc"
	oidcFlowTTL  = 10 * time.Minute
	oidcMaxFlows = 10_000
)

// Error codes the web turns into sentences (web/src/routes/auth.tsx).
const (
	ssoFailed     = "sso_failed"      // state, code or ID token did not verify
	ssoExpired    = "sso_expired"     // no sign-in in progress in this browser
	ssoDenied     = "sso_denied"      // the person cancelled at the provider
	ssoProvider   = "sso_provider"    // the provider is unreachable or misconfigured
	ssoConfig     = "sso_config"      // the server does not know its public URL
	ssoThrottled  = "sso_throttled"   // too many attempts
	ssoUnverified = "sso_unverified"  // email not verified by the provider
	ssoEmailTaken = "sso_email_taken" // the email's account is linked to someone else
	ssoNoAccount  = "sso_no_account"  // no account and no invite
	ssoInvite     = "sso_invite"      // invite expired or used
	ssoSetupCode  = "sso_setup_code"  // first run: wrong setup code
	ssoSetup      = "sso_setup"       // first run: household could not be created
)

// oidcFlow is one sign-in attempt between start and callback.
type oidcFlow struct {
	state, nonce, verifier string
	redirectURI            string
	next                   string
	in                     auth.SSOSignIn
	expires                time.Time
}

// flowStore keeps attempts in memory: they live minutes, and a restart only
// asks the person to click again.
type flowStore struct {
	mu sync.Mutex
	m  map[string]oidcFlow
}

func (s *flowStore) put(f oidcFlow) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]oidcFlow{}
	}
	now := time.Now()
	if len(s.m) >= oidcMaxFlows/2 {
		for k, v := range s.m {
			if v.expires.Before(now) {
				delete(s.m, k)
			}
		}
	}
	if len(s.m) >= oidcMaxFlows {
		return "", false
	}
	id := auth.Random(32)
	f.expires = now.Add(oidcFlowTTL)
	s.m[id] = f
	return id, true
}

// take returns an attempt and forgets it: a callback works once.
func (s *flowStore) take(id string) (oidcFlow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.m[id]
	delete(s.m, id)
	return f, ok && f.expires.After(time.Now())
}

func (a *authAPI) log() *slog.Logger {
	if a.cfg.Log != nil {
		return a.cfg.Log
	}
	return slog.New(slog.DiscardHandler)
}

// passwordOff reports whether password sign-in is disabled. It is only
// honoured with a provider configured, so nobody is locked out by a flag.
func (a *authAPI) passwordOff() bool { return a.cfg.DisablePasswordLogin && a.cfg.OIDC != nil }

func (a *authAPI) passwordRefused() error {
	if !a.passwordOff() {
		return nil
	}
	return huma.Error403Forbidden("Password sign-in is turned off on this server; use " + a.cfg.OIDC.Name())
}

// publicBase returns the origin people reach Exhale at. Without a
// configured public URL only this machine's own address is trusted: a Host
// header from anywhere else must not decide where the provider sends codes.
func (a *authAPI) publicBase(r *http.Request) (string, bool) {
	if a.cfg.PublicURL != "" {
		return strings.TrimRight(a.cfg.PublicURL, "/"), true
	}
	if !isLoopback(r) || !loopbackHost(r.Host) {
		return "", false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host, true
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// validPublicURL checks --public-url: an origin, since Exhale is not served
// under a path.
func validPublicURL(s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("the public URL must look like https://exhale.example.com, got %q", s)
	}
	return nil
}

// safeNext accepts only a path on this site as the place to land after
// signing in: no scheme, no host, nothing a browser could read as one.
func safeNext(next string) (string, bool) {
	if next == "" {
		return "/", true
	}
	if len(next) > 2000 || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, `\`) {
		return "", false
	}
	for _, c := range next {
		if c < 0x20 || c == 0x7f {
			return "", false
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return "", false
	}
	out := u.EscapedPath()
	if !strings.HasPrefix(out, "/") || strings.HasPrefix(out, "//") {
		return "", false
	}
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out, true
}

var inviteCode = regexp.MustCompile(`^[a-z2-7]{8,64}$`)

// ssoFailPath is where the web shows why a sign-in failed: the join page
// for someone holding an invite that still works, the root otherwise.
func ssoFailPath(invite, code string) string {
	if invite != "" && code != ssoInvite {
		return "/join/" + invite + "?error=" + code
	}
	return "/?error=" + code
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func registerOIDC(mux *http.ServeMux, a *authAPI) error {
	if err := validPublicURL(a.cfg.PublicURL); err != nil {
		return err
	}
	mux.HandleFunc("GET "+oidcStart, a.oidcStart)
	mux.HandleFunc("GET "+oidcCallback, a.oidcCallback)
	return nil
}

func (a *authAPI) oidcStart(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if a.cfg.OIDC == nil {
		writeProblem(w, http.StatusNotFound, "single sign-on is not configured")
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	next, ok := safeNext(q.Get("next"))
	if !ok {
		writeProblem(w, http.StatusBadRequest, "next must be a path on this site")
		return
	}
	invite := strings.TrimSpace(q.Get("invite"))
	if invite != "" && !inviteCode.MatchString(invite) {
		http.Redirect(w, r, ssoFailPath("", ssoInvite), http.StatusFound)
		return
	}
	// invite matched inviteCode above, so the path is ours: /join/<base32>.
	fail := func(code string) { http.Redirect(w, r, ssoFailPath(invite, code), http.StatusFound) } //nolint:gosec // G710: not an open redirect, see above
	if !a.ssoStarts.allow(clientIP(ctx)) {
		fail(ssoThrottled)
		return
	}
	base, ok := a.publicBase(r)
	if !ok {
		a.log().Warn("single sign-on needs --public-url (EXHALE_PUBLIC_URL) to be used from another machine")
		fail(ssoConfig)
		return
	}
	st := a.svc.Store()
	in := auth.SSOSignIn{Invite: invite}
	// Fail early, before a trip to the provider, when the answer is already
	// known. Both are checked again in the callback.
	if setup, err := auth.SetupRequired(ctx, st); err == nil && setup {
		in.SetupCode = strings.TrimSpace(q.Get("setup_code"))
		in.Currency, in.Timezone, in.Locale = q.Get("currency"), q.Get("timezone"), q.Get("locale")
		if !isLocal(ctx) && !auth.CheckSetupCode(ctx, st, in.SetupCode) {
			fail(ssoSetupCode)
			return
		}
	} else if invite != "" {
		if _, err := auth.LookupInvite(ctx, st, invite); err != nil {
			fail(ssoInvite)
			return
		}
	}
	flow := oidcFlow{
		state: auth.Random(32), nonce: auth.Random(32), verifier: oauth2.GenerateVerifier(),
		redirectURI: base + oidcCallback, next: next, in: in,
	}
	target, err := a.cfg.OIDC.AuthURL(ctx, flow.redirectURI, flow.state, flow.nonce, flow.verifier)
	if err != nil {
		a.log().Warn("single sign-on: the provider is not usable", "err", err)
		fail(ssoProvider)
		return
	}
	id, ok := a.flows.put(flow)
	if !ok {
		fail(ssoThrottled)
		return
	}
	http.SetCookie(w, a.flowCookie(id, int(oidcFlowTTL.Seconds()), strings.HasPrefix(base, "https://")))
	http.Redirect(w, r, target, http.StatusFound)
}

// flowCookie is Secure under the same rule as the session cookie, and also
// whenever the public URL is https.
func (a *authAPI) flowCookie(value string, maxAge int, https bool) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // G124: Secure depends on how Exhale is served
		Name: oidcCookie, Value: value, Path: oidcPrefix, MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.cfg.SecureCookies || https,
	}
}

func (a *authAPI) oidcCallback(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if a.cfg.OIDC == nil {
		writeProblem(w, http.StatusNotFound, "single sign-on is not configured")
		return
	}
	ctx := r.Context()
	var flow oidcFlow
	found := false
	if c, err := r.Cookie(oidcCookie); err == nil {
		flow, found = a.flows.take(c.Value)
		http.SetCookie(w, a.flowCookie("", -1, false))
	}
	if !found {
		http.Redirect(w, r, ssoFailPath("", ssoExpired), http.StatusFound)
		return
	}
	fail := func(code string) { http.Redirect(w, r, ssoFailPath(flow.in.Invite, code), http.StatusFound) }
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(flow.state)) != 1 {
		a.log().Warn("single sign-on: the callback's state does not match the attempt")
		fail(ssoFailed)
		return
	}
	if e := q.Get("error"); e != "" {
		a.log().Info("single sign-on: the provider returned an error", "error", clipText(e), "description", clipText(q.Get("error_description")))
		if e == "access_denied" {
			fail(ssoDenied)
		} else {
			fail(ssoProvider)
		}
		return
	}
	code := q.Get("code")
	if code == "" {
		fail(ssoFailed)
		return
	}
	id, err := a.cfg.OIDC.Exchange(ctx, flow.redirectURI, code, flow.verifier, flow.nonce)
	if err != nil {
		a.log().Warn("single sign-on: sign-in refused", "err", err)
		fail(ssoFailed)
		return
	}
	in := flow.in
	in.Local = isLocal(ctx)
	actor, err := auth.SignInWithIdentity(ctx, a.svc, id, in)
	if err != nil {
		code := ssoFailed
		switch {
		case errors.Is(err, auth.ErrSSOUnverified):
			code = ssoUnverified
		case errors.Is(err, auth.ErrSSOEmailTaken):
			code = ssoEmailTaken
		case errors.Is(err, auth.ErrSSONoAccount):
			code = ssoNoAccount
		case errors.Is(err, auth.ErrSSOInvite):
			code = ssoInvite
		case errors.Is(err, auth.ErrSSOSetupCode):
			code = ssoSetupCode
		case errors.Is(err, domain.ErrValidation):
			code = ssoSetup
		}
		// The subject is an opaque id at the provider, not a secret.
		a.log().Warn("single sign-on: no account for this identity", "reason", err, "issuer", id.Issuer, "subject", id.Subject)
		fail(code)
		return
	}
	if err := auth.StartSession(ctx, a.sm, a.svc.Store(), actor.MemberID); err != nil {
		a.log().Error("single sign-on: could not start the session", "err", err)
		fail(ssoFailed)
		return
	}
	http.Redirect(w, r, flow.next, http.StatusFound)
}

func clipText(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
