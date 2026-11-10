package httpapi

import "testing"

// TestChoosingALanguage: the session says which language to show — the
// person's own, else the household's — and someone who joins in another
// language keeps it.
func TestChoosingALanguage(t *testing.T) {
	srv, _ := freshServer(t)
	owner := newBrowser(t, srv.URL)
	owner.do("POST", "/api/v1/auth/setup", alex) // a Spanish household
	if _, s := owner.do("GET", "/api/v1/auth/session", nil); s["language"] != "es" {
		t.Fatalf("the household's language: %v", s["language"])
	}
	_, s := owner.do("POST", "/api/v1/auth/language", map[string]string{"language": "en"})
	if s["language"] != "en" || s["user"].(map[string]any)["locale"] != "en" {
		t.Fatalf("their own language: %v", s)
	}
	if code, _ := owner.do("POST", "/api/v1/auth/language", map[string]string{"language": "xx"}); code != 422 {
		t.Fatalf("an unknown language: %d", code)
	}
	if _, s := owner.do("POST", "/api/v1/auth/language", map[string]string{"language": ""}); s["language"] != "es" {
		t.Fatalf("back to the household's: %v", s["language"])
	}

	_, inv := owner.do("POST", "/api/v1/auth/invites", map[string]string{"role": "member"})
	guest := newBrowser(t, srv.URL)
	_, s = guest.do("POST", "/api/v1/auth/join", map[string]string{"code": inv["code"].(string), "name": "Sam", "email": "sam@example.com", "password": "another long password", "language": "en"})
	if s["language"] != "en" {
		t.Fatalf("joined in English: %v", s["language"])
	}
	if code, _ := owner.do("POST", "/api/v1/household", map[string]string{"locale": "xx"}); code != 422 {
		t.Fatalf("an unknown household language: %d", code)
	}
}
