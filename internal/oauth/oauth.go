// Package oauth is Exhale's own OAuth 2.1 authorization server, the part of
// the MCP authorization spec that lets a cloud assistant (Claude.ai,
// ChatGPT, Grok) connect to a person's Exhale by its URL (ADR 0018
// milestone 3, ADR 0024).
//
// It is deliberately small: the authorization code grant with PKCE (S256
// only), rotating refresh tokens, resource indicators (RFC 8707), two ways
// for an application to introduce itself (a Client ID Metadata Document, or
// dynamic registration, RFC 7591), revocation (RFC 7009) and opaque tokens
// stored as hashes. There are no JWTs and no signing keys. The HTTP
// endpoints live in internal/httpapi; this package holds the rules.
package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/netguard"
	"github.com/akamachinecode/exhale/internal/store"
)

const (
	// AccessPrefix and RefreshPrefix mark the tokens (secret-scanning
	// friendly, like exhale_pat_).
	AccessPrefix  = "exhale_oat_"
	RefreshPrefix = "exhale_ort_"
	secretPrefix  = "exhale_ocs_" //nolint:gosec // G101: a prefix, not a secret

	// AccessTTL is short because an access token cannot be recalled from
	// where it leaked to; the refresh token renews it.
	AccessTTL = time.Hour
	// RefreshTTL: a connection nobody uses for this long ends by itself.
	// Every refresh issues a new refresh token with a new lifetime.
	RefreshTTL = 90 * 24 * time.Hour
	// CodeTTL bounds the time between approving and the application
	// fetching its tokens.
	CodeTTL = 5 * time.Minute
	// RequestTTL is how long the consent screen stays valid.
	RequestTTL = 10 * time.Minute

	maxPending        = 10_000
	maxDynamicClients = 1000
	maxRedirectURIs   = 20
	maxURL            = 2048
	maxDocument       = 32 << 10 // a client metadata document
	fetchTimeout      = 5 * time.Second
	cleanupEvery      = 10 * time.Minute

	unusedClientTTL = 7 * 24 * time.Hour  // registered, never approved
	idleClientTTL   = 90 * 24 * time.Hour // approved once, no grant left
)

// Scopes an application can ask for. They are the scopes of personal
// tokens; "trusted" is never available through OAuth.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

// Scopes is what the protected resource metadata advertises.
var Scopes = []string{ScopeRead, ScopeWrite}

// Error is an OAuth error: Code goes to the application ("invalid_grant"),
// Description is a sentence for whoever debugs it.
type Error struct {
	Code        string
	Description string
}

func (e *Error) Error() string { return e.Code + ": " + e.Description }

func fail(code, format string, args ...any) *Error {
	return &Error{Code: code, Description: fmt.Sprintf(format, args...)}
}

// Code returns the OAuth error code of err ("server_error" if it has none).
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "server_error"
}

// Description returns the sentence of an OAuth error, or a generic one.
func Description(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Description
	}
	return "the server could not complete the request"
}

// ErrInvalidToken is returned for an access token that is unknown, expired,
// revoked or meant for another resource.
var ErrInvalidToken = errors.New("invalid access token")

// Service implements the authorization server on top of the store.
type Service struct {
	st *store.Store
	// HTTP fetches client metadata documents. The default refuses private
	// and local addresses and does not follow redirects.
	HTTP *http.Client
	// Now is the clock (tests move it).
	Now func() time.Time

	mu          sync.Mutex
	pending     map[string]*Request
	lastCleanup time.Time
}

// New returns the service. Client metadata documents are fetched through
// netguard: an application's URL must never reach the server's own network.
func New(st *store.Store) *Service {
	return &Service{st: st, HTTP: (&netguard.Guard{}).Client(fetchTimeout), Now: time.Now}
}

// ScopeString is the OAuth scope value of a granted scope: write includes
// read.
func ScopeString(s app.Scope) string {
	if s == app.ScopeWrite {
		return ScopeRead + " " + ScopeWrite
	}
	return ScopeRead
}

// maxScope is the most a request asks for. Scopes we do not know
// (offline_access, openid…) are ignored; a request that names none of ours
// gets the full choice on the consent screen.
func maxScope(requested string) app.Scope {
	read := false
	for _, s := range strings.Fields(requested) {
		switch s {
		case ScopeWrite:
			return app.ScopeWrite
		case ScopeRead:
			read = true
		}
	}
	if read {
		return app.ScopeRead
	}
	return app.ScopeWrite
}

// within reports whether scope s (read or write) is covered by limit.
func within(s, limit app.Scope) bool {
	switch s {
	case app.ScopeRead:
		return limit == app.ScopeRead || limit == app.ScopeWrite || limit == app.ScopeTrusted
	case app.ScopeWrite:
		return limit == app.ScopeWrite || limit == app.ScopeTrusted
	case app.ScopeTrusted:
		return false // never through OAuth: confirmations are the point
	}
	return false
}

// verifyPKCE checks an S256 code verifier against the challenge of the
// authorization request (RFC 7636).
func verifyPKCE(challenge, verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, c := range verifier {
		if !unreserved(c) {
			return false
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func unreserved(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}

// validChallenge reports whether s looks like base64url(SHA-256(…)).
func validChallenge(s string) bool {
	if len(s) != 43 {
		return false
	}
	for _, c := range s {
		if !unreserved(c) || c == '.' || c == '~' {
			return false
		}
	}
	return true
}

// NormalizeResource returns the canonical form of a resource identifier
// (RFC 8707): lowercase scheme and host, no default port, no trailing
// slash. It fails for anything that is not an absolute http(s) URL without
// fragment.
func NormalizeResource(raw string) (string, bool) {
	if raw == "" || len(raw) > maxURL || strings.Contains(raw, "#") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Host)
	if (scheme == "https" && strings.HasSuffix(host, ":443")) || (scheme == "http" && strings.HasSuffix(host, ":80")) {
		host = host[:strings.LastIndexByte(host, ':')]
	}
	out := scheme + "://" + host + strings.TrimRight(u.EscapedPath(), "/")
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out, true
}

// maybeCleanup runs Cleanup at most every few minutes, from the requests
// themselves: no extra goroutine to supervise.
func (s *Service) maybeCleanup(ctx context.Context) {
	s.mu.Lock()
	now := s.Now()
	due := now.Sub(s.lastCleanup) >= cleanupEvery
	if due {
		s.lastCleanup = now
	}
	s.mu.Unlock()
	if due {
		_ = s.Cleanup(ctx)
	}
}

// Cleanup deletes what has expired: tokens, codes, grants without tokens
// and clients nobody uses. It is cheap and safe to call often.
func (s *Service) Cleanup(ctx context.Context) error {
	now := s.Now()
	q := s.st.Q
	if err := q.DeleteExpiredOAuthTokens(ctx, store.FormatTime(now)); err != nil {
		return err
	}
	// A used code is kept for a day so that using it again is noticed.
	if err := q.DeleteExpiredOAuthCodes(ctx, store.FormatTime(now.Add(-24*time.Hour))); err != nil {
		return err
	}
	if err := q.DeleteDeadOAuthGrants(ctx); err != nil {
		return err
	}
	return q.DeleteIdleOAuthClients(ctx, dbIdle(now))
}
