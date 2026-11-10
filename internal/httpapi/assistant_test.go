package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/assistant"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/store"
)

// assistantServer is a server whose assistant the test controls.
func assistantServer(t *testing.T) (*httptest.Server, *app.Service, *assistant.Assistant) {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := app.New(st, bus.New(), nil, func() time.Time { return time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC) })
	asst, err := assistant.New(svc, nil, assistant.Env{})
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := New(svc, Config{Version: "test", Assistant: asst})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, svc, asst
}

// household signs up an owner and a member, each in its own browser.
func household(t *testing.T, base string) (owner, member *browser) {
	t.Helper()
	owner = newBrowser(t, base)
	if code, _ := owner.do("POST", "/api/v1/auth/setup", alex); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	_, inv := owner.do("POST", "/api/v1/auth/invites", map[string]string{"role": "member"})
	member = newBrowser(t, base)
	join := map[string]string{"code": inv["code"].(string), "name": "Marta", "email": "marta@example.com", "password": "another long password"}
	if code, _ := member.do("POST", "/api/v1/auth/join", join); code != 200 {
		t.Fatalf("join: %d", code)
	}
	return owner, member
}

// TestAssistantSettings: off by default; only an owner sets it up; the key
// goes in and never comes out.
func TestAssistantSettings(t *testing.T) {
	srv, _, _ := assistantServer(t)
	owner, member := household(t, srv.URL)

	if _, s := owner.do("GET", "/api/v1/auth/session", nil); s["assistant"] != false {
		t.Fatalf("the assistant must be off until it is set up: %v", s["assistant"])
	}
	if code, st := member.do("GET", "/api/v1/settings/assistant", nil); code != 200 || st["enabled"] != false || st["configured"] != false {
		t.Fatalf("status: %d %v", code, st)
	}
	if code, body := owner.do("POST", "/api/v1/assistant/messages", map[string]any{"text": "hello"}); code != 409 || !strings.Contains(body["detail"].(string), "is off") {
		t.Fatalf("asking while off: %d %v", code, body)
	}
	settings := map[string]any{"provider": "anthropic", "apiKey": "sk-ant-very-secret"}
	for _, path := range []string{"/api/v1/settings/assistant", "/api/v1/settings/assistant/test", "/api/v1/settings/assistant/remove"} {
		if code, _ := member.do("POST", path, settings); code != 403 {
			t.Fatalf("a member reached %s: %d", path, code)
		}
	}
	if code, _ := newBrowser(t, srv.URL).do("GET", "/api/v1/settings/assistant", nil); code != 401 {
		t.Fatalf("signed out: %d", code)
	}
	if code, body := owner.do("POST", "/api/v1/settings/assistant", map[string]any{"provider": "anthropic"}); code != 422 || !strings.Contains(body["detail"].(string), "API key") {
		t.Fatalf("no key: %d %v", code, body)
	}
	code, st := owner.do("POST", "/api/v1/settings/assistant", settings)
	if code != 200 || st["enabled"] != true || st["hasKey"] != true || st["model"] != assistant.DefaultAnthropicModel {
		t.Fatalf("configure: %d %v", code, st)
	}
	for _, b := range []*browser{owner, member} {
		_, st := b.do("GET", "/api/v1/settings/assistant", nil)
		_, session := b.do("GET", "/api/v1/auth/session", nil)
		if raw, _ := json.Marshal([]any{st, session}); strings.Contains(string(raw), "sk-ant") {
			t.Fatalf("the key came back: %s", raw)
		}
		if session["assistant"] != true {
			t.Fatalf("the web must know the assistant is on: %v", session)
		}
	}
	if _, s := newBrowser(t, srv.URL).do("GET", "/api/v1/auth/session", nil); s["assistant"] != false {
		t.Fatal("someone signed out must not learn whether the assistant is on")
	}
	if code, st := owner.do("POST", "/api/v1/settings/assistant/remove", nil); code != 200 || st["configured"] != false || st["hasKey"] != false {
		t.Fatalf("remove: %d %v", code, st)
	}
}

