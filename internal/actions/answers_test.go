package actions_test

import (
	"context"
	"log/slog"
	"regexp"
	"strconv"
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

var dupKey = regexp.MustCompile(`\|[0-9a-f]{16}\b`)

// answers is a household with a clock the test moves, one fake chat per
// member, and the scheduler that plans and delivers.
type answers struct {
	t      *testing.T
	ctx    context.Context
	now    time.Time
	svc    *app.Service
	st     *store.Store
	ch     *fake.Channel
	eng    *actions.Engine
	clock  *scheduler.Scheduler
	owner  app.Actor
	script strings.Builder
	seen   int
}

func newAnswers(t *testing.T) *answers {
	t.Helper()
	a := &answers{t: t, ctx: context.Background()}
	st, err := store.OpenMemory(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	madrid, _ := time.LoadLocation("Europe/Madrid")
	a.now = time.Date(2026, 11, 5, 9, 0, 0, 0, madrid)
	a.st = st
	a.svc = app.New(st, bus.New(), nil, func() time.Time { return a.now })
	if a.owner, err = a.svc.Bootstrap(a.ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	a.ch = fake.New()
	a.eng = actions.New(a.svc, slog.New(slog.DiscardHandler), a.ch)
	a.clock = scheduler.New(a.svc, slog.New(slog.DiscardHandler), a.eng)
	a.link(a.owner.MemberID, "alex")
	return a
}

func (a *answers) link(memberID, chat string) {
	a.t.Helper()
	if err := a.st.Q.LinkChannel(a.ctx, db.LinkChannelParams{ID: "lnk_" + chat, MemberID: memberID, Channel: "fake", ExternalID: chat, CreatedAt: store.FormatTime(a.now)}); err != nil {
		a.t.Fatal(err)
	}
}

// at moves the clock, plans and delivers what is due, and returns the
// messages sent meanwhile.
func (a *answers) at(when string) []string {
	a.t.Helper()
	madrid, _ := time.LoadLocation("Europe/Madrid")
	t, err := time.ParseInLocation("2006-01-02 15:04", when, madrid)
	if err != nil {
		a.t.Fatal(err)
	}
	a.now = t
	if err := a.svc.Plan(a.ctx); err != nil {
		a.t.Fatal(err)
	}
	a.clock.Tick(a.ctx)
	out := []string{}
	for _, l := range a.ch.Log[a.seen:] {
		if strings.HasPrefix(l, "SEND") {
			out = append(out, l)
		}
	}
	a.seen = len(a.ch.Log)
	if len(out) > 0 {
		a.script.WriteString("\n=== " + when + "\n" + strings.Join(out, "\n"))
	}
	return out
}

// find returns the id of the newest message in chat whose title is title.
func (a *answers) find(chat, title string) string {
	a.t.Helper()
	best := 0
	for k, m := range a.ch.Messages[chat] {
		if n, _ := strconv.Atoi(k); m.Title == title && n > best {
			best = n
		}
	}
	if best == 0 {
		a.t.Fatalf("no message %q in %s", title, chat)
	}
	return strconv.Itoa(best)
}

// press presses the button labelled label on message id of chat.
func (a *answers) press(actor app.Actor, chat, id, label string) actions.Message {
	a.t.Helper()
	msg := a.ch.Messages[chat][id]
	d := fake.Button(msg, label)
	if d == "" {
		a.t.Fatalf("no %q button in:\n%s", label, fake.Render(msg))
	}
	if len(d) > 64 {
		a.t.Fatalf("button data %q exceeds Telegram's 64 bytes", d)
	}
	rep, err := a.eng.Handle(a.ctx, actor, actions.ChatRef{Channel: "fake", ChatID: chat, MessageID: id}, d)
	if err != nil {
		a.t.Fatal(err)
	}
	a.script.WriteString("\n> " + chat + " presses " + label + "\n")
	if rep.Toast != "" {
		a.script.WriteString("(toast: " + rep.Toast + ")\n")
	}
	if rep.Edit == nil {
		return actions.Message{}
	}
	a.script.WriteString(fake.Render(*rep.Edit))
	a.ch.Messages[chat][id] = *rep.Edit
	return *rep.Edit
}

func (a *answers) setRepeat(times, every int) {
	a.t.Helper()
	if _, err := a.svc.UpdateHousehold(a.ctx, a.owner, app.UpdateHouseholdInput{ConfirmTimes: &times, ConfirmEveryDays: &every}); err != nil {
		a.t.Fatal(err)
	}
}

func titles(sent []string) []string {
	out := make([]string, len(sent))
	for i, s := range sent {
		lines := strings.Split(s, "\n")
		out[i] = lines[1]
	}
	return out
}

// TestNotYetAsksAgainTomorrow is the bug the "Not yet" button had: it said
// "I'll ask again tomorrow" and nothing ever did. With repeats switched
// off, the question still comes back the next day at the reminder hour.
func TestNotYetAsksAgainTomorrow(t *testing.T) {
	a := newAnswers(t)
	a.setRepeat(1, 1) // asked once: only "Not yet" can bring it back
	if _, err := a.svc.CreateSubscription(a.ctx, a.owner, app.CreateSubscriptionInput{Name: "Netflix", Price: "12.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-05"}); err != nil {
		t.Fatal(err)
	}
	if got := titles(a.at("2026-11-05 09:00")); len(got) != 1 || got[0] != "🔔 Netflix renews today" {
		t.Fatalf("on the day: %q", got)
	}
	if got := titles(a.at("2026-11-06 09:00")); len(got) != 1 || got[0] != "❓ Was Netflix charged?" {
		t.Fatalf("a day after: %q", got)
	}
	m := a.press(a.owner, "alex", a.find("alex", "❓ Was Netflix charged?"), "Not yet")
	if m.Footer != "OK — I'll ask again tomorrow." {
		t.Fatalf("footer = %q", m.Footer)
	}
	if got := a.at("2026-11-07 08:59"); len(got) != 0 {
		t.Fatalf("asked before the reminder hour: %q", titles(got))
	}
	if got := titles(a.at("2026-11-07 09:00")); len(got) != 1 || got[0] != "❓ Was Netflix charged?" {
		t.Fatalf("the question did not come back the next day: %q", got)
	}
	if got := a.at("2026-11-08 09:00"); len(got) != 0 {
		t.Fatalf("asked again without being asked to: %q", titles(got))
	}
	// A restart in between changes nothing: it lives in the outbox.
	a.press(a.owner, "alex", a.find("alex", "❓ Was Netflix charged?"), "Not yet")
	a.clock = scheduler.New(a.svc, slog.New(slog.DiscardHandler), a.eng)
	if got := titles(a.at("2026-11-09 11:30")); len(got) != 1 {
		t.Fatalf("after a restart and downtime: %q", got)
	}
}

// TestConfirmRepeatsUntilAnswered: by default "was it charged?" comes back
// daily up to three times, and stops as soon as someone answers.
func TestConfirmRepeatsUntilAnswered(t *testing.T) {
	a := newAnswers(t)
	if _, err := a.svc.CreateSubscription(a.ctx, a.owner, app.CreateSubscriptionInput{Name: "Netflix", Price: "12.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-05"}); err != nil {
		t.Fatal(err)
	}
	a.at("2026-11-05 09:00")
	for _, day := range []string{"2026-11-06", "2026-11-07", "2026-11-08"} {
		if got := titles(a.at(day + " 09:00")); len(got) != 1 || got[0] != "❓ Was Netflix charged?" {
			t.Fatalf("%s: %q", day, got)
		}
	}
	if got := a.at("2026-11-09 09:00"); len(got) != 0 {
		t.Fatalf("asked a fourth time: %q", titles(got))
	}

	// Answering stops the repeats.
	if _, err := a.svc.CreateSubscription(a.ctx, a.owner, app.CreateSubscriptionInput{Name: "Spotify", Price: "10.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-10"}); err != nil {
		t.Fatal(err)
	}
	a.at("2026-11-10 09:00")
	a.at("2026-11-11 09:00")
	a.press(a.owner, "alex", a.find("alex", "❓ Was Spotify charged?"), "Paid")
	if got := a.at("2026-11-12 09:00"); len(got) != 0 {
		t.Fatalf("asked again after the answer: %q", titles(got))
	}

	// Every 2 days, twice: the repeat follows the new setting.
	a.setRepeat(2, 2)
	if _, err := a.svc.CreateSubscription(a.ctx, a.owner, app.CreateSubscriptionInput{Name: "Disney+", Price: "8.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-15"}); err != nil {
		t.Fatal(err)
	}
	a.at("2026-11-15 09:00")
	if got := a.at("2026-11-16 09:00"); len(got) != 1 {
		t.Fatalf("first ask: %q", titles(got))
	}
	if got := a.at("2026-11-17 09:00"); len(got) != 0 {
		t.Fatalf("repeated after one day instead of two: %q", titles(got))
	}
	if got := a.at("2026-11-18 09:00"); len(got) != 1 {
		t.Fatalf("second ask: %q", titles(got))
	}
}

// TestAnswersConversation records the messages that end in an answer:
// snoozing a reminder and a keep-or-cancel question, the message about a
// service paid twice, and the one about a card about to expire.
func TestAnswersConversation(t *testing.T) {
	a := newAnswers(t)
	ctx := a.ctx
	ana := app.Actor{Kind: app.ActorMember, MemberID: "mem_ana0000000000000", HouseholdID: a.owner.HouseholdID, Label: "Ana", Scope: app.ScopeWrite}
	if err := a.st.Q.CreateMember(ctx, db.CreateMemberParams{ID: ana.MemberID, HouseholdID: ana.HouseholdID, Name: "Ana", Role: "member", CreatedAt: store.FormatTime(a.now)}); err != nil {
		t.Fatal(err)
	}
	a.link(ana.MemberID, "ana")
	visa, _ := a.svc.SavePaymentMethod(ctx, a.owner, app.SavePaymentMethodInput{Name: "Visa", Kind: "card", Last4: "4421", ExpiresOn: "2026-11-30"})
	revolut, _ := a.svc.SavePaymentMethod(ctx, a.owner, app.SavePaymentMethodInput{Name: "Revolut", Kind: "card", Last4: "0937"})
	for _, in := range []app.CreateSubscriptionInput{
		{Name: "Spotify", Price: "10.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-20", PaymentMethodID: visa.ID},
		{Name: "Spotify Premium", Price: "10.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-25", PayerID: ana.MemberID},
		{Name: "iCloud", Price: "2.99", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-08", PaymentMethodID: visa.ID},
		{Name: "Gym", Price: "39.00", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-19", TermsInput: app.TermsInput{NoticeDays: 7}},
	} {
		if _, err := a.svc.CreateSubscription(ctx, a.owner, in); err != nil {
			t.Fatal(err)
		}
	}

	// 9:00 today: the iCloud reminder, the duplicate (to both payers), the
	// card and the gym's "keep or cancel?" a week before its last day (the
	// 12th). Notices go out at the reminder time, once.
	sent := a.at("2026-11-05 09:00")
	if got := titles(sent); len(got) != 5 {
		t.Fatalf("first morning: %q\n%s", got, strings.Join(sent, "\n"))
	}
	if got := a.at("2026-11-05 15:00"); len(got) != 0 {
		t.Fatalf("notices must go out once: %q", titles(got))
	}
	a.press(a.owner, "alex", a.find("alex", "⏰ 7 days left to cancel Gym"), "In 3 days")

	// The duplicate: Ana says that's fine; nobody hears about it again.
	a.press(ana, "ana", a.find("ana", "👯 You and Alex both pay for Spotify"), "That's fine")
	audit, err := a.svc.Audit(ctx, a.owner, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Duplicates) != 0 {
		t.Fatalf("a dismissed duplicate is still reported: %+v", audit.Duplicates)
	}

	// The card: move everything to Revolut in one step.
	card := a.find("alex", "💳 Visa ••4421 expires in 25 days")
	a.press(a.owner, "alex", card, "Move them to another card")
	a.press(a.owner, "alex", card, "Revolut ••0937")
	list, _ := a.svc.ListSubscriptions(ctx, a.owner, app.ListSubscriptionsInput{})
	for _, s := range list.Subscriptions {
		if s.PaymentMethodID == visa.ID {
			t.Fatalf("%s still uses the expiring card", s.Name)
		}
	}

	// iCloud renews on the 8th: snoozed until tomorrow.
	a.press(a.owner, "alex", a.find("alex", "🔔 iCloud renews in 3 days"), "Remind me tomorrow")
	if got := titles(a.at("2026-11-06 09:00")); len(got) != 1 || got[0] != "🔔 iCloud renews in 2 days" {
		t.Fatalf("the snoozed reminder: %q", got)
	}
	got := titles(a.at("2026-11-08 09:00"))
	if len(got) != 2 || got[0] != "🔔 iCloud renews today" && got[1] != "🔔 iCloud renews today" {
		t.Fatalf("the 8th: %q", got)
	}
	a.press(a.owner, "alex", a.find("alex", "🔔 iCloud renews today"), "Already paid")

	// The gym again, day by day; two days before the last day "in 3 days"
	// would be too late: it comes back on the last day instead, once.
	a.press(a.owner, "alex", a.find("alex", "⏰ 4 days left to cancel Gym"), "Remind me tomorrow")
	a.at("2026-11-09 09:00")
	a.press(a.owner, "alex", a.find("alex", "⏰ 3 days left to cancel Gym"), "Remind me tomorrow")
	a.at("2026-11-10 09:00")
	a.press(a.owner, "alex", a.find("alex", "⏰ 2 days left to cancel Gym"), "On the last day")
	if got := a.at("2026-11-11 09:00"); len(got) != 0 {
		t.Fatalf("the 11th: %q", titles(got))
	}
	if got := titles(a.at("2026-11-12 09:00")); len(got) != 1 || got[0] != "⏰ Last day to cancel Gym: today" {
		t.Fatalf("the last day: %q", got)
	}
	if got := a.at("2026-11-13 09:00"); len(got) != 0 {
		t.Fatalf("after the last day: %q", titles(got))
	}
	_ = revolut

	out := termID.ReplaceAllString(a.script.String(), "${1}_X")
	out = strings.ReplaceAll(out, ana.MemberID, "mem_X")
	out = dupKey.ReplaceAllString(out, "|KEY")
	testutil.Golden(t, "answers_conversation", out)
}

