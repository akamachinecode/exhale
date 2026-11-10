package httpapi

import "testing"

// TestManagingMembers: an owner removes a member (their payments move to the
// owner), roles change, and there is always an owner left.
func TestManagingMembers(t *testing.T) {
	srv, _ := freshServer(t)
	owner := newBrowser(t, srv.URL)
	owner.do("POST", "/api/v1/auth/setup", alex)
	_, inv := owner.do("POST", "/api/v1/auth/invites", map[string]string{"role": "member"})
	guest := newBrowser(t, srv.URL)
	_, s := guest.do("POST", "/api/v1/auth/join", map[string]string{"code": inv["code"].(string), "name": "Marta", "email": "marta@example.com", "password": "another long password"})
	marta := s["user"].(map[string]any)["id"].(string)
	_, me := owner.do("GET", "/api/v1/auth/session", nil)
	alexID := me["user"].(map[string]any)["id"].(string)

	// Marta pays for something.
	code, sub := guest.do("POST", "/api/v1/subscriptions", map[string]any{"name": "Spotify", "price": "10.99", "currency": "EUR", "firstCharge": "2026-12-01"})
	if code != 200 {
		t.Fatalf("create: %d %v", code, sub)
	}
	subID := sub["subscription"].(map[string]any)["id"].(string)

	if code, _ := guest.do("POST", "/api/v1/members/remove", map[string]string{"id": alexID}); code != 403 {
		t.Fatalf("a member cannot remove someone else: %d", code)
	}
	if code, _ := guest.do("POST", "/api/v1/members/rename", map[string]string{"id": marta, "name": "Marta G."}); code != 200 {
		t.Fatalf("renaming yourself: %d", code)
	}
	if code, _ := owner.do("POST", "/api/v1/members/role", map[string]string{"id": alexID, "role": "member"}); code != 422 {
		t.Fatalf("the last owner cannot step down: %d", code)
	}
	if code, _ := owner.do("POST", "/api/v1/members/remove", map[string]string{"id": alexID}); code != 422 {
		t.Fatalf("the last owner cannot leave: %d", code)
	}
	if code, _ := owner.do("POST", "/api/v1/members/role", map[string]string{"id": marta, "role": "owner"}); code != 200 {
		t.Fatalf("promote: %d", code)
	}
	if code, _ := owner.do("POST", "/api/v1/members/role", map[string]string{"id": marta, "role": "member"}); code != 200 {
		t.Fatalf("demote: %d", code)
	}
	if code, _ := owner.do("POST", "/api/v1/members/remove", map[string]string{"id": marta}); code != 200 {
		t.Fatalf("remove: %d", code)
	}
	if code, _ := guest.do("GET", "/api/v1/household", nil); code != 401 {
		t.Fatalf("a removed member must be signed out: %d", code)
	}
	_, got := owner.do("GET", "/api/v1/subscriptions/"+subID, nil)
	if payer := got["subscription"].(map[string]any)["payerId"]; payer != alexID {
		t.Fatalf("their subscription must pass to the owner, payer = %v", payer)
	}
	if _, h := owner.do("GET", "/api/v1/household", nil); len(h["members"].([]any)) != 1 {
		t.Fatalf("members = %v", h["members"])
	}
}