// TestAskExhale is the web conversation: ask, get a change to confirm,
// confirm it, undo it. Another member cannot decide for you.
func TestAskExhale(t *testing.T) {
	srv, svc, asst := assistantServer(t)
	owner, member := household(t, srv.URL)
	model := &assistant.Fake{}
	asst.SetProvider(model)
	model.Then(
		assistant.Call("create_subscription", map[string]any{"name": "Netflix", "price": "13.99", "currency": "EUR", "firstCharge": "2026-11-12"}),
		assistant.Say("I prepared Netflix."),
	)
	code, turn := owner.do("POST", "/api/v1/assistant/messages", map[string]any{"text": "add netflix 13.99 on the 12th"})
	if code != 200 || turn["text"] != "I prepared Netflix." || len(turn["proposals"].([]any)) != 1 {
		t.Fatalf("ask: %d %v", code, turn)
	}
	p := turn["proposals"].([]any)[0].(map[string]any)
	title, _ := p["title"].(map[string]any)
	if title["key"] != "Add {0}" || title["args"].([]any)[0] != "Netflix" || p["destructive"] != false {
		t.Fatalf("proposal: %v", p)
	}
	path := "/api/v1/assistant/proposals/" + p["id"].(string)
	if code, _ := member.do("POST", path, map[string]any{"action": "confirm"}); code != 404 {
		t.Fatalf("another member confirmed it: %d", code)
	}
	if _, l := owner.do("GET", "/api/v1/subscriptions", nil); len(l["subscriptions"].([]any)) != 0 {
		t.Fatal("it exists before being confirmed")
	}
	code, out := owner.do("POST", path, map[string]any{"action": "confirm"})
	if code != 200 || out["status"] != "applied" || out["canUndo"] != true || out["subscriptionId"] == nil {
		t.Fatalf("confirm: %d %v", code, out)
	}
	_, l := owner.do("GET", "/api/v1/subscriptions", nil)
	if subs := l["subscriptions"].([]any); len(subs) != 1 || subs[0].(map[string]any)["id"] != out["subscriptionId"] {
		t.Fatalf("after confirming: %v", l)
	}
	if code, out := owner.do("POST", path, map[string]any{"action": "undo"}); code != 200 || out["status"] != "undone" {
		t.Fatalf("undo: %d %v", code, out)
	}
	if _, l := owner.do("GET", "/api/v1/subscriptions", nil); len(l["subscriptions"].([]any)) != 0 {
		t.Fatal("it is still there after undo")
	}
	if code, _ := owner.do("POST", "/api/v1/assistant/proposals/pnothere", map[string]any{"action": "confirm"}); code != 404 {
		t.Fatalf("an unknown proposal: %d", code)
	}

	// A follow-up carries the conversation; reset forgets it.
	model.Then(assistant.Say("Yes."), assistant.Say("Hello."))
	owner.do("POST", "/api/v1/assistant/messages", map[string]any{"text": "are you sure?"})
	if n := len(model.Requests[len(model.Requests)-1].Messages); n != 3 {
		t.Fatalf("the follow-up must include the turn before: %d messages", n)
	}
	owner.do("POST", "/api/v1/assistant/messages", map[string]any{"text": "hi", "reset": true})
	if n := len(model.Requests[len(model.Requests)-1].Messages); n != 1 {
		t.Fatalf("after a reset the conversation starts again: %d messages", n)
	}

	// An API token is an agent, not a person at the web: it does not get a
	// second model to talk to.
	_, tok := owner.do("POST", "/api/v1/auth/tokens", map[string]string{"name": "script", "scope": "write"})
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/assistant/messages", strings.NewReader(`{"text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok["token"].(string))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("a token asked the assistant: %d", res.StatusCode)
	}

	// A failing provider is a sentence, with a status the web can tell apart.
	model.Then(assistant.Fail(&assistant.Error{Kind: assistant.KindAuth, Detail: "invalid x-api-key"}))
	if code, body := owner.do("POST", "/api/v1/assistant/messages", map[string]any{"text": "hi"}); code != 502 || !strings.Contains(body["detail"].(string), "rejected the API key") || strings.Contains(body["detail"].(string), "x-api-key") {
		t.Fatalf("provider failure: %d %v", code, body)
	}
	_ = svc
}

