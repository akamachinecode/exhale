package oauth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/netguard"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// How an application introduced itself.
const (
	// KindDynamic clients registered themselves (RFC 7591). Anyone can, so
	// nothing they say about themselves is verified.
	KindDynamic = "dynamic"
	// KindMetadata clients are identified by the https URL of a document
	// that Exhale fetched: who controls that address is known.
	KindMetadata = "metadata"
)

// Client is an application that may ask for access.
type Client struct {
	ID           string
	Kind         string
	Name         string
	URI          string
	RedirectURIs []string
	// secretHash is set for confidential clients: dynamic registrations
	// that asked for client_secret_basic or client_secret_post.
	secretHash string
}

// Public reports whether the client has no secret (PKCE alone protects
// its codes).
func (c Client) Public() bool { return c.secretHash == "" }

// Verified reports whether the client's identity is an address Exhale
// fetched, rather than something the client said about itself.
func (c Client) Verified() bool { return c.Kind == KindMetadata }

// Host is the host of a verified client's address ("" for the others).
func (c Client) Host() string {
	if !c.Verified() {
		return ""
	}
	u, err := url.Parse(c.ID)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func clientFromRow(r db.OauthClient) Client {
	c := Client{ID: r.ID, Kind: r.Kind, Name: r.Name, URI: r.Uri, secretHash: r.SecretHash}
	_ = json.Unmarshal([]byte(r.RedirectUris), &c.RedirectURIs)
	return c
}

// Registration is the body of a dynamic client registration (RFC 7591).
// Fields we have no use for are accepted and ignored.
type Registration struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// Registered is what a registration returns: the client, and its secret
// once for a confidential client.
type Registered struct {
	Client
	Secret     string
	AuthMethod string
	CreatedAt  time.Time
}

// Register stores a self-registered client. A client is public (no secret,
// PKCE protects the code) unless it asks for client_secret_basic or
// client_secret_post; PKCE is required either way. When the table is full
// the oldest registration nobody approved makes room.
func (s *Service) Register(ctx context.Context, in Registration) (Registered, error) {
	s.maybeCleanup(ctx)
	method := in.TokenEndpointAuthMethod
	switch method {
	case "":
		method = "none"
	case "none", "client_secret_basic", "client_secret_post":
	default:
		return Registered{}, fail("invalid_client_metadata", "token_endpoint_auth_method %q is not supported", clip(method))
	}
	if err := checkRedirectURIs(in.RedirectURIs); err != nil {
		return Registered{}, fail("invalid_redirect_uri", "%s", err.Error())
	}
	if len(in.GrantTypes) > 0 && !contains(in.GrantTypes, "authorization_code") {
		return Registered{}, fail("invalid_client_metadata", "grant_types must include authorization_code")
	}
	for _, g := range in.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return Registered{}, fail("invalid_client_metadata", "grant type %q is not supported", clip(g))
		}
	}
	if len(in.ResponseTypes) > 0 && !contains(in.ResponseTypes, "code") {
		return Registered{}, fail("invalid_client_metadata", "response_types must include code")
	}
	c := Client{
		ID: domain.NewID("oac"), Kind: KindDynamic,
		Name: cleanName(in.ClientName), URI: cleanURI(in.ClientURI), RedirectURIs: in.RedirectURIs,
	}
	if c.Name == "" {
		c.Name = redirectHost(in.RedirectURIs[0])
	}
	out := Registered{AuthMethod: method, CreatedAt: s.Now()}
	if method != "none" {
		out.Secret = secretPrefix + auth.Random(32)
		c.secretHash = auth.Hash(out.Secret)
	}
	uris, _ := json.Marshal(c.RedirectURIs)
	err := s.st.Tx(ctx, func(q *db.Queries) error {
		n, err := q.CountOAuthDynamicClients(ctx)
		if err != nil {
			return err
		}
		if n >= maxDynamicClients {
			if gone, err := q.EvictOAuthClient(ctx); err != nil {
				return err
			} else if gone == 0 {
				return fail("temporarily_unavailable", "too many registered applications; remove one in Exhale and try again")
			}
		}
		return q.CreateOAuthClient(ctx, db.CreateOAuthClientParams{
			ID: c.ID, Name: c.Name, Uri: c.URI, RedirectUris: string(uris), SecretHash: c.secretHash,
			CreatedAt: store.FormatTime(out.CreatedAt),
		})
	})
	out.Client = c
	return out, err
}

// authenticate checks the credentials a token or revocation request
// presents for a client: none for a public client, its secret for a
// confidential one.
func (s *Service) authenticate(ctx context.Context, id, secret string) (Client, error) {
	c, err := s.KnownClient(ctx, id)
	if err != nil {
		return Client{}, err
	}
	if c.Public() {
		if secret != "" {
			return Client{}, fail("invalid_client", "this client has no secret")
		}
		return c, nil
	}
	if secret == "" || subtle.ConstantTimeCompare([]byte(auth.Hash(secret)), []byte(c.secretHash)) != 1 {
		return Client{}, fail("invalid_client", "wrong client secret")
	}
	return c, nil
}

