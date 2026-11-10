package oauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// AuthorizeParams are the query parameters of an authorization request.
type AuthorizeParams struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            []string
}

// Request is an authorization request waiting for the member's answer on
// the consent screen. It lives in memory: a restart only asks the person
// to connect again.
type Request struct {
	ID          string
	Client      Client
	RedirectURI string
	State       string
	Challenge   string
	Resource    string
	Issuer      string
	// MaxScope is the most the application asked for.
	MaxScope app.Scope
	expires  time.Time
}

// RedirectHost is where the browser goes after the answer: the consent
// screen must show it (MCP security considerations).
func (r *Request) RedirectHost() string { return redirectHost(r.RedirectURI) }

// Loopback reports whether the answer goes to a program on the person's
// own computer, which any program running there could be.
func (r *Request) Loopback() bool { return LoopbackRedirect(r.RedirectURI) }

// Authorize checks an authorization request. With a nil request the error
// must be shown to the person: the client or its redirect URI is not
// trusted, so nothing may be sent there. With a request and an error, the
// error goes back to the application at its redirect URI. Without error
// the request waits for the consent screen.
//
// issuer is this server's issuer identifier and resource the canonical URI
// of its MCP endpoint, the only resource it issues tokens for.
func (s *Service) Authorize(ctx context.Context, p AuthorizeParams, issuer, resource string) (*Request, error) {
	s.maybeCleanup(ctx)
	if p.ClientID == "" {
		return nil, fail("invalid_request", "client_id is missing")
	}
	c, err := s.Client(ctx, p.ClientID)
	if err != nil {
		return nil, err
	}
	if p.RedirectURI == "" {
		return nil, fail("invalid_request", "redirect_uri is missing")
	}
	if !matchRedirect(c.RedirectURIs, p.RedirectURI) {
		return nil, fail("invalid_request", "redirect_uri is not one of the application's registered addresses")
	}
	r := &Request{Client: c, RedirectURI: p.RedirectURI, State: p.State, Issuer: issuer, MaxScope: maxScope(p.Scope)}
	switch {
	case p.ResponseType != "code":
		return r, fail("unsupported_response_type", "only response_type=code is supported")
	case p.CodeChallenge == "":
		return r, fail("invalid_request", "PKCE is required: send code_challenge with code_challenge_method=S256")
	case p.CodeChallengeMethod != "S256":
		return r, fail("invalid_request", "code_challenge_method must be S256")
	case !validChallenge(p.CodeChallenge):
		return r, fail("invalid_request", "code_challenge is not a base64url SHA-256 value")
	case len(p.Resource) > 1:
		return r, fail("invalid_target", "only one resource can be requested")
	}
	r.Challenge = p.CodeChallenge
	r.Resource = resource
	if len(p.Resource) == 1 {
		got, ok := NormalizeResource(p.Resource[0])
		if !ok || got != resource {
			return r, fail("invalid_target", "this server only issues tokens for %s", resource)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	if s.pending == nil {
		s.pending = map[string]*Request{}
	}
	if len(s.pending) >= maxPending/2 {
		for k, v := range s.pending {
			if !v.expires.After(now) {
				delete(s.pending, k)
			}
		}
	}
	if len(s.pending) >= maxPending {
		return r, fail("temporarily_unavailable", "too many authorization requests in progress")
	}
	r.ID = auth.Random(32)
	r.expires = now.Add(RequestTTL)
	s.pending[r.ID] = r
	return r, nil
}

// Pending returns a request waiting for consent.
func (s *Service) Pending(id string) (*Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.pending[id]
	if !ok || !r.expires.After(s.Now()) {
		return nil, false
	}
	return r, true
}

// take returns a pending request and forgets it: it is answered once.
func (s *Service) take(id string) (*Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.pending[id]
	delete(s.pending, id)
	if !ok || !r.expires.After(s.Now()) {
		return nil, false
	}
	return r, true
}

// ErrRequestGone means the consent screen was answered already or waited
// too long.
var ErrRequestGone = errors.New("this connection request has expired or was already answered")

// Decide records the member's answer and returns where to send the
// browser: the application's redirect URI with a code, or with
// access_denied. scope is what the member chose; it cannot exceed what the
// application asked for nor what the member may do.
func (s *Service) Decide(ctx context.Context, id string, member app.Actor, approve bool, scope app.Scope) (string, error) {
	if member.Kind != app.ActorMember {
		return "", app.ErrForbidden
	}
	// Never above the member's own scope, and never "trusted" (within
	// only knows read and write): confirmations are the point.
	if approve && !within(scope, member.Scope) {
		return "", fmt.Errorf("%w: you cannot give more access than you have", domain.ErrValidation)
	}
	r, ok := s.Pending(id)
	if !ok {
		return "", ErrRequestGone
	}
	if approve && !within(scope, r.MaxScope) {
		return "", fmt.Errorf("%w: the application did not ask for %s access", domain.ErrValidation, scope)
	}
	if r, ok = s.take(id); !ok {
		return "", ErrRequestGone
	}
	if !approve {
		return r.errorRedirect(&Error{Code: "access_denied", Description: "the person did not allow access"}), nil
	}
	m, err := s.st.Q.GetMember(ctx, member.MemberID)
	if err != nil {
		return "", err
	}
	code := auth.Random(32)
	now := s.Now()
	err = s.st.Q.CreateOAuthCode(ctx, db.CreateOAuthCodeParams{
		CodeHash: auth.Hash(code), ClientID: r.Client.ID, MemberID: m.ID, RedirectUri: r.RedirectURI,
		CodeChallenge: r.Challenge, Scope: string(scope), Resource: r.Resource, SessionVersion: m.SessionVersion,
		ExpiresAt: store.FormatTime(now.Add(CodeTTL)),
	})
	if err != nil {
		return "", err
	}
	_ = s.st.Q.TouchOAuthClient(ctx, db.TouchOAuthClientParams{LastUsedAt: store.Ptr(store.FormatTime(now)), ID: r.Client.ID})
	return r.redirect(url.Values{"code": {code}}), nil
}

// ErrorRedirect is where to send the browser for an error the application
// must hear about.
func (r *Request) errorRedirect(e *Error) string {
	return r.redirect(url.Values{"error": {e.Code}, "error_description": {e.Description}})
}

// ErrorRedirect returns the redirect for an error from Authorize.
func (r *Request) ErrorRedirect(err error) string {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: "server_error", Description: "the server could not complete the request"}
	}
	return r.errorRedirect(e)
}

