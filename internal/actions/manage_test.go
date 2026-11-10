package actions_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/fake"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/testutil"
)

// TestManageConversation runs Exhale from chat alone: add a payment method,
// add a subscription step by step, edit it, record a charge that came out
// more expensive, look at the check-up and delete with undo.
func TestManageConversation(t *testing.T) {
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
	eng := actions.New(svc, slog.New(slog.DiscardHandler), fake.New())
	chat := actions.ChatRef{Channel: "fake", ChatID: "chat-1"}

	var script strings.Builder
	var msg actions.Message
	show := func(rep actions.Reply) {
		if rep.Toast != "" {
			script.WriteString("(toast: " + rep.Toast + ")\n")
		}
		switch {
		case rep.Send != nil:
			msg = *rep.Send
			script.WriteString("NEW MESSAGE\n" + fake.Render(msg))
		case rep.Edit != nil:
			msg = *rep.Edit
			script.WriteString(fake.Render(msg))
		}
	}
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
		show(rep)
	}
	say := func(text string) {
		t.Helper()
		script.WriteString("\n> type " + text + "\n")
		if strings.HasPrefix(text, "/") {
			if rep, ok := eng.Command(ctx, owner, chat, text); ok {
				show(rep)
				return
			}
			r, ok := actions.CommandRoute(text)
			if !ok {
				t.Fatalf("unknown command %s", text)
			}
			m, err := eng.OpenApp(ctx, owner, r)
			if err != nil {
				t.Fatal(err)
			}
			show(actions.Reply{Send: &m})
			return
		}
		rep, ok, err := eng.HandleText(ctx, owner, chat, text)
		if err != nil || !ok {
			t.Fatalf("text %q not handled: ok=%v err=%v", text, ok, err)
		}
		show(rep)
	}

	script.WriteString("# A payment method\n")
	say("/methods")
	press("Add a payment method")
	say("Visa ••4421")
	press("Card")
	press("Expiry")
	say("12/26")

	script.WriteString("\n# A subscription, step by step\n")
	say("/add")
	say("Netflix")
	say("13,99")
	press("Every month")
	press("12")
	press("Visa")
	press("Streaming")
	press("Add it")

	script.WriteString("\n# Editing it\n")
	press("Edit")
	press("Notice period")
	say("30")
	press("Reminders")
	press("7 days before")
	press("Back")
	press("Cancel link")
	say("netflix.com/cancelplan")

	script.WriteString("\n# The bank took more than expected\n")
	list, _ := svc.ListSubscriptions(ctx, owner, app.ListSubscriptionsInput{})
	d, _ := svc.GetSubscription(ctx, owner, app.GetSubscriptionInput{ID: list.Subscriptions[0].ID})
	m, err := eng.Render(ctx, owner, app.KindConfirm, d.Upcoming[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	msg = m
	script.WriteString(fake.Render(msg))
	press("Paid, another amount")
	say("15.99")
	press("Yes, from now on")

	script.WriteString("\n# The check-up and settings\n")
	say("/checkup")
	say("/settings")
	press("Payday")
	say("28")

	script.WriteString("\n# Delete, and take it back\n")
	say("/subs")
	press("Netflix")
	press("Edit")
	press("Delete")
	press("Delete")
	press("Undo")

	testutil.Golden(t, "manage_conversation", termID.ReplaceAllString(script.String(), "${1}_X"))
}