// Client resolves a client_id as an authorization request presents it: a
// registered id, or the URL of a metadata document, fetched (or read from
// its cached copy) and checked.
func (s *Service) Client(ctx context.Context, id string) (Client, error) {
	if strings.Contains(id, "://") {
		return s.metadataClient(ctx, id)
	}
	return s.KnownClient(ctx, id)
}

// KnownClient returns a client without fetching anything: the token
// endpoint only serves clients that went through an authorization request.
func (s *Service) KnownClient(ctx context.Context, id string) (Client, error) {
	if id == "" || len(id) > maxURL {
		return Client{}, fail("invalid_client", "unknown client")
	}
	row, err := s.st.Q.GetOAuthClient(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Client{}, fail("invalid_client", "unknown client")
	}
	if err != nil {
		return Client{}, err
	}
	return clientFromRow(row), nil
}

func (s *Service) metadataClient(ctx context.Context, id string) (Client, error) {
	if err := checkClientURL(id); err != nil {
		return Client{}, fail("invalid_client", "%s", err.Error())
	}
	now := s.Now()
	row, err := s.st.Q.GetOAuthClient(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Client{}, err
	}
	if err == nil && row.Kind != KindMetadata {
		return Client{}, fail("invalid_client", "unknown client")
	}
	if err == nil && row.FreshUntil != nil && store.ParseTime(*row.FreshUntil).After(now) {
		return clientFromRow(row), nil
	}
	doc, fresh, ferr := s.fetchDocument(ctx, id)
	if ferr != nil {
		return Client{}, fail("invalid_client", "could not read the application's description at %s: %s", netguard.Host(id), ferr.Error())
	}
	c := Client{ID: id, Kind: KindMetadata, Name: cleanName(doc.ClientName), URI: cleanURI(doc.ClientURI), RedirectURIs: doc.RedirectURIs}
	uris, _ := json.Marshal(c.RedirectURIs)
	err = s.st.Q.SaveOAuthMetadataClient(ctx, db.SaveOAuthMetadataClientParams{
		ID: c.ID, Name: c.Name, Uri: c.URI, RedirectUris: string(uris),
		CreatedAt: store.FormatTime(now), FreshUntil: store.Ptr(store.FormatTime(now.Add(fresh))),
	})
	return c, err
}

// document is a Client ID Metadata Document
// (draft-ietf-oauth-client-id-metadata-document).
type document struct {
	ClientID                string          `json:"client_id"`
	ClientName              string          `json:"client_name"`
	ClientURI               string          `json:"client_uri"`
	RedirectURIs            []string        `json:"redirect_uris"`
	GrantTypes              []string        `json:"grant_types"`
	TokenEndpointAuthMethod string          `json:"token_endpoint_auth_method"`
	ClientSecret            json.RawMessage `json:"client_secret"`
}

