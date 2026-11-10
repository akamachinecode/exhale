package httpapi

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TOTP
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"
)

// totp computes an authenticator app's code for the step now+offset, on its
// own (not with the code under test).
func totp(t *testing.T, secret string, offset int64) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(time.Now().Unix()/30+offset)) //nolint:gosec // test
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[19] & 0x0f
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff%1_000_000)
}

// freshStep waits for a new 30-second step when the current one is about to
// end, so a test that uses codes of neighbouring steps does not cross two.
func freshStep() {
	if left := 30 - time.Now().Unix()%30; left < 5 {
		time.Sleep(time.Duration(left)*time.Second + 100*time.Millisecond)
	}
}

// enrol turns two-factor on for the browser's account and returns the
// secret and the backup codes. The first code is of the previous step, so
// the current and next ones are left for signing in.
func enrol(t *testing.T, b *browser, password string) (string, []string) {
	t.Helper()
	if code, _ := b.do("POST", "/api/v1/auth/two-factor/setup", map[string]string{"password": "wrong password!"}); code != 422 {
		t.Fatalf("setup with a wrong password: %d", code)
	}
	code, setup := b.do("POST", "/api/v1/auth/two-factor/setup", map[string]string{"password": password})
	if code != 200 {
		t.Fatalf("setup: %d %v", code, setup)
	}
	secret := setup["secret"].(string)
	if uri := setup["uri"].(string); !strings.HasPrefix(uri, "otpauth://totp/Exhale:") || !strings.Contains(uri, "secret="+secret) {
		t.Fatalf("uri = %s", uri)
	}
	if code, st := b.do("GET", "/api/v1/auth/two-factor", nil); code != 200 || st["enabled"] != false {
		t.Fatalf("on before the first code: %d %v", code, st)
	}
	if code, _ := b.do("POST", "/api/v1/auth/two-factor/enable", map[string]string{"code": "000000"}); code != 422 {
		t.Fatalf("enable with a wrong code: %d", code)
	}
	code, out := b.do("POST", "/api/v1/auth/two-factor/enable", map[string]string{"code": totp(t, secret, -1)})
	if code != 200 {
		t.Fatalf("enable: %d %v", code, out)
	}
	var backups []string
	for _, c := range out["backupCodes"].([]any) {
		backups = append(backups, c.(string))
	}
	if len(backups) != 10 {
		t.Fatalf("backup codes: %v", backups)
	}
	if code, _ := b.do("POST", "/api/v1/auth/two-factor/setup", map[string]string{"password": password}); code != 409 {
		t.Fatalf("a second setup while on must be refused: %d", code)
	}
	return secret, backups
}

// login enters the password and returns the pending-login token.
func login(t *testing.T, b *browser, email, password string) string {
	t.Helper()
	code, s := b.do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != 200 || s["user"] != nil {
		t.Fatalf("password step: %d %v", code, s)
	}
	step, _ := s["twoFactor"].(map[string]any)
	if step == nil || step["token"] == "" {
		t.Fatalf("no two-factor step: %v", s)
	}
	return step["token"].(string)
}

