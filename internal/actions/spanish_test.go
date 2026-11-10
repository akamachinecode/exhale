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
	"github.com/akamachinecode/exhale/internal/scheduler"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
	"github.com/akamachinecode/exhale/internal/testutil"
)

// TestSpanishConversation checks that a household whose language is Spanish
// gets its reminders, screens, dates and amounts in Spanish.
func TestSpanishConversation(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	madrid, _ := time.LoadLocation("Europe/Madrid")
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, madrid)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Casa", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	ch := fake.New()
	eng := actions.New(svc, slog.New(slog.DiscardHandler), ch)
	chat := actions.ChatRef{Channel: "fake", ChatID: "chat-1"}
	if err := st.Q.LinkChannel(ctx, db.LinkChannelParams{ID: "lnk_1", MemberID: owner.MemberID, Channel: "fake", ExternalID: chat.ChatID, CreatedAt: store.FormatTime(now)}); err != nil {
		t.Fatal(err)
	}
	visa, _ := svc.SavePaymentMethod(ctx, owner, app.SavePaymentMethodInput{Name: "Visa", Kind: "card", Last4: "4421"})
	for _, in := range []app.CreateSubscriptionInput{
		{Name: "Netflix", Price: "13.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-08", PaymentMethodID: visa.ID},
		{Name: "Gimnasio", Price: "39.00", Currency: "EUR", Interval: 3, Unit: "month", FirstCharge: "2026-12-12", TermsInput: app.TermsInput{NoticeDays: 30}},
		{Name: "Notion AI", Price: "10.00", Currency: "USD", Unit: "month", TermsInput: app.TermsInput{TrialEndsOn: "2026-11-08"}},
	} {
		if _, err := svc.CreateSubscription(ctx, owner, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	scheduler.New(svc, slog.New(slog.DiscardHandler), eng).Tick(ctx)

	var script strings.Builder
	script.WriteString("# Recordatorios\n" + strings.Join(ch.Log, "\n"))
	var msg actions.Message
	show := func(title string, m actions.Message) {
		msg = m
		script.WriteString("\n# " + title + "\n" + fake.Render(m))
	}
	press := func(label string) {
		t.Helper()
		d := fake.Button(msg, label)
		if d == "" {
			t.Fatalf("no %q button in:\n%s", label, fake.Render(msg))
		}
		rep, err := eng.Handle(ctx, owner, chat, d)
		if err != nil {
			t.Fatal(err)
		}
		script.WriteString("\n> pulsa " + label + "\n")
		if rep.Toast != "" {
			script.WriteString("(aviso: " + rep.Toast + ")\n")
		}
		if rep.Edit != nil {
			msg = *rep.Edit
			script.WriteString(fake.Render(msg))
		}
	}
	home, err := eng.OpenApp(ctx, owner, actions.RouteHome)
	if err != nil {
		t.Fatal(err)
	}
	show("Inicio", home)
	press("Suscripciones")
	press("Gimnasio")
	press("Cancelar")
	press("Al final del periodo")
	show("Ayuda", actions.Message{Title: actions.HelpText("es")})

	out := termID.ReplaceAllString(script.String(), "${1}_X")
	if strings.Contains(out, "renews") || strings.Contains(out, "Cancel by") || strings.Contains(out, "monthly") {
		t.Errorf("English leaked into the Spanish conversation")
	}
	testutil.Golden(t, "spanish_conversation", out)
}
