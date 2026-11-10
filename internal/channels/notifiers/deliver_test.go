package notifiers_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/fake"
	"github.com/akamachinecode/exhale/internal/channels/notifiers"
	"github.com/akamachinecode/exhale/internal/scheduler"
)

// TestDeliverToEveryChannel is the whole path: the scheduler delivers a
// reminder to a member with a chat, an email address, an ntfy topic and a
// webhook. The webhook is down: the others still get the reminder, the
// failure surfaces so the outbox retries, and the retry reaches only the
// webhook: nobody gets the same reminder twice.
func TestDeliverToEveryChannel(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	m := w.manager(t)
	if err := m.SetSMTP(ctx, w.smtpInput()); err != nil {
		t.Fatal(err)
	}
	if err := m.SetNtfy(ctx, notifiers.NtfyInput{Server: "http://ntfy.test"}); err != nil {
		t.Fatal(err)
	}

	madrid, _ := time.LoadLocation("Europe/Madrid")
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, madrid)
	svc := app.New(w.st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	pm, _ := svc.SavePaymentMethod(ctx, owner, app.SavePaymentMethodInput{Name: "Revolut", Kind: "card", Last4: "0937"})
	sub, err := svc.CreateSubscription(ctx, owner, app.CreateSubscriptionInput{Name: "Spotify Family", Price: "17.99", Currency: "EUR", Interval: 1, Unit: "month", FirstCharge: "2026-11-08", PaymentMethodID: pm.ID})
	if err != nil {
		t.Fatal(err)
	}
	subID := sub.Subscription.ID

	chat := fake.New()
	for channel, dest := range map[string]string{
		"fake":    "chat-1",
		"email":   "alex@example.com",
		"ntfy":    "alex-reminders",
		"webhook": "http://hooks.test/in/secret-token",
	} {
		if _, err := auth.AddDestination(ctx, w.st, owner.MemberID, channel, dest); err != nil {
			t.Fatal(err)
		}
	}
	eng := actions.New(svc, slog.New(slog.DiscardHandler), append([]actions.Channel{chat}, m.Channels()...)...)
	clock := scheduler.New(svc, slog.New(slog.DiscardHandler), eng)
	if err := svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	outbox := func() (status string, attempts int64, lastError string) {
		t.Helper()
		rows, err := w.st.Q.ListOutbox(ctx, 10)
		if err != nil || len(rows) == 0 {
			t.Fatalf("outbox: %v %v", rows, err)
		}
		// Rows come latest first; the reminder under test is the earliest.
		for i := len(rows) - 1; i >= 0; i-- {
			if r := rows[i]; r.Kind == app.KindReminder {
				return r.Status, r.Attempts, r.LastError
			}
		}
		t.Fatal("no reminder in the outbox")
		return
	}
	counts := func() [4]int {
		return [4]int{chat.Count(), len(w.smtp.Messages()), len(w.ntfy.got()), len(w.hook.got())}
	}

	// First attempt: the webhook answers 500.
	w.hook.fail(500)
	clock.Tick(ctx)
	if got := counts(); got != [4]int{1, 1, 1, 1} {
		t.Fatalf("after the first attempt [chat email ntfy webhook] = %v", got)
	}
	status, attempts, lastErr := outbox()
	if status != "pending" || attempts != 1 {
		t.Fatalf("outbox = %s after %d attempts; the failure must leave it pending for a retry", status, attempts)
	}
	if !strings.Contains(lastErr, "webhook: hooks.test answered 500") {
		t.Fatalf("last error = %q", lastErr)
	}
	if strings.Contains(lastErr, "secret-token") {
		t.Fatalf("the webhook URL is in the outbox: %q", lastErr)
	}

	// What each channel got.
	mail := w.smtp.Messages()[0]
	if mail.To[0] != "alex@example.com" || !strings.Contains(mail.Data, "/subscriptions/"+subID) {
		t.Fatalf("email = %+v", mail)
	}
	push := w.ntfy.got()[0]
	for _, want := range []string{`"topic":"alex-reminders"`, `"title":"Spotify Family renews in 3 days"`, `"tags":["bell"]`, `"click":"https://exhale.example.com/subscriptions/` + subID + `"`, `"action":"view"`} {
		if !strings.Contains(push, want) {
			t.Errorf("ntfy body misses %s:\n%s", want, push)
		}
	}
	for _, want := range []string{`"event":"reminder"`, `"subscription":"Spotify Family"`, `"amount":"17.99"`, `"currency":"EUR"`, `"dueDate":"2026-11-08"`, `"daysUntil":3`, `"paymentMethod":"Revolut ••0937"`, `"link":"https://exhale.example.com/subscriptions/` + subID + `"`} {
		if !strings.Contains(w.hook.got()[0], want) {
			t.Errorf("webhook body misses %s:\n%s", want, w.hook.got()[0])
		}
	}

	// Not due yet: the retry waits for its backoff.
	clock.Tick(ctx)
	if got := counts(); got != [4]int{1, 1, 1, 1} {
		t.Fatalf("retried before the backoff: %v", got)
	}

	// The retry, still failing: only the webhook is tried again.
	now = now.Add(2 * time.Minute)
	clock.Tick(ctx)
	if got := counts(); got != [4]int{1, 1, 1, 2} {
		t.Fatalf("after the second attempt = %v; channels that got it must not get it again", got)
	}

	// The webhook is back.
	w.hook.fail(0)
	now = now.Add(5 * time.Minute)
	clock.Tick(ctx)
	if got := counts(); got != [4]int{1, 1, 1, 3} {
		t.Fatalf("after the third attempt = %v", got)
	}
	if status, attempts, _ := outbox(); status != "sent" || attempts != 3 {
		t.Fatalf("outbox = %s after %d attempts", status, attempts)
	}
	now = now.Add(time.Hour)
	clock.Tick(ctx)
	if got := counts(); got != [4]int{1, 1, 1, 3} {
		t.Fatalf("delivered again after success: %v", got)
	}

	// Later changes re-edit the chat message only: the notify-only channels
	// are never asked to edit, and nothing new is sent through them.
	done := make(chan struct{})
	syncCtx, cancel := context.WithCancel(ctx)
	go func() { _ = eng.Sync(syncCtx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	agent := owner
	agent.Kind, agent.Label = app.ActorAgent, "Claude (MCP)"
	detail, _ := svc.GetSubscription(ctx, agent, app.GetSubscriptionInput{ID: subID})
	if _, err := svc.SetChargeStatus(ctx, agent, app.SetChargeStatusInput{ChargeID: detail.Upcoming[0].ID, Status: "paid"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for chat.Count() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if got := counts(); got != [4]int{2, 1, 1, 3} {
		t.Fatalf("after a change [chat email ntfy webhook] = %v; want the chat message re-edited and nothing else", got)
	}
}