// TestTwoFactorSignIn: password, then code, then a session; a wrong code,
// a replayed code and a used backup code do not sign in.
func TestTwoFactorSignIn(t *testing.T) {
	freshStep()
	// This test tries more codes than the throttle allows at once; see
	// TestTwoFactorThrottle.
	saved := codeRate
	codeRate.burst, codeRate.ipBurst = 100, 100
	t.Cleanup(func() { codeRate = saved })
	srv, _ := freshServer(t)
	b := newBrowser(t, srv.URL)
	b.do("POST", "/api/v1/auth/setup", alex)
	_, tok := b.do("POST", "/api/v1/auth/tokens", map[string]string{"name": "script", "scope": "read"})
	elsewhere := newBrowser(t, srv.URL)
	elsewhere.do("POST", "/api/v1/auth/login", map[string]string{"email": alex.Email, "password": alex.Password})
	secret, backups := enrol(t, b, alex.Password)
	if code, _ := elsewhere.do("GET", "/api/v1/household", nil); code != 401 {
		t.Fatalf("turning it on must sign out the other sessions: %d", code)
	}
	if code, st := b.do("GET", "/api/v1/auth/two-factor", nil); code != 200 || st["enabled"] != true || st["backupCodesLeft"] != float64(10) {
		t.Fatalf("status: %d %v", code, st)
	}
	_, sess := b.do("GET", "/api/v1/auth/session", nil)
	if sess["user"] == nil {
		t.Fatal("turning it on must keep the session that did it")
	}
	b.do("POST", "/api/v1/auth/logout", nil)

	// The password alone is not a session.
	token := login(t, b, "alex@example.com", alex.Password)
	if code, _ := b.do("GET", "/api/v1/household", nil); code != 401 {
		t.Fatalf("signed in before the code: %d", code)
	}
	if code, _ := b.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": "000000"}); code != 401 {
		t.Fatalf("wrong code: %d", code)
	}
	if code, _ := b.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": "made-up", "code": totp(t, secret, 0)}); code != 410 {
		t.Fatalf("unknown pending login: %d", code)
	}
	if code, _ := b.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": totp(t, secret, -1)}); code != 401 {
		t.Fatalf("the code used to turn it on works again: %d", code)
	}
	code, s := b.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": totp(t, secret, 0)})
	if code != 200 || s["user"] == nil {
		t.Fatalf("code: %d %v", code, s)
	}
	if code, _ := b.do("GET", "/api/v1/household", nil); code != 200 {
		t.Fatalf("not signed in after the code: %d", code)
	}
	if code, _ := b.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": totp(t, secret, 1)}); code != 410 {
		t.Fatalf("a pending login works once: %d", code)
	}

	// Replay: the same code on another sign-in.
	other := newBrowser(t, srv.URL)
	token = login(t, other, "alex@example.com", alex.Password)
	if code, _ := other.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": totp(t, secret, 0)}); code != 401 {
		t.Fatalf("replayed code: %d", code)
	}
	// A backup code, with the dash and capitals people type, works once.
	if code, _ := other.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": " " + strings.ToUpper(backups[0]) + " "}); code != 200 {
		t.Fatalf("backup code: %d", code)
	}
	third := newBrowser(t, srv.URL)
	token = login(t, third, "alex@example.com", alex.Password)
	if code, _ := third.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": backups[0]}); code != 401 {
		t.Fatalf("backup code used twice: %d", code)
	}
	if code, _ := third.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": strings.ReplaceAll(backups[1], "-", "")}); code != 200 {
		t.Fatalf("second backup code: %d", code)
	}
	if _, st := b.do("GET", "/api/v1/auth/two-factor", nil); st["backupCodesLeft"] != float64(8) {
		t.Fatalf("backup codes left: %v", st)
	}

	// API tokens do not ask for codes.
	api := newBrowser(t, srv.URL)
	api.hdr.Set("Authorization", "Bearer "+tok["token"].(string))
	if code, _ := api.do("GET", "/api/v1/subscriptions", nil); code != 200 {
		t.Fatalf("API token after two-factor: %d", code)
	}

	// New backup codes replace the old ones.
	code, fresh := b.do("POST", "/api/v1/auth/two-factor/backup-codes", map[string]string{"password": alex.Password})
	if code != 200 || len(fresh["backupCodes"].([]any)) != 10 {
		t.Fatalf("new codes: %d %v", code, fresh)
	}
	fourth := newBrowser(t, srv.URL)
	token = login(t, fourth, "alex@example.com", alex.Password)
	if code, _ := fourth.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": backups[2]}); code != 401 {
		t.Fatalf("an old backup code still works: %d", code)
	}

	_, log := b.do("GET", "/api/v1/changes", nil)
	var actions []string
	for _, e := range log["entries"].([]any) {
		actions = append(actions, e.(map[string]any)["action"].(string))
	}
	if got := strings.Join(actions, " "); got != "two_factor.backup_codes two_factor.enable" {
		t.Fatalf("audit log: %s", got)
	}
}

