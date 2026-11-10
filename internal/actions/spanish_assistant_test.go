package actions_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/assistant"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/fake"
	"github.com/akamachinecode/exhale/internal/store"
)

// TestSpanishAssistant: in a Spanish household the cards Exhale writes, the
// buttons and the reasons a change failed are in Spanish; the model's own
// sentence is whatever language the model wrote.
func TestSpanishAssistant(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Casa", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	owner.Scope = app.ScopeWrite
	eng := actions.New(svc, slog.New(slog.DiscardHandler), fake.New())
	asst, err := assistant.New(svc, nil, assistant.Env{})
	if err != nil {
		t.Fatal(err)
	}
	eng.SetAssistant(asst)
	model := &assistant.Fake{}
	asst.SetProvider(model)
	chat := actions.ChatRef{Channel: "fake", ChatID: "c1"}

	model.Then(
		assistant.Call("create_subscription", map[string]any{"name": "Netflix", "price": "13.99", "currency": "EUR", "firstCharge": "2026-11-12"}),
		assistant.Say("Te preparé Netflix."),
		assistant.Call("create_subscription", map[string]any{"name": "Gym", "price": "10", "currency": "EUR", "firstCharge": "2026-11-31"}),
		assistant.Say("No pude."),
	)
	msgs, _ := eng.Ask(ctx, owner, chat, "añade netflix")
	var all strings.Builder
	for _, m := range msgs {
		all.WriteString(fake.Render(m))
	}
	got := all.String()
	for _, want := range []string{"¿Añadir Netflix?", "13,99", "€ al mes", "Primer cobro: 2026-11-12", "✅ Hazlo"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// The dry run fails: the model is told in English, the person reads the
	// reason in Spanish when it reaches them.
	msgs, _ = eng.Ask(ctx, owner, chat, "añade gym el 31 de noviembre")
	if len(msgs) != 1 || !strings.Contains(fake.Render(msgs[0]), "No pude.") {
		t.Fatalf("answer: %v", msgs)
	}
	if res := model.Requests[len(model.Requests)-1]; !strings.Contains(assistant.LastToolResult(res).Content, `"2026-11-31" is not a date like 2026-01-31`) {
		t.Errorf("tool result: %s", assistant.LastToolResult(res).Content)
	}
	_, err = svc.CreateSubscription(ctx, owner, app.CreateSubscriptionInput{Name: "Gym", Price: "10", Currency: "EUR", FirstCharge: "x"})
	if got := actions.ErrorText("es", err); got != `Primer cobro: "x" no es una fecha como 2026-01-31` {
		t.Errorf("error text: %q", got)
	}
}