// fetchDocument downloads and validates the document at id, and says how
// long it may be cached.
func (s *Service) fetchDocument(ctx context.Context, id string) (document, time.Duration, error) {
	var doc document
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if err != nil {
		return doc, 0, errors.New("the address is not valid")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Exhale")
	res, err := s.HTTP.Do(req)
	if err != nil {
		// The cause without the URL: enough to act on, nothing to leak.
		return doc, 0, netguard.Cause(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		// Redirects are not followed: the document must be at its address.
		return doc, 0, errors.New("the server answered " + strconv.Itoa(res.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxDocument+1))
	if err != nil {
		return doc, 0, errors.New("the answer could not be read")
	}
	if len(body) > maxDocument {
		return doc, 0, errors.New("the document is too large")
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return doc, 0, errors.New("the document is not valid JSON")
	}
	switch {
	case doc.ClientID != id:
		return doc, 0, errors.New("the document's client_id is not its own address")
	case strings.TrimSpace(doc.ClientName) == "":
		return doc, 0, errors.New("the document has no client_name")
	case len(doc.ClientSecret) > 0 || (doc.TokenEndpointAuthMethod != "" && doc.TokenEndpointAuthMethod != "none"):
		return doc, 0, errors.New(`the document must use token_endpoint_auth_method "none"`)
	case len(doc.GrantTypes) > 0 && !contains(doc.GrantTypes, "authorization_code"):
		return doc, 0, errors.New("the document does not allow the authorization code grant")
	}
	if err := checkRedirectURIs(doc.RedirectURIs); err != nil {
		return doc, 0, err
	}
	return doc, freshness(res.Header.Get("Cache-Control")), nil
}

// freshness honours Cache-Control: max-age within bounds: often enough to
// notice a changed document, rarely enough not to fetch on every consent.
func freshness(cacheControl string) time.Duration {
	const lo, hi, def = 5 * time.Minute, 24 * time.Hour, time.Hour
	out := def
	for _, part := range strings.Split(cacheControl, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "no-store" || part == "no-cache" {
			return lo
		}
		if v, ok := strings.CutPrefix(part, "max-age="); ok {
			if n, err := strconv.Atoi(v); err == nil {
				out = time.Duration(n) * time.Second
			}
		}
	}
	return min(max(out, lo), hi)
}

// checkClientURL applies the rules for a client_id that is a URL: https, a
// path, and nothing that makes two spellings of one address.
func checkClientURL(raw string) error {
	if len(raw) > maxURL {
		return errors.New("the client address is too long")
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Host == "":
		return errors.New("the client address is not a valid URL")
	case u.Scheme != "https":
		return errors.New("the client address must use https")
	case u.User != nil || strings.Contains(raw, "#"):
		return errors.New("the client address must not carry credentials or a fragment")
	case u.Path == "" || u.Path == "/":
		return errors.New("the client address must have a path")
	}
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		if seg == "." || seg == ".." {
			return errors.New("the client address must not contain dot segments")
		}
	}
	return nil
}

func checkRedirectURIs(uris []string) error {
	if len(uris) == 0 {
		return errors.New("at least one redirect URI is needed")
	}
	if len(uris) > maxRedirectURIs {
		return errors.New("too many redirect URIs")
	}
	for _, u := range uris {
		if err := checkRedirectURI(u); err != nil {
			return err
		}
	}
	return nil
}

// checkRedirectURI accepts where a code may be sent: an https address, or
// http on this computer (a native application listening on a local port).
// Other schemes are refused, which also keeps javascript: and data: out.
func checkRedirectURI(raw string) error {
	if raw == "" || len(raw) > maxURL {
		return errors.New("a redirect URI is empty or too long")
	}
	for _, c := range raw {
		if c <= 0x20 || c == 0x7f || c == '\\' {
			return errors.New("a redirect URI contains characters that are not allowed")
		}
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Host == "" || u.Hostname() == "" || u.Opaque != "":
		return errors.New("a redirect URI is not an absolute URL")
	case u.User != nil || strings.Contains(raw, "#"):
		return errors.New("a redirect URI must not carry credentials or a fragment")
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && loopbackName(u.Hostname()):
		return nil
	}
	return errors.New("a redirect URI must use https, or http on localhost")
}

func loopbackName(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LoopbackRedirect reports whether a redirect URI points at this computer:
// any program running there could be the one that receives the code.
func LoopbackRedirect(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && loopbackName(u.Hostname())
}

func redirectHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// matchRedirect compares the redirect URI of a request with the registered
// ones: exactly, except that a native application's local address matches
// on any port, since it picks a free one each time (RFC 8252 section 7.3;
// applied to "localhost" too, which Claude Code uses).
func matchRedirect(registered []string, requested string) bool {
	if checkRedirectURI(requested) != nil {
		return false
	}
	for _, r := range registered {
		if r == requested {
			return true
		}
	}
	want, err := url.Parse(requested)
	if err != nil || want.Scheme != "http" || !loopbackName(want.Hostname()) {
		return false
	}
	for _, r := range registered {
		have, err := url.Parse(r)
		if err != nil || have.Scheme != "http" || !loopbackName(have.Hostname()) {
			continue
		}
		if strings.EqualFold(have.Hostname(), want.Hostname()) && have.EscapedPath() == want.EscapedPath() && have.RawQuery == want.RawQuery {
			return true
		}
	}
	return false
}

// cleanName makes a name an application gave itself safe to show and
// store: one line, no control or invisible characters, a sensible length.
func cleanName(s string) string {
	var b strings.Builder
	space := false
	for _, c := range strings.TrimSpace(s) {
		switch {
		case unicode.IsSpace(c):
			space = true
		case unicode.IsControl(c) || unicode.In(c, unicode.Cf):
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(c)
		}
	}
	out := []rune(b.String())
	if len(out) > 60 {
		out = out[:60]
	}
	return string(out)
}

// cleanURI keeps an application's home page only if it is a plain https
// address.
func cleanURI(s string) string {
	u, err := url.Parse(s)
	if err != nil || len(s) > maxURL || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	return s
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

func dbIdle(now time.Time) db.DeleteIdleOAuthClientsParams {
	return db.DeleteIdleOAuthClientsParams{
		UnusedBefore: store.FormatTime(now.Add(-unusedClientTTL)),
		IdleBefore:   store.Ptr(store.FormatTime(now.Add(-idleClientTTL))),
	}
}