func upload(t *testing.T, b *browser, data []byte) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", b.base+"/api/v1/import/parse", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := b.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// TestImportStatement: the same upload that reads a spreadsheet reads a bank
// export by itself, and a PDF or a picture through the model when it is on.
func TestImportStatement(t *testing.T) {
	srv, _, asst := assistantServer(t)
	owner, _ := household(t, srv.URL)
	pdf := []byte("%PDF-1.7\nstatement")

	// Assistant off: a PDF is refused with what to do; nothing else changes.
	code, body := upload(t, owner, pdf)
	if code != 422 || !strings.Contains(body["detail"].(string), "needs the built-in assistant") {
		t.Fatalf("pdf with the assistant off: %d %v", code, body)
	}
	if code, body := upload(t, owner, []byte("PK\x03\x04\x00\x00\x00a zip")); code != 422 || !strings.Contains(body["detail"].(string), "not a Wallos database") {
		t.Fatalf("unsupported file: %d %v", code, body)
	}
	bank := "Date,Description,Amount,Balance\n2026-08-06,NETFLIX.COM 866-579,-13.99,100\n2026-09-06,NETFLIX.COM 866-579,-13.99,86\n2026-10-06,NETFLIX.COM 866-579,-13.99,72\n2026-10-09,BOOKSHOP,-20.00,52\n"
	code, body = upload(t, owner, []byte(bank))
	if code != 200 || body["source"] != "transactions" || len(body["items"].([]any)) != 1 || body["model"] != nil {
		t.Fatalf("bank export with the assistant off: %d %v", code, body)
	}
	if it := body["items"].([]any)[0].(map[string]any); it["name"] != "Netflix" || it["price"] != "13.99" || it["nextCharge"] != "2026-12-06" {
		t.Fatalf("item: %v", it)
	}

	// Assistant on: the model proposes, and the list goes through the same
	// dry run as every import.
	model := &assistant.Fake{}
	asst.SetProvider(model)
	model.Then(assistant.Call("report_recurring_charges", map[string]any{"items": []map[string]any{
		{"name": "Netflix", "price": "13.99", "currency": "EUR", "unit": "month", "nextCharge": "2026-12-06", "remark": "3 charges on the 6th"},
		{"name": "Gym", "price": "29", "currency": "EUR", "unit": "month", "nextCharge": "2026-12-01", "remark": "2 charges on the 1st"},
	}}))
	code, body = upload(t, owner, pdf)
	if code != 200 || body["source"] != "statement" || len(body["items"].([]any)) != 2 || body["model"] == nil {
		t.Fatalf("pdf with the assistant on: %d %v", code, body)
	}
	if _, l := owner.do("GET", "/api/v1/subscriptions", nil); len(l["subscriptions"].([]any)) != 0 {
		t.Fatal("reading a statement saved something")
	}
	code, dry := owner.do("POST", "/api/v1/import", map[string]any{"items": body["items"], "dryRun": true})
	if code != 200 || dry["created"].(float64) != 2 {
		t.Fatalf("dry run: %d %v", code, dry)
	}
	// The person ticks one of the two.
	code, done := owner.do("POST", "/api/v1/import", map[string]any{"items": body["items"].([]any)[:1]})
	if code != 200 || done["created"].(float64) != 1 {
		t.Fatalf("import: %d %v", code, done)
	}

	// Oversized: refused before any model sees it.
	calls := model.Calls()
	big := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("a"), assistant.MaxImageSize)...)
	if code, body := upload(t, owner, big); code != 422 || !strings.Contains(body["detail"].(string), "larger than 5 MB") {
		t.Fatalf("a big picture: %d %v", code, body)
	}
	huge := bytes.Repeat([]byte("a"), 22<<20)
	if code, _ := upload(t, owner, huge); code != 413 {
		t.Fatalf("a file over the upload limit: %d", code)
	}
	if model.Calls() != calls {
		t.Fatal("a refused file reached the model")
	}
	model.Then(assistant.Fail(&assistant.Error{Kind: assistant.KindNoAnswer}))
	if code, body := upload(t, owner, pdf); code != 502 || !strings.Contains(body["detail"].(string), "did not answer") {
		t.Fatalf("a model that does not answer: %d %v", code, body)
	}
}
