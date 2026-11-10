package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Sign-in with an OpenID Connect provider (ADR 0006): authorization code
// flow with PKCE. The provider says who the person is; whether that person
// has an account here is decided by SignInWithIdentity.

// DefaultOIDCName is what the sign-in button says when no name is set.
const DefaultOIDCName = "Single sign-on"

// OIDCConfig is the provider registration, set with flags or environment.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string // empty for a public client (PKCE only)
	Name         string
	Scopes       []string
}

// OIDC talks to the configured provider. Discovery happens on first use and
// is retried until it works, so Exhale starts while the provider is down.
type OIDC struct {
	cfg    OIDCConfig
	client *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
	lastTry  time.Time
	lastErr  error
}

// NewOIDC validates the configuration. It does not touch the network.
func NewOIDC(cfg OIDCConfig) (*OIDC, error) {
	cfg.Issuer = strings.TrimSpace(cfg.Issuer)
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.Issuer == "" || cfg.ClientID == "" {
		return nil, errors.New("single sign-on needs both an issuer URL and a client id")
	}
	u, err := url.Parse(cfg.Issuer)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("the issuer must be an http(s) URL without query, got %q", cfg.Issuer)
	}
	if cfg.Name == "" {
		cfg.Name = DefaultOIDCName
	}
	scopes := []string{oidc.ScopeOpenID}
	for _, s := range cfg.Scopes {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(scopes, s) {
			scopes = append(scopes, s)
		}
	}
	if len(scopes) == 1 {
		scopes = append(scopes, "email", "profile")
	}
	cfg.Scopes = scopes
	return &OIDC{cfg: cfg, client: &http.Client{Timeout: 15 * time.Second}}, nil
}

// Name is the provider's display name for the sign-in button.
func (o *OIDC) Name() string { return o.cfg.Name }

// InsecureIssuer reports whether the issuer is reached over plain HTTP,
// which lets anyone on the network path forge sign-ins.
func (o *OIDC) InsecureIssuer() bool { return strings.HasPrefix(o.cfg.Issuer, "http://") }

// Discover reads the provider's configuration if it is not known yet.
func (o *OIDC) Discover(ctx context.Context) error {
	_, err := o.discover(ctx)
	return err
}

func (o *OIDC) discover(ctx context.Context) (*oidc.Provider, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return o.provider, nil
	}
	// A provider that is down is not asked again by every click.
	if o.lastErr != nil && time.Since(o.lastTry) < 5*time.Second {
		return nil, o.lastErr
	}
	ctx = oidc.ClientContext(ctx, o.client)
	p, err := oidc.NewProvider(ctx, o.cfg.Issuer)
	// Providers disagree on the trailing slash of the issuer (Authentik has
	// one, Keycloak does not); accept either spelling of the same URL.
	var mismatch *oidc.IssuerMismatchError
	if errors.As(err, &mismatch) && strings.TrimSuffix(mismatch.Discovered, "/") == strings.TrimSuffix(o.cfg.Issuer, "/") {
		p, err = oidc.NewProvider(ctx, mismatch.Discovered)
	}
	o.lastTry = time.Now()
	if err != nil {
		o.lastErr = fmt.Errorf("discovery at %s: %s", o.cfg.Issuer, clip(err.Error()))
		return nil, o.lastErr
	}
	o.provider, o.lastErr = p, nil
	return p, nil
}

func (o *OIDC) oauth(p *oidc.Provider, redirectURI string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: o.cfg.ClientID, ClientSecret: o.cfg.ClientSecret,
		Endpoint: p.Endpoint(), RedirectURL: redirectURI, Scopes: o.cfg.Scopes,
	}
}

// AuthURL returns where to send the browser to sign in. state protects the
// callback against forgery, nonce binds the ID token to this attempt and
// verifier is the PKCE secret (only its S256 hash leaves the server here).
func (o *OIDC) AuthURL(ctx context.Context, redirectURI, state, nonce, verifier string) (string, error) {
	p, err := o.discover(ctx)
	if err != nil {
		return "", err
	}
	return o.oauth(p, redirectURI).AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

// Identity is who the provider says signed in.
type Identity struct {
	Issuer        string // as published by the provider's discovery document
	Subject       string // stable id of the person at that provider
	Email         string
	EmailVerified bool
	Name          string
}

// flexBool reads a boolean that some providers send as a string.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	*b = flexBool(strings.EqualFold(strings.Trim(string(data), `"`), "true"))
	return nil
}

type idClaims struct {
	Email         string   `json:"email"`
	EmailVerified flexBool `json:"email_verified"`
	Name          string   `json:"name"`
	Username      string   `json:"preferred_username"`
}

