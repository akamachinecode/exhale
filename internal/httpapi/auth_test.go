package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/store"
)

// browser is a client with a cookie jar, like a web UI tab.
type browser struct {
	t    *testing.T
	base string
	c    *http.Client
	hdr  http.Header
}

func newBrowser(t *testing.T, base string) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, base: base, c: &http.Client{Jar: jar}, hdr: http.Header{}}
}

func (b *browser) do(method, path string, body any) (int, map[string]any) {
	b.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, b.base+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range b.hdr {
		req.Header[k] = v
	}
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func freshServer(t *testing.T) (*httptest.Server, *app.Service) {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := app.New(st, bus.New(), nil, func() time.Time { return time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC) })
	h, _, err := New(svc, Config{Version: "test", TelegramBot: func() string { return "exhale_test_bot" }})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, svc
}

var alex = auth.SetupInput{Name: "Alex", Email: "Alex@Example.com", Password: "correct horse battery", HouseholdName: "Casa", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "es"}

// TestOnboardingAndSignIn walks the first run: setup wizard, the app behind
// the session, sign out, wrong and right passwords.
func TestOnboardingAndSignIn(t *testing.T) {
	srv, _ := freshServer(t)
	b := newBrowser(t, srv.URL)

	code, s := b.do("GET", "/api/v1/auth/session", nil)
	if code != 200 || s["setupRequired"] != true || s["setupNeedsCode"] != false || s["user"] != nil {
		t.Fatalf("fresh instance should ask for setup from this machine: %d %v", code, s)
	}
	if code, _ := b.do("GET", "/api/v1/household", nil); code != 401 {
		t.Fatalf("the API must be closed before sign-in, got %d", code)
	}
	weak := alex
	weak.Password = "short"
	if code, _ := b.do("POST", "/api/v1/auth/setup", weak); code != 422 {
		t.Fatalf("short password accepted: %d", code)
	}
	code, s = b.do("POST", "/api/v1/auth/setup", alex)
	if code != 200 || s["user"].(map[string]any)["email"] != "alex@example.com" || s["telegramBot"] != "exhale_test_bot" {
		t.Fatalf("setup: %d %v", code, s)
	}
	code, h := b.do("GET", "/api/v1/household", nil)
	if code != 200 || h["household"].(map[string]any)["name"] != "Casa" || h["household"].(map[string]any)["currency"] != "EUR" {
		t.Fatalf("household after setup: %d %v", code, h)
	}
	if code, _ := b.do("POST", "/api/v1/auth/setup", alex); code != 409 {
		t.Fatalf("second setup must be refused, got %d", code)
	}

	b.do("POST", "/api/v1/auth/logout", nil)
	if code, _ := b.do("GET", "/api/v1/household", nil); code != 401 {
		t.Fatalf("still signed in after logout: %d", code)
	}
	if code, _ := b.do("POST", "/api/v1/auth/login", map[string]string{"email": "alex@example.com", "password": "wrong password!"}); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _ := b.do("POST", "/api/v1/auth/login", map[string]string{"email": " ALEX@example.com ", "password": alex.Password}); code != 200 {
		t.Fatalf("login: %d", code)
	}
	if code, _ := b.do("GET", "/api/v1/subscriptions", nil); code != 200 {
		t.Fatalf("API after login: %d", code)
	}
}

func TestRemoteSetupNeedsCode(t *testing.T) {
	srv, svc := freshServer(t)
	b := newBrowser(t, srv.URL)
	b.hdr.Set("X-Forwarded-For", "203.0.113.7") // behind a proxy: not local

	if _, s := b.do("GET", "/api/v1/auth/session", nil); s["setupNeedsCode"] != true {
		t.Fatalf("remote setup must ask for the code: %v", s)
	}
	if code, _ := b.do("POST", "/api/v1/auth/setup", alex); code != 422 {
		t.Fatalf("setup without code: %d", code)
	}
	setupCode, _ := auth.NewSetupCode(context.Background(), svc.Store())
	in := alex
	in.Code = setupCode
	if code, s := b.do("POST", "/api/v1/auth/setup", in); code != 200 {
		t.Fatalf("setup with code: %d %v", code, s)
	}
}

func TestCrossSiteWritesAreRejected(t *testing.T) {
	srv, _ := freshServer(t)
	b := newBrowser(t, srv.URL)
	b.hdr.Set("Sec-Fetch-Site", "cross-site")
	if code, _ := b.do("POST", "/api/v1/auth/setup", alex); code != http.StatusForbidden {
		t.Fatalf("cross-site POST must be rejected, got %d", code)
	}
}