// TestTwoFactorDisable: turning it off takes the password and a code; new
// backup codes take the password.
func TestTwoFactorDisable(t *testing.T) {
	freshStep()
	srv, _ := freshServer(t)
	b := newBrowser(t, srv.URL)
	b.do("POST", "/api/v1/auth/setup", alex)
	secret, _ := enrol(t, b, alex.Password)
	if code, _ := b.do("POST", "/api/v1/auth/two-factor/backup-codes", map[string]string{"password": "wrong password!"}); code != 422 {
		t.Fatalf("new codes with a wrong password: %d", code)
	}
	// Turning it off takes the password and a code.
	if code, _ := b.do("POST", "/api/v1/auth/two-factor/disable", map[string]string{"password": alex.Password, "code": "000000"}); code != 422 {
		t.Fatalf("disable with a wrong code: %d", code)
	}
	if code, _ := b.do("POST", "/api/v1/auth/two-factor/disable", map[string]string{"password": "wrong password!", "code": totp(t, secret, 1)}); code != 422 {
		t.Fatalf("disable with a wrong password: %d", code)
	}
	if code, _ := b.do("POST", "/api/v1/auth/two-factor/disable", map[string]string{"password": alex.Password, "code": totp(t, secret, 1)}); code != 200 {
		t.Fatalf("disable: %d", code)
	}
	code, s := newBrowser(t, srv.URL).do("POST", "/api/v1/auth/login", map[string]string{"email": "alex@example.com", "password": alex.Password})
	if code != 200 || s["user"] == nil || s["twoFactor"] != nil {
		t.Fatalf("password alone after turning it off: %d %v", code, s)
	}
	_, log := b.do("GET", "/api/v1/changes", nil)
	if top := log["entries"].([]any)[0].(map[string]any); top["action"] != "two_factor.disable" || top["actor"] != "Alex" {
		t.Fatalf("audit: %v", top)
	}
}

// TestTwoFactorThrottle: codes are throttled per member, whatever the
// pending login, and a pending login dies after a few wrong codes.
func TestTwoFactorThrottle(t *testing.T) {
	freshStep()
	srv, _ := freshServer(t)
	b := newBrowser(t, srv.URL)
	b.do("POST", "/api/v1/auth/setup", alex)
	secret, _ := enrol(t, b, alex.Password)
	// enrol tried two codes: 3 are left at once for this member, whatever
	// the pending login.
	b = newBrowser(t, srv.URL)
	token := login(t, b, "alex@example.com", alex.Password)
	for range 2 {
		if code, _ := b.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": "000000"}); code != 401 {
			t.Fatalf("wrong code: %d", code)
		}
	}
	token = login(t, b, "alex@example.com", alex.Password)
	if code, _ := b.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": "000000"}); code != 401 {
		t.Fatalf("wrong code: %d", code)
	}
	if code, _ := b.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": totp(t, secret, 0)}); code != 429 {
		t.Fatalf("the right code past the limit must wait: %d", code)
	}

	// A pending login takes 3 wrong codes, then the password is asked again.
	srv, _ = freshServer(t)
	b = newBrowser(t, srv.URL)
	b.do("POST", "/api/v1/auth/setup", alex)
	enrol(t, b, alex.Password)
	b = newBrowser(t, srv.URL)
	token = login(t, b, "alex@example.com", alex.Password)
	codes := []int{}
	for range 4 {
		code, _ := b.do("POST", "/api/v1/auth/login/two-factor", map[string]string{"token": token, "code": "000000"})
		codes = append(codes, code)
	}
	if fmt.Sprint(codes) != "[401 401 401 410]" {
		t.Fatalf("codes: %v", codes)
	}
}

