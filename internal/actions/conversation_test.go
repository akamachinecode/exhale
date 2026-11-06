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
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/fake"
	"github.com/akamachinecode/exhale/internal/scheduler"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
	"github.com/akamachinecode/exhale/internal/testutil"
)

// TestReminderConversation is the flagship flow: a reminder arrives with
// buttons, the user moves the date for this charge only, and a later change
// made by an AI agent re-edits the same message with an undo button.
func TestReminderConversation(t *testing.T) {
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
	pm, _ := svc.SavePaymentMethod(ctx, owner, app.SavePaymentMethodInput{Name: "Revolut", Kind: "card", Last4: "0937"})
	visa, _ := svc.SavePaymentMethod(ctx, owner, app.SavePaymentMethodInput{Name: "Visa", Kind: "card", Last4: "4421"})
	if _, err := svc.CreateSubscription(ctx, owner, app.CreateSubscriptionInput{Name: "Spotify Family", Price: "17.99", Currency: "EUR", Interval: 1, Unit: "month", FirstCharge: "2026-11-08", PaymentMethodID: pm.ID}); err != nil {
		t.Fatal(err)
	}

	// The clock plans and delivers the "3 days before" reminder.
	if err := svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	clock := scheduler.New(svc, slog.New(slog.DiscardHandler), eng)
	clock.Tick(ctx)
	if len(ch.Log) != 1 {
		t.Fatalf("expected one reminder, got %d", len(ch.Log))
	}
	clock.Tick(ctx) // delivering twice must not duplicate it
	if len(ch.Log) != 1 {
		t.Fatalf("reminder was duplicated")
	}

	var script strings.Builder
	script.WriteString(ch.Log[0])
	msg := ch.Messages[chat.ChatID]["1"]
	press := func(label string) {
		t.Helper()
		d := fake.Button(msg, label)
		if d == "" {
			t.Fatalf("no %q button in:\n%s", label, fake.Render(msg))
		}
		if len(d) > 64 {
			t.Fatalf("button data %q exceeds Telegram's 64 bytes", d)
		}
		rep, err := eng.Handle(ctx, owner, chat, d)
		if err != nil {
			t.Fatal(err)
		}
		script.WriteString("\n> press " + label + "\n")
		if rep.Toast != "" {
			script.WriteString("(toast: " + rep.Toast + ")\n")
		}
		if rep.Edit != nil {
			msg = *rep.Edit
			script.WriteString(fake.Render(msg))
		}
	}
	press("Change date")
	press("10")
	press("Only this charge")

	// An AI agent changes the payment method; the same message is re-edited.
	done := make(chan struct{})
	syncCtx, cancel := context.WithCancel(ctx)
	go func() { eng.Sync(syncCtx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	agent := owner
	agent.Kind, agent.Label = app.ActorAgent, "Claude (MCP)"
	list, _ := svc.ListSubscriptions(ctx, agent, app.ListSubscriptionsInput{})
	if _, err := svc.UpdateSubscription(ctx, agent, app.UpdateSubscriptionInput{ID: list.Subscriptions[0].ID, PaymentMethodID: &visa.ID}); err != nil {
		t.Fatal(err)
	}
	detail, _ := svc.GetSubscription(ctx, agent, app.GetSubscriptionInput{ID: list.Subscriptions[0].ID})
	if _, err := svc.SetChargeStatus(ctx, agent, app.SetChargeStatusInput{ChargeID: detail.Upcoming[0].ID, Status: "paid"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for ch.Count() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if ch.Count() < 2 {
		t.Fatal("the message was not re-edited after the agent's change")
	}
	script.WriteString("\n> Claude marks it paid over MCP\n" + ch.Last())

	out := script.String()
	// ids are random; make the transcript stable
	for _, id := range []string{detail.Upcoming[0].ID} {
		out = strings.ReplaceAll(out, id, "chg_X")
	}
	out = stableAudit(out)
	testutil.Golden(t, "reminder_conversation", out)
}

var auditID = regexp.MustCompile(`aud_[0-9a-z]{16}`)

func stableAudit(s string) string { return auditID.ReplaceAllString(s, "aud_X") }