// Exchange trades the authorization code for the person's identity. It
// verifies the ID token (signature, issuer, audience, expiry) and its nonce.
// Errors never contain tokens or the client secret.
func (o *OIDC) Exchange(ctx context.Context, redirectURI, code, verifier, nonce string) (Identity, error) {
	p, err := o.discover(ctx)
	if err != nil {
		return Identity{}, err
	}
	ctx = oidc.ClientContext(ctx, o.client)
	tok, err := o.oauth(p, redirectURI).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) {
			status := 0
			if re.Response != nil {
				status = re.Response.StatusCode
			}
			return Identity{}, fmt.Errorf("the provider refused the code: %s %s (HTTP %d)", re.ErrorCode, clip(re.ErrorDescription), status)
		}
		return Identity{}, fmt.Errorf("code exchange: %s", clip(err.Error()))
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return Identity{}, errors.New("the provider returned no ID token")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: o.cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("ID token: %s", clip(err.Error()))
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(nonce)) != 1 {
		return Identity{}, errors.New("ID token: the nonce does not match this sign-in")
	}
	if idt.Subject == "" {
		return Identity{}, errors.New("ID token: no subject")
	}
	var c idClaims
	if err := idt.Claims(&c); err != nil {
		return Identity{}, errors.New("ID token: unreadable claims")
	}
	// Some providers (Authelia) keep email and name out of the ID token and
	// serve them from the userinfo endpoint.
	if c.Email == "" && p.UserInfoEndpoint() != "" && tok.AccessToken != "" {
		if idt.AccessTokenHash == "" || idt.VerifyAccessToken(tok.AccessToken) == nil {
			if ui, err := p.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil && ui.Subject == idt.Subject {
				name, username := c.Name, c.Username
				_ = ui.Claims(&c)
				c.Name, c.Username = firstOf(c.Name, name), firstOf(c.Username, username)
			}
		}
	}
	return Identity{
		Issuer: issuerOf(p, o.cfg.Issuer), Subject: idt.Subject,
		Email: c.Email, EmailVerified: bool(c.EmailVerified), Name: firstOf(c.Name, c.Username),
	}, nil
}

// issuerOf returns the issuer the provider publishes, which is what links
// are stored under (not the token's own spelling: Google's differs).
func issuerOf(p *oidc.Provider, fallback string) string {
	var doc struct {
		Issuer string `json:"issuer"`
	}
	if err := p.Claims(&doc); err == nil && doc.Issuer != "" {
		return doc.Issuer
	}
	return fallback
}

func firstOf(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// clip keeps provider text short enough for a log line.
func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// Why a provider sign-in is refused. The web shows a sentence for each.
var (
	ErrSSOUnverified = errors.New("the provider has not verified this email")
	ErrSSOEmailTaken = errors.New("this email belongs to an account linked to another identity")
	ErrSSONoAccount  = errors.New("no account for this identity; ask the owner for an invite")
	ErrSSOInvite     = errors.New("the invite has expired or was already used")
	ErrSSOSetupCode  = errors.New("the setup code is wrong or expired")
)

// SSOSignIn is what the browser carried through the provider's pages.
type SSOSignIn struct {
	Invite string // invite code, when joining a household

	// First run only.
	SetupCode string
	Local     bool // request from the server's own machine: no setup code
	Currency  string
	Timezone  string
	Locale    string
}

// SignInWithIdentity returns the member of a provider identity. These rules
// are the security core of single sign-on (ADR 0006):
//
//  1. An identity already linked to a member is that member, whatever its
//     email is today.
//  2. On a fresh instance (nobody can sign in yet) the identity becomes the
//     owner, with the same setup-code rule as the password wizard.
//  3. An identity whose email is a member's email is linked to that member
//     only if the provider says the email is verified, and only if the
//     member is not linked to another identity of the same provider.
//  4. Otherwise an account is created only with a valid invite.
//
// Authenticating at the provider never creates an account by itself.
func SignInWithIdentity(ctx context.Context, svc *app.Service, id Identity, in SSOSignIn) (app.Actor, error) {
	st := svc.Store()
	if id.Issuer == "" || id.Subject == "" {
		return app.Actor{}, errors.New("identity without issuer or subject")
	}
	m, err := st.Q.MemberByIdentity(ctx, db.MemberByIdentityParams{OidcIssuer: id.Issuer, OidcSubject: id.Subject})
	if err == nil {
		return app.MemberActor(store.MemberFromRow(m), app.ScopeTrusted), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return app.Actor{}, err
	}
	email, _ := NormalizeEmail(id.Email) // empty when missing or malformed

	if setup, err := SetupRequired(ctx, st); err != nil {
		return app.Actor{}, err
	} else if setup {
		return setupWithIdentity(ctx, svc, id, email, in)
	}

	if email != "" {
		m, err := st.Q.MemberByEmail(ctx, email)
		switch {
		case err == nil:
			// A link to a provider that is no longer configured is dead, so
			// the member can be matched again after moving providers.
			if m.OidcSubject != "" && m.OidcIssuer == id.Issuer {
				return app.Actor{}, ErrSSOEmailTaken
			}
			if !id.EmailVerified {
				return app.Actor{}, ErrSSOUnverified
			}
			if err := linkIdentity(ctx, st.Q, m.ID, id); err != nil {
				return app.Actor{}, err
			}
			return memberActor(ctx, st, m.ID)
		case !errors.Is(err, sql.ErrNoRows):
			return app.Actor{}, err
		}
	}
	if strings.TrimSpace(in.Invite) == "" {
		return app.Actor{}, ErrSSONoAccount
	}
	return joinWithIdentity(ctx, st, id, email, in.Invite)
}

// CheckSetupCode reports whether code is the current setup code.
func CheckSetupCode(ctx context.Context, st *store.Store, code string) bool {
	return checkSetupCode(ctx, st, code)
}

// linkIdentity ties a member to an identity. It never replaces a link to
// the same provider.
func linkIdentity(ctx context.Context, q *db.Queries, memberID string, id Identity) error {
	n, err := q.LinkMemberIdentity(ctx, db.LinkMemberIdentityParams{Issuer: id.Issuer, Subject: id.Subject, ID: memberID})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSSOEmailTaken
	}
	return nil
}