// redirect adds the response parameters to the redirect URI, keeping its
// own query, with state and the issuer (RFC 9207, against mix-up attacks).
func (r *Request) redirect(v url.Values) string {
	u, _ := url.Parse(r.RedirectURI) // checked when the request was made
	q := u.Query()
	for k, vs := range v {
		q[k] = vs
	}
	if r.State != "" {
		q.Set("state", r.State)
	}
	q.Set("iss", r.Issuer)
	u.RawQuery = q.Encode()
	return u.String()
}

// TokenRequest is the body of a token request, with the client's
// credentials from either the body or the Authorization header.
type TokenRequest struct {
	GrantType    string
	ClientID     string
	ClientSecret string
	Code         string
	RedirectURI  string
	CodeVerifier string
	RefreshToken string
	Scope        string
	Resource     []string
}

// Tokens is a successful token response.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// Token answers a token request: the authorization code grant or the
// refresh token grant.
func (s *Service) Token(ctx context.Context, in TokenRequest) (Tokens, error) {
	s.maybeCleanup(ctx)
	if in.ClientID == "" {
		return Tokens{}, fail("invalid_client", "client_id is missing")
	}
	c, err := s.authenticate(ctx, in.ClientID, in.ClientSecret)
	if err != nil {
		return Tokens{}, err
	}
	var resource string
	switch len(in.Resource) {
	case 0:
	case 1:
		var ok bool
		if resource, ok = NormalizeResource(in.Resource[0]); !ok {
			return Tokens{}, fail("invalid_target", "resource is not a valid URL")
		}
	default:
		return Tokens{}, fail("invalid_target", "only one resource can be requested")
	}
	switch in.GrantType {
	case "authorization_code":
		return s.exchange(ctx, c, in, resource)
	case "refresh_token":
		return s.refresh(ctx, c, in, resource)
	case "":
		return Tokens{}, fail("invalid_request", "grant_type is missing")
	}
	return Tokens{}, fail("unsupported_grant_type", "only authorization_code and refresh_token are supported")
}

