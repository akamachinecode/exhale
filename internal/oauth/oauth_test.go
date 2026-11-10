package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/store"
)

func TestNormalizeResource(t *testing.T) {
	for in, want := range map[string]string{
		"https://Exhale.Example.com/mcp":     "https://exhale.example.com/mcp",
		"HTTPS://exhale.example.com/mcp/":    "https://exhale.example.com/mcp",
		"https://exhale.example.com:443/mcp": "https://exhale.example.com/mcp",
		"http://127.0.0.1:8080/mcp":          "http://127.0.0.1:8080/mcp",
		"http://localhost:80/mcp":            "http://localhost/mcp",
		"https://exhale.example.com":         "https://exhale.example.com",
	} {
		if got, ok := NormalizeResource(in); !ok || got != want {
			t.Errorf("%s: %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "exhale.example.com/mcp", "https://x/mcp#frag", "ftp://x/mcp", "https://user@x/mcp"} {
		if _, ok := NormalizeResource(in); ok {
			t.Errorf("%q must be refused", in)
		}
	}
}

func TestRedirectURIs(t *testing.T) {
	for _, ok := range []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost:3000/cb", "http://127.0.0.1/cb", "http://[::1]:9/cb"} {
		if err := checkRedirectURI(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://claude.ai/cb", "javascript:alert(1)", "https://x/cb#f", "https://u:p@x/cb", "cursor://cb", "https://x/c b", `https://x\@y/cb`, "/relative"} {
		if checkRedirectURI(bad) == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	reg := []string{"https://app.example/cb", "http://127.0.0.1:3000/cb"}
	for req, want := range map[string]bool{
		"https://app.example/cb":       true,
		"https://app.example/cb/":      false,
		"https://app.example/cb?x=1":   false,
		"https://APP.example/cb":       false,
		"http://127.0.0.1:51234/cb":    true, // a native app's free port (OAuth 2.1 section 8.4.2)
		"http://127.0.0.1:51234/other": false,
		"http://localhost:3000/cb":     false,
	} {
		if got := matchRedirect(reg, req); got != want {
			t.Errorf("%s: %v, want %v", req, got, want)
		}
	}
}

func TestPKCE(t *testing.T) {
	// RFC 7636 appendix B.
	verifier, ch := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if !validChallenge(ch) || !verifyPKCE(ch, verifier) {
		t.Fatal("the RFC example must verify")
	}
	if verifyPKCE(ch, verifier[:42]) || verifyPKCE(ch, strings.Repeat("a", 43)) || verifyPKCE(ch, verifier+"+") {
		t.Fatal("wrong verifiers must fail")
	}
	if validChallenge("plain") || validChallenge(ch+"=") {
		t.Fatal("not a S256 challenge")
	}
}

func TestScopes(t *testing.T) {
	for in, want := range map[string]string{"": "write", "read": "read", "read write": "write", "offline_access": "write", "openid read": "read"} {
		if got := string(maxScope(in)); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}

func TestCleanNameAndFreshness(t *testing.T) {
	if got := cleanName("  Chat\u202eGPT\n\tconnector  "); got != "ChatGPT connector" {
		t.Errorf("cleanName: %q", got)
	}
	if got := cleanName(strings.Repeat("é", 100)); len([]rune(got)) != 60 {
		t.Errorf("long names are cut: %d", len([]rune(got)))
	}
	for cc, want := range map[string]time.Duration{"": time.Hour, "max-age=60": 5 * time.Minute, "max-age=7200": 2 * time.Hour, "no-store": 5 * time.Minute, "max-age=999999": 24 * time.Hour} {
		if got := freshness(cc); got != want {
			t.Errorf("%q: %v, want %v", cc, got, want)
		}
	}
}

func TestClientMetadataDocument(t *testing.T) {
	doc := map[string]any{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved.json" {
			http.Redirect(w, r, "/client.json", http.StatusFound)
			return
		}
		w.Header().Set("Cache-Control", "max-age=600")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(srv.Close)
	st, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	s := New(st)
	s.HTTP = srv.Client()
	s.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	s.Now = func() time.Time { return now }
	id := srv.URL + "/client.json"
	ctx := context.Background()

	for name, d := range map[string]map[string]any{
		"wrong client_id": {"client_id": srv.URL + "/other.json", "client_name": "X", "redirect_uris": []string{"https://x/cb"}},
		"no name":         {"client_id": id, "redirect_uris": []string{"https://x/cb"}},
		"secret":          {"client_id": id, "client_name": "X", "redirect_uris": []string{"https://x/cb"}, "token_endpoint_auth_method": "client_secret_basic"},
		"bad redirect":    {"client_id": id, "client_name": "X", "redirect_uris": []string{"http://x/cb"}},
	} {
		doc = d
		if _, err := s.Client(ctx, id); Code(err) != "invalid_client" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.Client(ctx, srv.URL+"/moved.json"); Code(err) != "invalid_client" {
		t.Errorf("redirects are not followed: %v", err)
	}
	if _, err := s.Client(ctx, "http://example.com/client.json"); Code(err) != "invalid_client" {
		t.Errorf("http client id: %v", err)
	}

	doc = map[string]any{"client_id": id, "client_name": "Claude", "redirect_uris": []string{"https://claude.example/cb"}}
	c, err := s.Client(ctx, id)
	if err != nil || c.Name != "Claude" || !c.Verified() || c.Host() != "127.0.0.1" {
		t.Fatalf("valid document: %+v %v", c, err)
	}
	// Cached for its max-age: a changed document is seen after it.
	doc = map[string]any{"client_id": id, "client_name": "Claude 2", "redirect_uris": []string{"https://claude.example/cb"}}
	if c, _ := s.Client(ctx, id); c.Name != "Claude" {
		t.Fatalf("cached: %s", c.Name)
	}
	now = now.Add(11 * time.Minute)
	if c, _ := s.Client(ctx, id); c.Name != "Claude 2" {
		t.Fatalf("refetched: %s", c.Name)
	}
}
