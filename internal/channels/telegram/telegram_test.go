package telegram

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/store"
)

// fakeBotAPI is a minimal Telegram Bot API server (multipart requests).
type fakeBotAPI struct {
	mu      sync.Mutex
	updates []map[string]any
	nextID  int
	calls   []call
}

type call struct {
	Method string
	Fields map[string]string
}

func (f *fakeBotAPI) push(u map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	u["update_id"] = f.nextID
	f.updates = append(f.updates, u)
}

func (f *fakeBotAPI) find(method, contains string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.Method == method && strings.Contains(c.Fields["text"]+c.Fields["reply_markup"], contains) {
			return true
		}
	}
	return false
}

func (f *fakeBotAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	_ = r.ParseMultipartForm(1 << 20)
	fields := map[string]string{}
	if r.MultipartForm != nil {
		for k, v := range r.MultipartForm.Value {
			fields[k] = v[0]
		}
	}
	w.Header().Set("Content-Type", "application/json")
	ok := func(result any) { json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result}) }
	switch method {
	case "getUpdates":
		deadline := time.Now().Add(200 * time.Millisecond)
		for time.Now().Before(deadline) {
			f.mu.Lock()
			if len(f.updates) > 0 {
				u := f.updates
				f.updates = nil
				f.mu.Unlock()
				ok(u)
				return
			}
			f.mu.Unlock()
			time.Sleep(10 * time.Millisecond)
		}
		ok([]any{})
	case "sendMessage", "editMessageText":
		f.mu.Lock()
		f.calls = append(f.calls, call{method, fields})
		f.nextID++
		id := f.nextID
		f.mu.Unlock()
		ok(map[string]any{"message_id": id, "date": 0, "chat": map[string]any{"id": 42, "type": "private"}, "text": fields["text"]})
	default:
		f.mu.Lock()
		f.calls = append(f.calls, call{method, fields})
		f.mu.Unlock()
		ok(true)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTelegramLinkReminderAndButton(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api := &fakeBotAPI{}
	srv := httptest.NewServer(api)
	defer srv.Close()

	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, _ := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "UTC", Locale: "en"})
	log := slog.New(slog.DiscardHandler)
	tg, err := New("123:abc", srv.URL, svc, log)
	if err != nil {
		t.Fatal(err)
	}
	eng := actions.New(svc, log, tg)
	tg.SetEngine(eng)
	go tg.Serve(ctx)

	// Linking: the user opens t.me/<bot>?start=<code>.
	code, _ := auth.CreateLinkCode(ctx, st, owner.MemberID, Name)
	api.push(map[string]any{"message": map[string]any{"message_id": 1, "date": 0, "text": "/start " + code, "chat": map[string]any{"id": 42, "type": "private"}, "from": map[string]any{"id": 42, "first_name": "Alex"}}})
	eventually(t, "link confirmation", func() bool { return api.find("sendMessage", "Linked") })

	d, err := svc.CreateSubscription(ctx, owner, app.CreateSubscriptionInput{Name: "Netflix", Price: "19.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-08"})
	if err != nil {
		t.Fatal(err)
	}
	chargeID := d.Upcoming[0].ID
	if err := eng.Deliver(ctx, app.OutboxItem{Kind: app.KindReminder, ChargeID: chargeID, MemberID: owner.MemberID}); err != nil {
		t.Fatal(err)
	}
	if !api.find("sendMessage", "Netflix renews in 3 days") || !api.find("sendMessage", "paid|"+chargeID) {
		t.Fatal("reminder with buttons was not sent")
	}

	// The user taps "Already paid".
	api.push(map[string]any{"callback_query": map[string]any{"id": "cb1", "data": "paid|" + chargeID, "chat_instance": "x", "from": map[string]any{"id": 42, "first_name": "Alex"},
		"message": map[string]any{"message_id": 7, "date": 1762333200, "chat": map[string]any{"id": 42, "type": "private"}, "text": "reminder"}}})
	eventually(t, "edited message", func() bool { return api.find("editMessageText", "Netflix paid") })
	got, _ := svc.GetSubscription(ctx, owner, app.GetSubscriptionInput{ID: d.Subscription.ID})
	if len(got.History) == 0 || got.History[0].Status != "paid" {
		t.Fatalf("charge not marked paid: %+v", got.History)
	}
}
