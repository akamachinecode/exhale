package actions_test

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/assistant"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/fake"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/testutil"
)

// TestAssistantConversation is plain language in a chat: a question gets an
// answer; a request gets a message that says what would change, with
// buttons; pressing one applies it and offers Undo. Buttons carry a short
// id, never the change itself.
func TestAssistantConversation(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	madrid, _ := time.LoadLocation("Europe/Madrid")
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, madrid)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	owner.Scope = app.ScopeWrite
	spotify, err := svc.CreateSubscription(ctx, owner, app.CreateSubscriptionInput{Name: "Spotify Family", Price: "17.99", Currency: "EUR", FirstCharge: "2026-11-08"})
	if err != nil {
		t.Fatal(err)
	}
	eng := actions.New(svc, slog.New(slog.DiscardHandler), fake.New())
	chat := actions.ChatRef{Channel: "fake", ChatID: "chat-1"}

	// Without an assistant, free text is not answered: the channel shows
	// its help, as before.
	if msgs, ok := eng.Ask(ctx, owner, chat, "what do I pay?"); ok || msgs != nil || eng.CanAsk(ctx) {
		t.Fatal("free text was answered without an assistant")
	}
	asst, err := assistant.New(svc, nil, assistant.Env{})
	if err != nil {
		t.Fatal(err)
	}
	eng.SetAssistant(asst)
	if _, ok := eng.Ask(ctx, owner, chat, "what do I pay?"); ok {
		t.Fatal("an assistant nobody configured must stay silent")
	}
	model := &assistant.Fake{}
	asst.SetProvider(model)

	var script strings.Builder
	var last []actions.Message
	type_ := func(text string) {
		t.Helper()
		msgs, ok := eng.Ask(ctx, owner, chat, text)
		if !ok {
			t.Fatalf("%q was not answered", text)
		}
		script.WriteString("\n> " + text + "\n")
		for _, m := range msgs {
			script.WriteString(fake.Render(m))
		}
		last = msgs
	}
	press := func(label string) {
		t.Helper()
		d := ""
		for _, m := range last {
			if b := fake.Button(m, label); b != "" {
				d = b
			}
		}
		if d == "" {
			t.Fatalf("no %q button", label)
		}
		if len(d) > 64 {
			t.Fatalf("button data %q exceeds Telegram's 64 bytes", d)
		}
		rep, err := eng.Handle(ctx, owner, actions.ChatRef{Channel: "fake", ChatID: "chat-1", MessageID: "9"}, d)
		if err != nil {
			t.Fatal(err)
		}
		script.WriteString("\n> press " + label + "\n")
		if rep.Toast != "" {
			script.WriteString("(toast: " + rep.Toast + ")\n")
		}
		if rep.Edit != nil {
			script.WriteString(fake.Render(*rep.Edit))
			last = []actions.Message{*rep.Edit}
		}
	}
	status := func() string {
		d, err := svc.GetSubscription(ctx, owner, app.GetSubscriptionInput{ID: spotify.Subscription.ID})
		if err != nil {
			return "deleted"
		}
		return string(d.Subscription.Status)
	}
	names := func() string {
		l, _ := svc.ListSubscriptions(ctx, owner, app.ListSubscriptionsInput{})
		var n []string
		for _, s := range l.Subscriptions {
			n = append(n, s.Name)
		}
		return strings.Join(n, ",")
	}

	model.Then(assistant.Call("spending_summary", map[string]any{}), assistant.Say("You pay 17.99 EUR a month: Spotify Family.\nNext charge on Sunday."))
	type_("what do I pay per month?")

	model.Then(
		assistant.Call("create_subscription", map[string]any{"name": "Netflix", "price": "13.99", "currency": "EUR", "firstCharge": "2026-11-06"}),
		assistant.Say("I prepared Netflix at 13.99 EUR a month, first charge tomorrow."),
	)
	type_("add Netflix 13.99 on the 6th")
	if got := names(); got != "Spotify Family" {
		t.Fatalf("created before the button was pressed: %s", got)
	}
	press("Do it")
	if got := names(); got != "Netflix,Spotify Family" && got != "Spotify Family,Netflix" {
		t.Fatalf("after Do it: %s", got)
	}
	press("Undo")
	if got := names(); got != "Spotify Family" {
		t.Fatalf("after Undo: %s", got)
	}

	model.Then(
		assistant.Call("set_subscription_status", map[string]any{"id": spotify.Subscription.ID, "action": "cancel_at_period_end"}),
		assistant.Say("I prepared the cancellation."),
	)
	type_("cancel spotify")
	if status() != "active" {
		t.Fatal("cancelled before the button was pressed")
	}
	press("Don't")
	if status() != "active" {
		t.Fatal("cancelled although the person said no")
	}

	model.Then(assistant.Fail(&assistant.Error{Kind: assistant.KindAuth, Detail: "invalid x-api-key"}))
	type_("hello?")

	// A button whose proposal is gone only says so.
	rep, err := eng.Handle(ctx, owner, chat, "ai|ok|pgone")
	if err != nil || rep.Edit != nil || !strings.Contains(rep.Toast, "expired") {
		t.Fatalf("expired button: %+v %v", rep, err)
	}
	// Proposal ids are random; make the transcript stable.
	testutil.Golden(t, "assistant_conversation", regexp.MustCompile(`\|p[a-z2-7]{13}\)`).ReplaceAllString(script.String(), "|pX)"))
}
