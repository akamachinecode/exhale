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
	"github.com/akamachinecode/exhale/internal/assistant"
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

func (f *fakeBotAPI) called(method string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.Method == method {
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
	case "getMe":
		ok(map[string]any{"id": 1, "is_bot": true, "first_name": "Exhale Test", "username": "exhale_test_bot"})
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
	// Linking opens the app: a home message with navigation, pinned.
	eventually(t, "home app message", func() bool { return api.find("sendMessage", "a|subs|0") })
	eventually(t, "pinned home", func() bool { return api.called("pinChatMessage") })
	if !api.called("setMyCommands") {
		t.Fatal("the command menu was not set")
	}

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

// TestManagerConfiguredFromTheWeb: no token at start; the owner pastes one in
// the web, the bot starts without a restart, and removing it stops it.
func TestManagerConfiguredFromTheWeb(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(&fakeBotAPI{})
	defer srv.Close()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := app.New(st, bus.New(), nil, time.Now)
	log := slog.New(slog.DiscardHandler)
	m := NewManager("", srv.URL, svc, log)
	m.SetEngine(actions.New(svc, log, m))
	go m.Serve(ctx)

	if s := m.Status(ctx); s.Configured || s.Running {
		t.Fatalf("no token yet, got %+v", s)
	}
	if _, err := m.Send(ctx, "42", actions.Message{Title: "x"}); err == nil {
		t.Fatal("sending without a bot should fail")
	}
	if _, err := m.Configure(ctx, "not-a-token"); err == nil {
		t.Fatal("a malformed token was accepted")
	}
	s, err := m.Configure(ctx, "123456789:AAH-test-token-abcdefghijkl")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Running || s.Username != "exhale_test_bot" || s.Source != "settings" || s.Name != "Exhale Test" {
		t.Fatalf("bot should run after configure: %+v", s)
	}
	if _, err := m.Send(ctx, "42", actions.Message{Title: "hello"}); err != nil {
		t.Fatalf("send through the configured bot: %v", err)
	}
	s, _ = m.Remove(ctx)
	eventually(t, "bot stopped", func() bool { return !m.Status(ctx).Running })
	if s.Configured {
		t.Fatalf("token should be gone: %+v", s)
	}

	env := NewManager("999:env-token-abcdefghijklmnop", srv.URL, svc, log)
	if _, err := env.Configure(ctx, "123456789:AAH-test-token-abcdefghijkl"); err == nil {
		t.Fatal("a token from the environment must not be overridden from the web")
	}
}

// TestJoinFromTelegram: an owner shares a t.me invite; the other person
// opens it and becomes a member with their chat linked, no web needed.
func TestJoinFromTelegram(t *testing.T) {
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
	svc := app.New(st, bus.New(), nil, time.Now)
	owner, _ := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Casa", OwnerName: "Alex", Currency: "EUR", Timezone: "UTC", Locale: "en"})
	log := slog.New(slog.DiscardHandler)
	tg, err := New("123:abc", srv.URL, svc, log)
	if err != nil {
		t.Fatal(err)
	}
	tg.SetEngine(actions.New(svc, log, tg))
	go tg.Serve(ctx)

	code, _, err := auth.CreateInvite(ctx, st, owner, "member")
	if err != nil {
		t.Fatal(err)
	}
	start := func(id int, text string) {
		api.push(map[string]any{"message": map[string]any{"message_id": id, "date": 0, "text": text,
			"chat": map[string]any{"id": 77, "type": "private"}, "from": map[string]any{"id": 77, "first_name": "Marta", "last_name": "G"}}})
	}
	start(1, "/start join-"+code)
	eventually(t, "welcome", func() bool { return api.find("sendMessage", "Welcome to Casa, Marta G") })
	eventually(t, "home app", func() bool { return api.find("sendMessage", "a|subs|0") })
	info, _ := svc.GetHousehold(ctx, owner, struct{}{})
	if len(info.Members) != 2 || info.Members[1].Name != "Marta G" || info.Members[1].Role != "member" {
		t.Fatalf("member not created: %+v", info.Members)
	}
	start(2, "/start join-"+code)
	eventually(t, "already linked", func() bool { return api.find("sendMessage", "already linked") })
}

// TestFreeText: typed words that are not a command get the command list,
// as always, until an owner turns the assistant on; then they are answered,
// and a change arrives as a message with buttons.
func TestFreeText(t *testing.T) {
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
	asst, err := assistant.New(svc, log, assistant.Env{})
	if err != nil {
		t.Fatal(err)
	}
	eng.SetAssistant(asst)
	tg.SetEngine(eng)
	go tg.Serve(ctx)
	code, _ := auth.CreateLinkCode(ctx, st, owner.MemberID, Name)
	say := func(text string) {
		api.push(map[string]any{"message": map[string]any{"message_id": 1, "date": 0, "text": text, "chat": map[string]any{"id": 42, "type": "private"}, "from": map[string]any{"id": 42, "first_name": "Alex"}}})
	}
	say("/start " + code)
	eventually(t, "link confirmation", func() bool { return api.find("sendMessage", "Linked") })

	// Not configured: today's help, and the model is never called.
	say("what do I pay per month?")
	eventually(t, "the command list", func() bool { return api.find("sendMessage", "Commands:") })

	model := &assistant.Fake{}
	asst.SetProvider(model)
	model.Then(
		assistant.Call("create_subscription", map[string]any{"name": "Netflix", "price": "13.99", "currency": "EUR", "firstCharge": "2026-11-06"}),
		assistant.Say("I prepared Netflix <13.99 EUR>."),
	)
	liveEvery = 20 * time.Millisecond
	say("add Netflix 13.99 on the 6th")
	// The answer appears as it is written, in one message edited into the
	// finished answer.
	eventually(t, "the answer being written", func() bool { return api.find("sendMessage", "I …") })
	eventually(t, "the answer", func() bool { return api.find("editMessageText", "I prepared Netflix &lt;13.99 EUR&gt;.") })
	eventually(t, "the proposal with its buttons", func() bool {
		return api.find("sendMessage", "<b>Add Netflix?</b>") && api.find("sendMessage", "ai|ok|p")
	})
	if !api.called("sendChatAction") {
		t.Error("typing… was not shown while the model worked")
	}
	if l, _ := svc.ListSubscriptions(ctx, owner, app.ListSubscriptionsInput{}); len(l.Subscriptions) != 0 {
		t.Fatal("the subscription exists before the button was pressed")
	}
	// A command is never sent to the model.
	before := model.Calls()
	say("/nonsense")
	eventually(t, "the command list for an unknown command", func() bool {
		api.mu.Lock()
		defer api.mu.Unlock()
		n := 0
		for _, c := range api.calls {
			if c.Method == "sendMessage" && strings.Contains(c.Fields["text"], "Commands:") {
				n++
			}
		}
		return n == 2
	})
	if model.Calls() != before {
		t.Fatal("a command reached the model")
	}
}
