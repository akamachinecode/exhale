package webhook_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/channels/webhook"
	"github.com/akamachinecode/exhale/internal/testutil"
)

var reminder = actions.Message{
	Title: "🔔 Netflix renews in 3 days",
	Lines: []string{"💶 12,99 € · Fri, 13 Nov 2026", "💳 Visa ••4421"},
	Rows:  [][]actions.Button{{{Label: "✅ Already paid", Data: "paid|chg_1"}}},
	Event: &actions.Event{
		Kind: actions.EventReminder, SubscriptionID: "sub_1", Subscription: "Netflix", ChargeID: "chg_1",
		Amount: "12.99", Currency: "EUR", DueDate: "2026-11-13", DaysUntil: 3, PaymentMethod: "Visa ••4421", Path: "/subscriptions/sub_1",
	},
}

type hook struct {
	ch       *webhook.Channel
	requests []string // "METHOD path content-type user-agent"
	bodies   []string
	template string
}

func server(t *testing.T, handler http.HandlerFunc) *hook {
	t.Helper()
	h := &hook{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		h.requests = append(h.requests, strings.Join([]string{r.Method, r.URL.RequestURI(), r.Header.Get("Content-Type"), r.Header.Get("User-Agent")}, " "))
		h.bodies = append(h.bodies, string(raw))
		if handler != nil {
			handler(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	net := testutil.NewInternet().Host("hooks.test", srv.Listener.Addr().String()).PrivateHost("nas.lan", "10.0.0.5")
	h.ch = &webhook.Channel{
		Client:    net.Guard(nil).Client(5 * time.Second),
		Template:  func(context.Context, string) string { return h.template },
		PublicURL: "https://exhale.example.com",
		UserAgent: "Exhale/test",
	}
	return h
}

func TestDefaultBody(t *testing.T) {
	h := server(t, nil)
	id, err := h.ch.Send(context.Background(), "http://hooks.test/in/secret-token?x=1", reminder)
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("message id = %q; a notify-only channel returns none", id)
	}
	if len(h.requests) != 1 || h.requests[0] != "POST /in/secret-token?x=1 application/json Exhale/test" {
		t.Fatalf("requests = %v", h.requests)
	}
	want := `{"event":"reminder","title":"Netflix renews in 3 days","message":"💶 12,99 € · Fri, 13 Nov 2026\n💳 Visa ••4421","subscription":"Netflix","amount":"12.99","currency":"EUR","dueDate":"2026-11-13","daysUntil":3,"paymentMethod":"Visa ••4421","link":"https://exhale.example.com/subscriptions/sub_1"}`
	if h.bodies[0] != want {
		t.Fatalf("body =\n%s\nwant\n%s", h.bodies[0], want)
	}
}

// A message of a kind this package has never heard of still goes out, with
// the fields it has.
func TestDefaultBodyOfAnUnknownKind(t *testing.T) {
	body, err := webhook.DefaultBody(actions.Message{Title: "🎁 Your free trial ends tomorrow", Lines: []string{"Spotify"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"event":"message","title":"Your free trial ends tomorrow","message":"Spotify"}` {
		t.Fatalf("body = %s", body)
	}
}

func TestTemplate(t *testing.T) {
	h := server(t, nil)
	// Discord: text inside a string, a number outside one, spaces in braces.
	h.template = `{"content": "**{{title}}** {{ emoji }}\n{{message}}\n{{link}}", "days": {{daysUntil}}, "embeds": [{"title": "{{subscription}}", "description": "{{amount}} {{currency}} on {{dueDate}} · {{paymentMethod}} · {{event}}{{category}}"}]}`
	if _, err := h.ch.Send(context.Background(), "http://hooks.test/discord", reminder); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Content string
		Days    int
		Embeds  []struct{ Title, Description string }
	}
	if err := json.Unmarshal([]byte(h.bodies[0]), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, h.bodies[0])
	}
	if got.Content != "**Netflix renews in 3 days** 🔔\n💶 12,99 € · Fri, 13 Nov 2026\n💳 Visa ••4421\nhttps://exhale.example.com/subscriptions/sub_1" {
		t.Fatalf("content = %q", got.Content)
	}
	if got.Days != 3 || got.Embeds[0].Title != "Netflix" || got.Embeds[0].Description != "12.99 EUR on 2026-11-13 · Visa ••4421 · reminder" {
		t.Fatalf("got = %+v", got)
	}
}

func TestRender(t *testing.T) {
	vars := webhook.Vars(actions.Message{
		Title: `🔔 "Quotes" \ and <tags> renew`,
		Lines: []string{"line one", "tab\there"},
		Event: &actions.Event{Subscription: `Say "hi"`, DaysUntil: -2},
	}, "")
	out, err := webhook.Render(`{"a":"{{title}}","b":"{{message}}","c":"{{subscription}}","d":{{daysUntil}},"e":"{{text}}"}`, vars)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got["a"] != `"Quotes" \ and <tags> renew` || got["b"] != "line one\ntab\there" || got["c"] != `Say "hi"` || got["d"] != float64(-2) {
		t.Fatalf("got = %#v", got)
	}
	// A value cannot break out of its string and add fields.
	out, err = webhook.Render(`{"a":"{{title}}"}`, map[string]string{"title": `x","admin":true,"b":"`})
	if err != nil || strings.Contains(string(out), `"admin":true`) {
		t.Fatalf("out = %s, %v", out, err)
	}

	for name, tmpl := range map[string]string{
		"unknown variable":    `{"a":"{{nope}}"}`,
		"text outside quotes": `{"a":{{title}}}`,
		"not json":            `content={{title}}`,
		"unfinished":          `{"a":"{{title}}"`,
	} {
		if _, err := webhook.Render(tmpl, vars); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := webhook.ValidateTemplate(""); err != nil {
		t.Fatal(err)
	}
	if err := webhook.ValidateTemplate(`{"text":"{{text}}"}`); err != nil {
		t.Fatal(err)
	}
	if err := webhook.ValidateTemplate(`{"text":"` + strings.Repeat("x", webhook.MaxTemplate) + `"}`); err == nil {
		t.Fatal("a huge template was accepted")
	}
	// Every documented variable exists.
	for _, name := range webhook.Variables {
		if _, ok := vars[name]; !ok {
			t.Errorf("variable %s is documented but not set", name)
		}
	}
	if len(vars) != len(webhook.Variables) {
		t.Errorf("%d variables set, %d documented", len(vars), len(webhook.Variables))
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("not 2xx", func(t *testing.T) {
		h := server(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"message": "Cannot send an empty message"}`)
		})
		_, err := h.ch.Send(ctx, "http://hooks.test/in/secret-token", reminder)
		if err == nil || !strings.Contains(err.Error(), `hooks.test answered 400: {"message": "Cannot send an empty message"}`) {
			t.Fatalf("error = %v", err)
		}
		if strings.Contains(err.Error(), "secret-token") {
			t.Fatalf("the URL is in the error: %v", err)
		}
	})
	t.Run("redirect is not followed", func(t *testing.T) {
		h := server(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://nas.lan/admin", http.StatusTemporaryRedirect)
		})
		_, err := h.ch.Send(ctx, "http://hooks.test/in", reminder)
		if err == nil || !strings.Contains(err.Error(), "redirect") || len(h.requests) != 1 {
			t.Fatalf("error = %v, requests = %v", err, h.requests)
		}
	})
	t.Run("private address", func(t *testing.T) {
		h := server(t, nil)
		_, err := h.ch.Send(ctx, "http://nas.lan/in/secret-token", reminder)
		if err == nil || !strings.Contains(err.Error(), "private network") || len(h.requests) != 0 {
			t.Fatalf("error = %v, requests = %v", err, h.requests)
		}
		if strings.Contains(err.Error(), "secret-token") {
			t.Fatalf("the URL is in the error: %v", err)
		}
	})
	t.Run("loopback", func(t *testing.T) {
		h := server(t, nil)
		if _, err := h.ch.Send(ctx, "http://127.0.0.1:8484/api/v1/household", reminder); err == nil || !strings.Contains(err.Error(), "this machine") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		h := server(t, func(http.ResponseWriter, *http.Request) { <-release })
		defer close(release)
		h.ch.Client.Timeout = 50 * time.Millisecond
		_, err := h.ch.Send(ctx, "http://hooks.test/in/secret-token", reminder)
		if err == nil || !strings.Contains(err.Error(), "timed out") || strings.Contains(err.Error(), "secret-token") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("bad template", func(t *testing.T) {
		h := server(t, nil)
		h.template = `{"a":{{title}}}`
		if _, err := h.ch.Send(ctx, "http://hooks.test/in", reminder); err == nil || len(h.requests) != 0 {
			t.Fatalf("error = %v, requests = %v", err, h.requests)
		}
	})
}
