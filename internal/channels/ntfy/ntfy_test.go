package ntfy_test

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
	"github.com/akamachinecode/exhale/internal/channels/ntfy"
	"github.com/akamachinecode/exhale/internal/testutil"
)

var reminder = actions.Message{
	Title: "🔔 Netflix renews in 3 days",
	Lines: []string{"💶 12,99 € · Fri, 13 Nov 2026", "💳 Visa ••4421"},
	Rows:  [][]actions.Button{{{Label: "✅ Already paid", Data: "paid|chg_1"}}},
	Event: &actions.Event{Kind: actions.EventReminder, Subscription: "Netflix", Amount: "12.99", Currency: "EUR", DueDate: "2026-11-13", DaysUntil: 3, Path: "/subscriptions/sub_1"},
}

type request struct {
	path, auth, contentType string
	body                    map[string]any
}

// server is a fake ntfy server reachable as https-less "ntfy.test".
func server(t *testing.T, status int, answer string) (*ntfy.Channel, *[]request, *ntfy.Config) {
	t.Helper()
	var got []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		got = append(got, request{r.Method + " " + r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), body})
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	net := testutil.NewInternet().Host("ntfy.test", srv.Listener.Addr().String()).PrivateHost("ntfy.lan", "192.168.1.9")
	cfg := &ntfy.Config{Server: "http://ntfy.test"}
	ch := &ntfy.Channel{
		Config:    func(context.Context) ntfy.Config { return *cfg },
		Client:    net.Guard(nil).Client(5 * time.Second),
		PublicURL: "https://exhale.example.com",
	}
	return ch, &got, cfg
}

func TestPublish(t *testing.T) {
	ch, got, cfg := server(t, 200, `{"id":"abc"}`)
	cfg.Token = "tk_secret"
	id, err := ch.Send(context.Background(), "alex-reminders", reminder)
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("message id = %q; a notify-only channel returns none", id)
	}
	if len(*got) != 1 {
		t.Fatalf("requests = %d", len(*got))
	}
	r := (*got)[0]
	if r.path != "POST /" || r.auth != "Bearer tk_secret" || r.contentType != "application/json" {
		t.Fatalf("request = %+v", r)
	}
	link := "https://exhale.example.com/subscriptions/sub_1"
	want := map[string]any{
		"topic":    "alex-reminders",
		"title":    "Netflix renews in 3 days",
		"message":  "💶 12,99 € · Fri, 13 Nov 2026\n💳 Visa ••4421",
		"tags":     []any{"bell"},
		"priority": float64(3),
		"click":    link,
		"actions":  []any{map[string]any{"action": "view", "label": "Open in Exhale", "url": link}},
	}
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(r.body)
	if string(a) != string(b) {
		t.Fatalf("body =\n%s\nwant\n%s", b, a)
	}
}

func TestFromMessage(t *testing.T) {
	// No public address: no click, no button. No token: no header.
	n := ntfy.FromMessage("t", reminder, "")
	if n.Click != "" || len(n.Actions) != 0 {
		t.Fatalf("link without a public address: %+v", n)
	}
	// A failed charge stands out.
	failed := actions.Message{Title: "⚠️ Netflix failed — did it go through later?", Event: &actions.Event{Kind: actions.EventFailed}}
	if n := ntfy.FromMessage("t", failed, ""); n.Priority != 4 || n.Tags[0] != "warning" || n.Title != "Netflix failed — did it go through later?" {
		t.Fatalf("failed = %+v", n)
	}
	// A new kind of message with an emoji ntfy has no tag for keeps it, and
	// sections become text.
	other := actions.Message{
		Title:    "🎁 Your free trial ends tomorrow",
		Sections: []actions.Section{{Heading: "After the trial", Table: [][]string{{"Monthly", "9.99"}, {"Yearly", "99.00"}}}},
		Footer:   "Cancel any time",
	}
	n = ntfy.FromMessage("t", other, "")
	if n.Title != "🎁 Your free trial ends tomorrow" || len(n.Tags) != 0 || n.Priority != 3 {
		t.Fatalf("other = %+v", n)
	}
	if n.Message != "After the trial\nMonthly   9.99\nYearly   99.00\n\nCancel any time" {
		t.Fatalf("message = %q", n.Message)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("token refused", func(t *testing.T) {
		ch, _, cfg := server(t, 401, `{"code":40101,"http":401,"error":"unauthorized"}`)
		cfg.Token = "tk_secret"
		_, err := ch.Send(ctx, "t", reminder)
		if err == nil || !strings.Contains(err.Error(), "refused the access token") || strings.Contains(err.Error(), "tk_secret") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("token needed", func(t *testing.T) {
		ch, _, _ := server(t, 403, `{"error":"forbidden"}`)
		if _, err := ch.Send(ctx, "t", reminder); err == nil || !strings.Contains(err.Error(), "needs an access token") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("server error", func(t *testing.T) {
		ch, _, _ := server(t, 429, `{"error":"limit reached: too many requests"}`)
		if _, err := ch.Send(ctx, "t", reminder); err == nil || !strings.Contains(err.Error(), "ntfy.test answered 429: limit reached") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("not ntfy", func(t *testing.T) {
		ch, _, _ := server(t, 404, `<html>not found</html>`)
		if _, err := ch.Send(ctx, "t", reminder); err == nil || !strings.Contains(err.Error(), "is it an ntfy server?") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("private server refused", func(t *testing.T) {
		ch, got, cfg := server(t, 200, "")
		cfg.Server = "http://ntfy.lan"
		_, err := ch.Send(ctx, "t", reminder)
		if err == nil || !strings.Contains(err.Error(), "private network") || len(*got) != 0 {
			t.Fatalf("error = %v, requests = %d", err, len(*got))
		}
	})
}

func TestTopic(t *testing.T) {
	for in, want := range map[string]string{
		"alex-reminders":             "alex-reminders",
		" Alex_42 ":                  "Alex_42",
		"https://ntfy.sh/my-topic":   "my-topic",
		"ntfy.example.com/my-topic/": "my-topic",
	} {
		if got, err := ntfy.Topic(in); err != nil || got != want {
			t.Errorf("Topic(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "has space", "ñ", strings.Repeat("a", 65), "a?b=c", "https://ntfy.sh/"} {
		if _, err := ntfy.Topic(bad); err == nil {
			t.Errorf("Topic(%q) accepted", bad)
		}
	}
}