// TestOwnerResetsTwoFactor: an owner turns a member's two-factor off from
// member management, and it is in the activity log; a member cannot.
func TestOwnerResetsTwoFactor(t *testing.T) {
	freshStep()
	srv, _ := freshServer(t)
	owner := newBrowser(t, srv.URL)
	owner.do("POST", "/api/v1/auth/setup", alex)
	_, inv := owner.do("POST", "/api/v1/auth/invites", map[string]string{"role": "member"})
	guest := newBrowser(t, srv.URL)
	_, s := guest.do("POST", "/api/v1/auth/join", map[string]string{"code": inv["code"].(string), "name": "Marta", "email": "marta@example.com", "password": "another long password"})
	marta := s["user"].(map[string]any)["id"].(string)
	_, me := owner.do("GET", "/api/v1/auth/session", nil)
	alexID := me["user"].(map[string]any)["id"].(string)

	enrol(t, guest, "another long password")
	enrol(t, owner, alex.Password)
	if code, _ := guest.do("GET", "/api/v1/members/two-factor", nil); code != 403 {
		t.Fatalf("a member lists who uses two-factor: %d", code)
	}
	if code, _ := guest.do("POST", "/api/v1/members/two-factor/reset", map[string]string{"id": alexID}); code != 403 {
		t.Fatalf("a member turns off the owner's two-factor: %d", code)
	}
	code, list := owner.do("GET", "/api/v1/members/two-factor", nil)
	if code != 200 || len(list["members"].([]any)) != 2 {
		t.Fatalf("list: %d %v", code, list)
	}
	if code, _ := owner.do("POST", "/api/v1/members/two-factor/reset", map[string]string{"id": alexID}); code != 422 {
		t.Fatalf("owners turn their own off from their settings: %d", code)
	}
	if code, _ := owner.do("POST", "/api/v1/members/two-factor/reset", map[string]string{"id": marta}); code != 200 {
		t.Fatalf("reset: %d", code)
	}
	if code, _ := owner.do("POST", "/api/v1/members/two-factor/reset", map[string]string{"id": marta}); code != 409 {
		t.Fatalf("reset twice: %d", code)
	}
	code, s = newBrowser(t, srv.URL).do("POST", "/api/v1/auth/login", map[string]string{"email": "marta@example.com", "password": "another long password"})
	if code != 200 || s["user"] == nil {
		t.Fatalf("Marta signs in with the password alone: %d %v", code, s)
	}
	_, log := owner.do("GET", "/api/v1/changes", nil)
	top := log["entries"].([]any)[0].(map[string]any)
	if top["action"] != "two_factor.reset" || top["actor"] != "Alex" || top["entityId"] != marta {
		t.Fatalf("audit: %v", top)
	}
	if code, _ := owner.do("POST", "/api/v1/changes/undo", map[string]string{"auditId": top["id"].(string)}); code == 200 {
		t.Fatal("a two-factor reset must not be undone from the log")
	}
}

// TestSSOSkipsTwoFactor: single sign-on leaves the second factor to the
// identity provider; the password of the same account still asks for it.
func TestSSOSkipsTwoFactor(t *testing.T) {
	freshStep()
	e := ssoServer(t, nil).withOwner(t)
	b := newBrowser(t, e.srv.URL)
	b.do("POST", "/api/v1/auth/login", map[string]string{"email": alex.Email, "password": alex.Password})
	enrol(t, b, alex.Password)
	b.do("POST", "/api/v1/auth/logout", nil)

	e.idp.set(func(p *fakeIdP) { p.user = idpUser{Sub: "u-alex", Email: alex.Email, Verified: true} })
	if got := b.nav(ssoStartPath); got != "/" || !b.signedIn() {
		t.Fatalf("single sign-on must not ask for a code: landed on %s", got)
	}
	b.do("POST", "/api/v1/auth/logout", nil)
	login(t, newBrowser(t, e.srv.URL), alex.Email, alex.Password)
}