func (s *Service) exchange(ctx context.Context, c Client, in TokenRequest, resource string) (Tokens, error) {
	if in.Code == "" || in.CodeVerifier == "" || in.RedirectURI == "" {
		return Tokens{}, fail("invalid_request", "code, code_verifier and redirect_uri are required")
	}
	hash := auth.Hash(in.Code)
	code, err := s.st.Q.GetOAuthCode(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Tokens{}, fail("invalid_grant", "unknown or expired authorization code")
	}
	if err != nil {
		return Tokens{}, err
	}
	now := s.Now()
	if code.UsedAt != nil {
		// A code used twice was copied: end what the first use obtained
		// (RFC 6749 section 4.1.2).
		if code.GrantID != nil {
			s.endGrant(ctx, *code.GrantID, "code reused")
		}
		return Tokens{}, fail("invalid_grant", "the authorization code was already used")
	}
	switch {
	case !store.ParseTime(code.ExpiresAt).After(now):
		return Tokens{}, fail("invalid_grant", "the authorization code has expired")
	case code.ClientID != c.ID:
		return Tokens{}, fail("invalid_grant", "the authorization code was issued to another client")
	case code.RedirectUri != in.RedirectURI:
		return Tokens{}, fail("invalid_grant", "redirect_uri does not match the authorization request")
	case !verifyPKCE(code.CodeChallenge, in.CodeVerifier):
		return Tokens{}, fail("invalid_grant", "code_verifier does not match the code_challenge")
	case resource != "" && resource != code.Resource:
		return Tokens{}, fail("invalid_target", "the authorization code is for %s", code.Resource)
	}
	m, err := s.st.Q.GetMember(ctx, code.MemberID)
	if err != nil || m.SessionVersion != code.SessionVersion {
		return Tokens{}, fail("invalid_grant", "the account's sessions were ended; connect again")
	}
	grantID := domain.NewID("ogr")
	var out Tokens
	err = s.st.Tx(ctx, func(q *db.Queries) error {
		n, err := q.UseOAuthCode(ctx, db.UseOAuthCodeParams{UsedAt: store.Ptr(store.FormatTime(now)), CodeHash: hash})
		if err != nil {
			return err
		}
		if n == 0 {
			return fail("invalid_grant", "the authorization code was already used")
		}
		if c.Kind == KindDynamic {
			// One registration is one installation: connecting it again
			// replaces what it had.
			old, err := q.ListOAuthGrantsOfClient(ctx, db.ListOAuthGrantsOfClientParams{MemberID: m.ID, ClientID: c.ID, Resource: code.Resource})
			if err != nil {
				return err
			}
			for _, id := range old {
				if _, err := q.DeleteOAuthGrant(ctx, id); err != nil {
					return err
				}
			}
		}
		err = q.CreateOAuthGrant(ctx, db.CreateOAuthGrantParams{
			ID: grantID, MemberID: m.ID, ClientID: c.ID, Scope: code.Scope, Resource: code.Resource,
			SessionVersion: m.SessionVersion, CreatedAt: store.FormatTime(now),
		})
		if err != nil {
			return err
		}
		if err := q.SetOAuthCodeGrant(ctx, db.SetOAuthCodeGrantParams{GrantID: &grantID, CodeHash: hash}); err != nil {
			return err
		}
		if out, err = s.issue(ctx, q, grantID, app.Scope(code.Scope)); err != nil {
			return err
		}
		return s.audit(ctx, q, c.Name, m.ID, "app.connect", grantID, map[string]string{"client": c.Name, "scope": code.Scope})
	})
	if err != nil {
		return Tokens{}, err
	}
	_ = s.st.Q.TouchOAuthClient(ctx, db.TouchOAuthClientParams{LastUsedAt: store.Ptr(store.FormatTime(now)), ID: c.ID})
	return out, nil
}

