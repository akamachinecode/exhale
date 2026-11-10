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

var termID = regexp.MustCompile(`(chg|sub|aud|pm|cat)_[0-9a-z]{16}`)

// TestTermsConversation covers the dates that are not a plain renewal: a
// free trial about to charge, the last day to cancel a contract with a
// notice period, and something that expires unless it is renewed.
func TestTermsConversation(t *testing.T) {
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
	for _, in := range []app.CreateSubscriptionInput{
		{Name: "Notion AI", Price: "10.00", Currency: "EUR", Unit: "month", TermsInput: app.TermsInput{TrialEndsOn: "2026-11-08", CancelURL: "https://notion.example/billing"}},
		{Name: "Gym", Price: "39.00", Currency: "EUR", Unit: "month", FirstCharge: "2026-12-12", TermsInput: app.TermsInput{NoticeDays: 30, CancelURL: "https://gym.example/cancel"}},
		{Name: "example.com", Price: "12.00", Currency: "EUR", Unit: "year", FirstCharge: "2026-11-05", TermsInput: app.TermsInput{Renewal: "manual"}},
	} {
		if _, err := svc.CreateSubscription(ctx, owner, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	clock := scheduler.New(svc, slog.New(slog.DiscardHandler), eng)
	clock.Tick(ctx)

	var script strings.Builder
	find := func(title string) (string, actions.Message) {
		t.Helper()
		for id, m := range ch.Messages[chat.ChatID] {
			if strings.Contains(m.Title, title) {
				return id, m
			}
		}
		t.Fatalf("no message titled %q; log:\n%s", title, strings.Join(ch.Log, "\n"))
		return "", actions.Message{}
	}
	press := func(msg *actions.Message, label string) {
		t.Helper()
		d := fake.Button(*msg, label)
		if d == "" {
			t.Fatalf("no %q button in:\n%s", label, fake.Render(*msg))
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
			*msg = *rep.Edit
			script.WriteString(fake.Render(*msg))
		}
	}

	_, trial := find("free trial")
	script.WriteString("# A free trial is about to charge\n" + fake.Render(trial))
	press(&trial, "Cancel before it charges")
	press(&trial, "At period end")

	_, gym := find("to cancel Gym")
	script.WriteString("\n# A contract with 30 days' notice\n" + fake.Render(gym))
	press(&gym, "Keep it")

	_, domain := find("example.com expires")
	script.WriteString("\n# Something that expires unless renewed\n" + fake.Render(domain))
	press(&domain, "Let it expire")

	testutil.Golden(t, "terms_conversation", termID.ReplaceAllString(script.String(), "${1}_X"))
}

// TestReminderLogExplainsWhatHappened: a reminder for someone with no chat
// linked is not "sent"; the log says why it never arrived.
func TestReminderLogExplainsWhatHappened(t *testing.T) {
	ctx := context.Background()
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
	eng := actions.New(svc, slog.New(slog.DiscardHandler), fake.New())
	if _, err := svc.CreateSubscription(ctx, owner, app.CreateSubscriptionInput{Name: "Netflix", Price: "13.99", Currency: "EUR", FirstCharge: "2026-11-08"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	clock := scheduler.New(svc, slog.New(slog.DiscardHandler), eng)
	clock.Tick(ctx)
	clock.Tick(ctx) // it must not be retried forever
	log, err := svc.Reminders(ctx, owner, app.ReminderLogInput{})
	if err != nil {
		t.Fatal(err)
	}
	var failed, pending int
	for _, e := range log.Entries {
		switch e.Status {
		case "failed":
			failed++
			if e.Attempts != 1 || !strings.Contains(e.Explanation, "Alex had no chat or channel linked") {
				t.Fatalf("entry = %+v", e)
			}
		case "pending":
			pending++
		default:
			t.Fatalf("unexpected status %s", e.Status)
		}
	}
	if failed != 1 || pending == 0 {
		t.Fatalf("failed=%d pending=%d: %+v", failed, pending, log.Entries)
	}
}