func setupWithIdentity(ctx context.Context, svc *app.Service, id Identity, email string, in SSOSignIn) (app.Actor, error) {
	st := svc.Store()
	if !in.Local && !checkSetupCode(ctx, st, in.SetupCode) {
		return app.Actor{}, ErrSSOSetupCode
	}
	owner, err := svc.Owner(ctx)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		owner, err = svc.Bootstrap(ctx, app.BootstrapInput{
			OwnerName: identityName(id, email), OwnerEmail: email,
			Currency: in.Currency, Timezone: in.Timezone, Locale: in.Locale,
		})
		if err != nil {
			return app.Actor{}, err
		}
	case err != nil:
		return app.Actor{}, err
	default:
		// A household made with `exhale init`: its owner keeps the name and
		// takes the email of the identity, unless someone else has it.
		if m, err := st.Q.GetMember(ctx, owner.MemberID); err == nil && email != "" && email != m.Email {
			if _, err := st.Q.MemberByEmail(ctx, email); errors.Is(err, sql.ErrNoRows) {
				if err := st.Q.SetMemberProfile(ctx, db.SetMemberProfileParams{Name: m.Name, Email: email, ID: m.ID}); err != nil {
					return app.Actor{}, err
				}
			}
		}
	}
	if err := linkIdentity(ctx, st.Q, owner.MemberID, id); err != nil {
		return app.Actor{}, err
	}
	_ = st.Q.PutSetting(ctx, db.PutSettingParams{Key: settingSetupCode, Value: ""})
	return memberActor(ctx, st, owner.MemberID)
}

func joinWithIdentity(ctx context.Context, st *store.Store, id Identity, email, invite string) (app.Actor, error) {
	codeHash := Hash(strings.TrimSpace(invite))
	memberID := domain.NewID(domain.PrefixMember)
	err := st.Tx(ctx, func(q *db.Queries) error {
		now := store.FormatTime(time.Now())
		inv, err := q.GetInvite(ctx, db.GetInviteParams{CodeHash: codeHash, ExpiresAt: now})
		if err != nil {
			return ErrSSOInvite
		}
		if n, err := q.UseInvite(ctx, db.UseInviteParams{UsedAt: store.Ptr(now), CodeHash: codeHash}); err != nil || n != 1 {
			return ErrSSOInvite
		}
		if err := q.CreateMember(ctx, db.CreateMemberParams{ID: memberID, HouseholdID: inv.HouseholdID, Name: identityName(id, email), Email: email, Role: inv.Role, CreatedAt: now}); err != nil {
			return err
		}
		return linkIdentity(ctx, q, memberID, id)
	})
	if err != nil {
		return app.Actor{}, err
	}
	return memberActor(ctx, st, memberID)
}

// identityName picks a display name for an account made from an identity.
func identityName(id Identity, email string) string {
	name := strings.TrimSpace(id.Name)
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	if name == "" {
		name = "Member"
	}
	if utf8.RuneCountInString(name) > 80 {
		name = string([]rune(name)[:80])
	}
	return name
}

// IdentityHost returns the host of the provider a member is linked to, for
// display ("auth.example.com"), or "" if the member has no identity.
func IdentityHost(m db.Member) string {
	if m.OidcSubject == "" {
		return ""
	}
	if u, err := url.Parse(m.OidcIssuer); err == nil && u.Host != "" {
		return u.Host
	}
	return m.OidcIssuer
}