func (s *Service) refresh(ctx context.Context, c Client, in TokenRequest, resource string) (Tokens, error) {
	if !strings.HasPrefix(in.RefreshToken, RefreshPrefix) {
		return Tokens{}, fail("invalid_grant", "unknown refresh token")
	}
	hash := auth.Hash(in.RefreshToken)
	tok, err := s.st.Q.GetOAuthToken(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && tok.Kind != "refresh") {
		return Tokens{}, fail("invalid_grant", "unknown refresh token")
	}
	if err != nil {
		return Tokens{}, err
	}
	g, err := s.st.Q.GetOAuthGrant(ctx, tok.GrantID)
	if errors.Is(err, sql.ErrNoRows) {
		return Tokens{}, fail("invalid_grant", "the connection was removed")
	}
	if err != nil {
		return Tokens{}, err
	}
	if g.ClientID != c.ID {
		return Tokens{}, fail("invalid_grant", "the refresh token was issued to another client")
	}
	now := s.Now()
	if tok.UsedAt != nil {
		// Rotation: a refresh token works once. Seeing it again means it
		// was copied, so the whole connection ends (OAuth 2.1 section
		// 4.3.1) and the person connects again.
		s.endGrant(ctx, g.ID, "refresh token reused")
		return Tokens{}, fail("invalid_grant", "the refresh token was already used; the connection was ended for safety")
	}
	if !store.ParseTime(tok.ExpiresAt).After(now) {
		return Tokens{}, fail("invalid_grant", "the refresh token has expired")
	}
	if resource != "" && resource != g.Resource {
		return Tokens{}, fail("invalid_target", "the connection is for %s", g.Resource)
	}
	if strings.Contains(" "+in.Scope+" ", " "+ScopeWrite+" ") && g.Scope != ScopeWrite {
		return Tokens{}, fail("invalid_scope", "the connection was approved for %s only", ScopeString(app.Scope(g.Scope)))
	}
	m, err := s.st.Q.GetMember(ctx, g.MemberID)
	if err != nil || m.SessionVersion != g.SessionVersion {
		return Tokens{}, fail("invalid_grant", "the account's sessions were ended; connect again")
	}
	var out Tokens
	err = s.st.Tx(ctx, func(q *db.Queries) error {
		n, err := q.UseOAuthToken(ctx, db.UseOAuthTokenParams{UsedAt: store.Ptr(store.FormatTime(now)), TokenHash: hash})
		if err != nil {
			return err
		}
		if n == 0 {
			return fail("invalid_grant", "the refresh token was already used")
		}
		out, err = s.issue(ctx, q, g.ID, app.Scope(g.Scope))
		return err
	})
	if err != nil {
		return Tokens{}, err
	}
	s.touch(ctx, g.ID, now)
	return out, nil
}

// issue creates an access token and a refresh token for a grant.
func (s *Service) issue(ctx context.Context, q *db.Queries, grantID string, scope app.Scope) (Tokens, error) {
	now := s.Now()
	out := Tokens{
		AccessToken: AccessPrefix + auth.Random(32), RefreshToken: RefreshPrefix + auth.Random(32),
		TokenType: "Bearer", ExpiresIn: int(AccessTTL / time.Second), Scope: ScopeString(scope),
	}
	for _, t := range []struct {
		plain, kind string
		ttl         time.Duration
	}{{out.AccessToken, "access", AccessTTL}, {out.RefreshToken, "refresh", RefreshTTL}} {
		err := q.CreateOAuthToken(ctx, db.CreateOAuthTokenParams{
			TokenHash: auth.Hash(t.plain), GrantID: grantID, Kind: t.kind,
			CreatedAt: store.FormatTime(now), ExpiresAt: store.FormatTime(now.Add(t.ttl)),
		})
		if err != nil {
			return Tokens{}, err
		}
	}
	return out, nil
}

