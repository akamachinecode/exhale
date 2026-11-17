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
	"github.com/akamachinecode/exhale/internal/scheduler"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
	"github.com/akamachinecode/exhale/internal/testutil"
)

// TestPaidOnAnotherDayConversation follows an App Store receipt: Exhale
// expects ChatGPT on 20 November, the receipt is dated 15 November and says
// it renews on 9 December. Moving the charge to 9 December for good would
// leave November without a charge, so the scope screen warns and offers to
// record the payment; after that, one tap moves the next charge.
func TestPaidOnAnotherDayConversation(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	madrid, _ := time.LoadLocation("Europe/Madrid")
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, madrid)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "USD", Timezone: "Europe/Madrid", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	ch := fake.New()
	eng := actions.New(svc, slog.New(slog.DiscardHandler), ch)
	chat := actions.ChatRef{Channel: "fake", ChatID: "chat-1"}
	if err := st.Q.LinkChannel(ctx, db.LinkChannelParams{ID: "lnk_1", MemberID: owner.MemberID, Channel: "fake", ExternalID: chat.ChatID, CreatedAt: store.FormatTime(now)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSubscription(ctx, owner, app.CreateSubscriptionInput{Name: "ChatGPT", Price: "19.99", Currency: "USD", Interval: 1, Unit: "month", FirstCharge: "2026-11-20"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	scheduler.New(svc, slog.New(slog.DiscardHandler), eng).Tick(ctx)
	if len(ch.Log) != 1 {
		t.Fatalf("expected the 3-days-before reminder, got %d messages", len(ch.Log))
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
	press("›")
	press("9")
	if !strings.Contains(fake.Render(msg), "nothing is charged in between") {
		t.Fatalf("moving November to the next renewal must warn:\n%s", fake.Render(msg))
	}
	press("Paid another day")
	press("16")
	// The receipt says the 15th: correct the paid date without reopening.
	press("Change paid date")
	press("15")
	if !strings.Contains(fake.Render(msg), "Renewal: Fri, 20 Nov 2026") || !strings.Contains(fake.Render(msg), "Paid on: Sun, 15 Nov 2026") {
		t.Fatalf("both dates must show:\n%s", fake.Render(msg))
	}
	press("Change the next charge")
	press("9")
	if strings.Contains(fake.Render(msg), "nothing is charged in between") {
		t.Fatalf("with November paid, moving December must not warn:\n%s", fake.Render(msg))
	}
	press("This and following")

	d, _ := svc.ListSubscriptions(ctx, owner, app.ListSubscriptionsInput{})
	if got := d.Subscriptions[0].NextCharge.String(); got != "2026-12-09" {
		t.Fatalf("next charge = %s, want 2026-12-09", got)
	}
	testutil.Golden(t, "paid_on_conversation", stableAudit(stableCharges(script.String())))
}

var chargeID = regexp.MustCompile(`chg_[0-9a-z]{16}`)

// stableCharges numbers charge ids in order of appearance.
func stableCharges(s string) string {
	seen := map[string]string{}
	return chargeID.ReplaceAllStringFunc(s, func(id string) string {
		if _, ok := seen[id]; !ok {
			seen[id] = fmt.Sprintf("chg_%d", len(seen)+1)
		}
		return seen[id]
	})
}