// TestInvitePasswordAndTokens covers the household and security settings:
// invite a member, change a password (other sessions end), API tokens and
// the Telegram link code.
func TestInvitePasswordAndTokens(t *testing.T) {
	srv, _ := freshServer(t)
	owner := newBrowser(t, srv.URL)
	owner.do("POST", "/api/v1/auth/setup", alex)
	laptop := newBrowser(t, srv.URL)
	if code, _ := laptop.do("POST", "/api/v1/auth/login", map[string]string{"email": alex.Email, "password": alex.Password}); code != 200 {
		t.Fatalf("second session: %d", code)
	}

	code, inv := owner.do("POST", "/api/v1/auth/invites", map[string]string{"role": "member"})
	if code != 200 {
		t.Fatalf("invite: %d %v", code, inv)
	}
	invite := inv["code"].(string)
	guest := newBrowser(t, srv.URL)
	if code, info := guest.do("GET", "/api/v1/auth/invites/"+invite, nil); code != 200 || info["householdName"] != "Casa" || info["invitedBy"] != "Alex" {
		t.Fatalf("invite info: %d %v", code, info)
	}
	join := map[string]string{"code": invite, "name": "Marta", "email": "marta@example.com", "password": "another long password"}
	if code, s := guest.do("POST", "/api/v1/auth/join", join); code != 200 || s["user"].(map[string]any)["role"] != "member" {
		t.Fatalf("join: %d %v", code, s)
	}
	join["email"] = "someone.else@example.com"
	if code, _ := newBrowser(t, srv.URL).do("POST", "/api/v1/auth/join", join); code != 404 {
		t.Fatalf("an invite must work once, got %d", code)
	}
	if code, _ := guest.do("POST", "/api/v1/auth/invites", map[string]string{"role": "member"}); code != 403 {
		t.Fatalf("members cannot invite, got %d", code)
	}
	if _, h := guest.do("GET", "/api/v1/household", nil); len(h["members"].([]any)) != 2 {
		t.Fatalf("household should have two members: %v", h["members"])
	}

	if code, _ := owner.do("POST", "/api/v1/auth/password", map[string]string{"current": "nope nope nope", "new": "a brand new password"}); code != 422 {
		t.Fatalf("wrong current password: %d", code)
	}
	if code, _ := owner.do("POST", "/api/v1/auth/password", map[string]string{"current": alex.Password, "new": "a brand new password"}); code != 200 {
		t.Fatalf("change password: %d", code)
	}
	if code, _ := owner.do("GET", "/api/v1/household", nil); code != 200 {
		t.Fatalf("the session that changed the password must stay signed in, got %d", code)
	}
	if code, _ := laptop.do("GET", "/api/v1/household", nil); code != 401 {
		t.Fatalf("other sessions must be signed out after a password change, got %d", code)
	}

	code, tok := owner.do("POST", "/api/v1/auth/tokens", map[string]string{"name": "Claude Code", "scope": "write"})
	if code != 200 {
		t.Fatalf("create token: %d %v", code, tok)
	}
	agent := newBrowser(t, srv.URL)
	agent.c.Jar = nil
	agent.hdr.Set("Authorization", "Bearer "+tok["token"].(string))
	if code, _ := agent.do("GET", "/api/v1/subscriptions", nil); code != 200 {
		t.Fatalf("token should work: %d", code)
	}
	_, list := owner.do("GET", "/api/v1/auth/tokens", nil)
	id := list["tokens"].([]any)[0].(map[string]any)["id"]
	owner.do("POST", "/api/v1/auth/tokens/revoke", map[string]any{"id": id})
	if code, _ := agent.do("GET", "/api/v1/subscriptions", nil); code != 401 {
		t.Fatalf("revoked token still works: %d", code)
	}

	code, link := owner.do("POST", "/api/v1/auth/links/telegram", nil)
	if code != 200 || link["url"] != "https://t.me/exhale_test_bot?start="+link["code"].(string) {
		t.Fatalf("telegram link: %d %v", code, link)
	}
}

func TestLoginIsThrottled(t *testing.T) {
	srv, _ := freshServer(t)
	b := newBrowser(t, srv.URL)
	b.do("POST", "/api/v1/auth/setup", alex)
	last := 0
	for range 15 {
		last, _ = b.do("POST", "/api/v1/auth/login", map[string]string{"email": "alex@example.com", "password": "guessing guessing"})
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("15 quick wrong logins should be throttled, last status %d", last)
	}
}
