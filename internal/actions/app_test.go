package actions_test

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/fake"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
	"github.com/akamachinecode/exhale/internal/testutil"
)

// TestAppConversation drives the app inside a message (ADR 0019): the home
// message navigates by editing itself, charge actions stay inside the app,
// and a change made elsewhere redraws the screen it is showing.
func TestAppConversation(t *testing.T) {
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
	ch := fake.New()
	eng := actions.New(svc, slog.New(slog.DiscardHandler), ch)
	chat := actions.ChatRef{Channel: "fake", ChatID: "chat-1"}
	if err := st.Q.LinkChannel(ctx, db.LinkChannelParams{ID: "lnk_1", MemberID: owner.MemberID, Channel: "fake", ExternalID: chat.ChatID, CreatedAt: store.FormatTime(now)}); err != nil {
		t.Fatal(err)
	}
	info, _ := svc.GetHousehold(ctx, owner, struct{}{})
	cat := map[string]string{}
	for _, c := range info.Categories {
		cat[c.Name] = c.ID
	}
	revolut, _ := svc.SavePaymentMethod(ctx, owner, app.SavePaymentMethodInput{Name: "Revolut", Kind: "card", Last4: "0937"})
	visa, _ := svc.SavePaymentMethod(ctx, owner, app.SavePaymentMethodInput{Name: "Visa", Kind: "card", Last4: "4421"})
	for _, in := range []app.CreateSubscriptionInput{
		{Name: "Spotify Family", Price: "17.99", Currency: "EUR", Interval: 1, Unit: "month", FirstCharge: "2026-11-08", PaymentMethodID: revolut.ID, CategoryID: cat["Music"]},
		{Name: "Netflix", Price: "13.99", Currency: "EUR", Interval: 1, Unit: "month", FirstCharge: "2026-11-06", PaymentMethodID: visa.ID, CategoryID: cat["Streaming"]},
		{Name: "iCloud+", Price: "35.88", Currency: "EUR", Interval: 1, Unit: "year", FirstCharge: "2026-11-25", PaymentMethodID: revolut.ID, CategoryID: cat["Cloud & Dev"]},
	} {
		if _, err := svc.CreateSubscription(ctx, owner, in); err != nil {
			t.Fatal(err)
		}
	}

	// Two days later Netflix's charge has passed without confirmation.
	now = now.AddDate(0, 0, 2)

	var script strings.Builder
	open := func(cmd string) string {
		t.Helper()
		r, ok := actions.CommandRoute(cmd)
		if !ok {
			t.Fatalf("%s is not an app command", cmd)
		}
		m, err := eng.OpenApp(ctx, owner, r)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := ch.Send(ctx, chat.ChatID, m)
		if _, err := eng.AppSent(ctx, owner, chat, id, r); err != nil {
			t.Fatal(err)
		}
		script.WriteString("> " + cmd + "\n")
		return id
	}
	// The first app message is replaced by the second: it is retired.
	open("/subs")
	msgID := open("/home")
	script.WriteString(strings.Join(ch.Log, "\n"))

	msg := func() actions.Message { return ch.Messages[chat.ChatID][msgID] }
	press := func(label string) {
		t.Helper()
		m := msg()
		d := fake.Button(m, label)
		if d == "" {
			t.Fatalf("no %q button in:\n%s", label, fake.Render(m))
		}
		rep, err := eng.Handle(ctx, owner, actions.ChatRef{Channel: chat.Channel, ChatID: chat.ChatID, MessageID: msgID}, d)
		if err != nil {
			t.Fatal(err)
		}
		script.WriteString("\n> press " + label + "\n")
		if rep.Toast != "" {
			script.WriteString("(toast: " + rep.Toast + ")\n")
		}
		if rep.Edit != nil {
			_ = ch.Edit(ctx, chat.ChatID, msgID, *rep.Edit)
			script.WriteString(fake.Render(*rep.Edit))
		}
		for _, row := range msg().Rows {
			for _, b := range row {
				if len(b.Data) > 64 {
					t.Fatalf("button %q data %q exceeds Telegram's 64 bytes", b.Label, b.Data)
				}
			}
		}
	}

	press("1 charge to confirm")
	press("Netflix")
	press("Paid")
	press("Home")
	press("Subscriptions")
	press("Spotify Family")
	press("Next charge")
	press("Change date")
	press("Home")
	press("Summary")
	press("Home")
	press("Subscriptions")
	press("iCloud+")
	press("Pause")
	press("Home")

	// Someone changes data elsewhere (the web, an agent): the live home
	// message is redrawn without anyone touching it.
	done := make(chan struct{})
	syncCtx, cancel := context.WithCancel(ctx)
	go func() { eng.Sync(syncCtx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	before := ch.Count()
	agent := owner
	agent.Kind, agent.Label = app.ActorAgent, "Claude (MCP)"
	if _, err := svc.CreateSubscription(ctx, agent, app.CreateSubscriptionInput{Name: "ChatGPT Plus", Price: "23", Currency: "EUR", Interval: 1, Unit: "month", FirstCharge: "2026-11-09", CategoryID: cat["AI"]}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for ch.Count() == before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if ch.Count() == before {
		t.Fatal("the live app message was not redrawn after a change elsewhere")
	}
	script.WriteString("\n> Claude adds ChatGPT Plus over MCP\n" + ch.Last())

	testutil.Golden(t, "app_conversation", stableIDs(script.String()))
}

var anyID = regexp.MustCompile(`\b(sub|chg|aud)_[0-9a-z]{16}\b`)

// stableIDs numbers random ids by first appearance (sub_1, chg_1, …).
func stableIDs(s string) string {
	seen := map[string]string{}
	count := map[string]int{}
	return anyID.ReplaceAllStringFunc(s, func(id string) string {
		if v, ok := seen[id]; ok {
			return v
		}
		prefix := id[:strings.IndexByte(id, '_')]
		count[prefix]++
		seen[id] = fmt.Sprintf("%s_%d", prefix, count[prefix])
		return seen[id]
	})
}
