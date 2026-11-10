package actions_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/fake"
	"github.com/akamachinecode/exhale/internal/store"
)

// edits records, per message id, the latest edit a channel was asked for.
type edits struct {
	*fake.Channel
	mu   sync.Mutex
	last map[string]string
}

func (e *edits) Edit(ctx context.Context, chatID, messageID string, m actions.Message) error {
	e.mu.Lock()
	e.last[messageID] = fake.Render(m)
	e.mu.Unlock()
	return e.Channel.Edit(ctx, chatID, messageID, m)
}

func (e *edits) of(messageID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.last[messageID]
}

// TestAppSurface: a surface such as Slack's Home tab is an app message that
// lives beside the chat's own app message. Neither retires the other, charge
// buttons pressed in it keep the way home, and it follows data changes.
func TestAppSurface(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "UTC", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	ch := &edits{Channel: fake.New(), last: map[string]string{}}
	eng := actions.New(svc, slog.New(slog.DiscardHandler), ch)
	chat := actions.ChatRef{Channel: "fake", ChatID: "U1"}
	const surface = actions.SurfacePrefix + "home"

	// The adapter draws the surface, then the app message, then another one.
	home, err := eng.OpenApp(ctx, owner, actions.RouteHome)
	if err != nil {
		t.Fatal(err)
	}
	ch.Messages["U1"] = map[string]actions.Message{surface: home}
	if err := eng.AppSurface(ctx, owner, chat, surface, actions.RouteHome); err != nil {
		t.Fatal(err)
	}
	first, _ := ch.Send(ctx, "U1", home)
	if old, err := eng.AppSent(ctx, owner, chat, first, actions.RouteHome); err != nil || len(old) != 0 {
		t.Fatalf("the first app message retires nothing, not even the surface: %v %v", old, err)
	}
	second, _ := ch.Send(ctx, "U1", home)
	if old, err := eng.AppSent(ctx, owner, chat, second, actions.RouteHome); err != nil || len(old) != 1 || old[0] != first {
		t.Fatalf("the second app message retires the first one only: %v %v", old, err)
	}
	if !strings.Contains(ch.of(first), "moved") || ch.of(surface) != "" {
		t.Fatalf("only the first app message should be retired; the surface got %q", ch.of(surface))
	}
	// Drawing the surface again retires nothing either.
	if err := eng.AppSurface(ctx, owner, chat, surface, actions.RouteHome); err != nil {
		t.Fatal(err)
	}
	if ch.of(second) != "" {
		t.Fatalf("the surface retired the app message: %q", ch.of(second))
	}

	// A charge button pressed in the surface stays inside the app.
	d, err := svc.CreateSubscription(ctx, owner, app.CreateSubscriptionInput{Name: "Netflix", Price: "13.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-08"})
	if err != nil {
		t.Fatal(err)
	}
	in := chat
	in.MessageID = surface
	rep, err := eng.Handle(ctx, owner, in, "paid|"+d.Upcoming[0].ID)
	if err != nil || rep.Edit == nil || fake.Button(*rep.Edit, "Home") != actions.RouteHome {
		t.Fatalf("paid inside the surface should keep the way home: %+v %v", rep, err)
	}

	// And the surface is redrawn, through Channel.Edit, when data changes.
	if _, err := eng.Handle(ctx, owner, in, actions.RouteHome); err != nil {
		t.Fatal(err)
	}
	go eng.Sync(ctx)
	time.Sleep(20 * time.Millisecond) // let Sync subscribe
	if _, err := svc.CreateSubscription(ctx, owner, app.CreateSubscriptionInput{Name: "Spotify", Price: "9.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-09"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(ch.of(surface), "Spotify") || !strings.Contains(ch.of(second), "Spotify") {
		if time.Now().After(deadline) {
			t.Fatalf("the surface and the app message should both be redrawn; the surface shows:\n%s", ch.of(surface))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
