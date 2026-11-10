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

// TestBillingDayConversation: the billing day and the weekend shift are
// shown next to the price and changed from the edit screen, in English and
// in Spanish.
func TestBillingDayConversation(t *testing.T) {
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
	// The 15th: 15 Nov 2026 is a Sunday, the third of the month.
	if _, err := svc.CreateSubscription(ctx, owner, app.CreateSubscriptionInput{Name: "Gym", Price: "30", Currency: "EUR", FirstCharge: "2026-11-15"}); err != nil {
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
		if rep.Edit != nil {
			msg = *rep.Edit
			script.WriteString(fake.Render(msg))
		}
	}
	open := func(cmd string) {
		t.Helper()
		r, _ := actions.CommandRoute(cmd)
		m, err := eng.OpenApp(ctx, owner, r)
		if err != nil {
			t.Fatal(err)
		}
		msg = m
		script.WriteString("\n> type " + cmd + "\n" + fake.Render(msg))
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

	open("/subs")
	press("Gym")
	press("Edit")
	press("Billing day")
	press("→ Monday")
	press("the third Sunday")
	press("Back")
	press("‹ Gym")

	script.WriteString("\n# In Spanish\n")
	open("/settings")
	press("Español")
	open("/subs")
	press("Gym")
	press("Editar")
	press("Día de cobro")

	testutil.Golden(t, "billing_conversation", termID.ReplaceAllString(script.String(), "${1}_X"))
}
