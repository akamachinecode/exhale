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
	"github.com/akamachinecode/exhale/internal/store/db"
)

// TestMemberLanguage: chat speaks the language a person chose, and the
// household's to everyone else.
func TestMemberLanguage(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "UTC", Locale: "en-GB"})
	if err != nil {
		t.Fatal(err)
	}
	eng := actions.New(svc, slog.New(slog.DiscardHandler), fake.New())
	if got := eng.Locale(ctx, owner); got != "en" {
		t.Fatalf("household language = %q", got)
	}
	if err := st.Q.SetMemberLocale(ctx, db.SetMemberLocaleParams{Locale: "es", ID: owner.MemberID}); err != nil {
		t.Fatal(err)
	}
	if got := eng.Locale(ctx, owner); got != "es" {
		t.Fatalf("own language = %q", got)
	}
	if got, en := actions.HelpText(eng.Locale(ctx, owner)), actions.HelpText("en"); got == en || strings.HasPrefix(got, "Commands:") {
		t.Fatalf("help in Spanish: %q", got)
	}
	if got := actions.Language("pt-BR"); got != "en" {
		t.Fatalf("an unregistered language falls back to English: %q", got)
	}
}