// Revoke answers a revocation request (RFC 7009). Revoking an access token
// ends that token; revoking a refresh token ends the whole connection. An
// unknown token is not an error: the result is the same.
func (s *Service) Revoke(ctx context.Context, clientID, secret, token string) error {
	if clientID == "" {
		return fail("invalid_client", "client_id is missing")
	}
	c, err := s.authenticate(ctx, clientID, secret)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(token, AccessPrefix) && !strings.HasPrefix(token, RefreshPrefix) {
		return nil
	}
	tok, err := s.st.Q.GetOAuthToken(ctx, auth.Hash(token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	g, err := s.st.Q.GetOAuthGrant(ctx, tok.GrantID)
	if err != nil || g.ClientID != c.ID {
		// Another client's token: the request is refused without saying
		// whether the token exists.
		return nil //nolint:nilerr // RFC 7009: the same answer either way
	}
	if tok.Kind == "access" {
		return s.st.Q.DeleteOAuthToken(ctx, tok.TokenHash)
	}
	s.endGrant(ctx, g.ID, "revoked by the application")
	return nil
}

// endGrant deletes a grant and its tokens and records it in the audit log.
func (s *Service) endGrant(ctx context.Context, grantID, why string) {
	_ = s.st.Tx(ctx, func(q *db.Queries) error {
		g, err := q.GetOAuthGrant(ctx, grantID)
		if err != nil {
			return err
		}
		name := g.ClientID
		if c, err := q.GetOAuthClient(ctx, g.ClientID); err == nil {
			name = c.Name
		}
		if n, err := q.DeleteOAuthGrant(ctx, grantID); err != nil || n == 0 {
			return err
		}
		return s.audit(ctx, q, name, g.MemberID, "app.disconnect", grantID, map[string]string{"client": name, "reason": why})
	})
}

// audit records a connection or disconnection in the activity timeline,
// under the application's name.
func (s *Service) audit(ctx context.Context, q *db.Queries, clientName, memberID, action, grantID string, after any) error {
	b, err := json.Marshal(after)
	if err != nil {
		return err
	}
	return q.CreateAudit(ctx, db.CreateAuditParams{
		ID: domain.NewID("aud"), At: store.FormatTime(s.Now()), ActorKind: string(app.ActorAgent), ActorID: memberID,
		ActorLabel: clientName, Action: action, Entity: "connected_app", EntityID: grantID, After: string(b),
	})
}

// touch records that a grant was used, at most once a minute.
func (s *Service) touch(ctx context.Context, grantID string, now time.Time) {
	_ = s.st.Q.TouchOAuthGrant(ctx, db.TouchOAuthGrantParams{
		Now: store.Ptr(store.FormatTime(now)), ID: grantID, Stale: store.Ptr(store.FormatTime(now.Add(-time.Minute))),
	})
}

// Access returns the actor of an access token presented at resource: the
// member who approved it, under the application's name, with the scope
// they chose.
func (s *Service) Access(ctx context.Context, plain, resource string) (app.Actor, error) {
	if !strings.HasPrefix(plain, AccessPrefix) {
		return app.Actor{}, ErrInvalidToken
	}
	row, err := s.st.Q.GetOAuthAccess(ctx, auth.Hash(plain))
	if err != nil {
		return app.Actor{}, ErrInvalidToken
	}
	now := s.Now()
	if !store.ParseTime(row.ExpiresAt).After(now) || row.GrantVersion != row.MemberVersion || row.Resource != resource {
		return app.Actor{}, ErrInvalidToken
	}
	s.touch(ctx, row.GrantID, now)
	return app.Actor{Kind: app.ActorAgent, MemberID: row.MemberID, HouseholdID: row.HouseholdID, Label: row.ClientName, Scope: app.Scope(row.Scope)}, nil
}

// App is an application a member connected.
type App struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Verified     bool   `json:"verified" doc:"the application is identified by an address Exhale fetched (a client metadata document), not by what it said about itself"`
	ClientHost   string `json:"clientHost,omitempty" doc:"host of the application's address, when verified"`
	RedirectHost string `json:"redirectHost" doc:"where the application received its access"`
	Scope        string `json:"scope" enum:"read,write"`
	CreatedAt    string `json:"createdAt"`
	LastUsedAt   string `json:"lastUsedAt,omitempty"`
}

// Apps lists the applications a member has connected and that can still
// reach Exhale.
func (s *Service) Apps(ctx context.Context, memberID string) ([]App, error) {
	rows, err := s.st.Q.ListOAuthGrants(ctx, db.ListOAuthGrantsParams{MemberID: memberID, Now: store.FormatTime(s.Now())})
	out := make([]App, 0, len(rows))
	for _, r := range rows {
		c := clientFromRow(db.OauthClient{ID: r.ClientID, Kind: r.ClientKind, Name: r.ClientName, Uri: r.ClientUri, RedirectUris: r.RedirectUris})
		a := App{ID: r.ID, Name: c.Name, Verified: c.Verified(), ClientHost: c.Host(), Scope: r.Scope, CreatedAt: r.CreatedAt, LastUsedAt: store.Str(r.LastUsedAt)}
		if len(c.RedirectURIs) > 0 {
			a.RedirectHost = redirectHost(c.RedirectURIs[0])
		}
		out = append(out, a)
	}
	return out, err
}

// Disconnect ends one of a member's connections, with every token it had.
func (s *Service) Disconnect(ctx context.Context, memberID, grantID string) error {
	g, err := s.st.Q.GetOAuthGrant(ctx, grantID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && g.MemberID != memberID) {
		return fmt.Errorf("%w: connected app %s", domain.ErrNotFound, grantID)
	}
	if err != nil {
		return err
	}
	s.endGrant(ctx, g.ID, "disconnected in Exhale")
	return nil
}
