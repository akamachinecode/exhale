package actions_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/assistant"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/fake"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/testutil"
)

// TestImportConversation: a PDF statement sent to the bot comes back as a
// list. The charges it confirms are marked paid, a recurring payment that
// is not in Exhale is offered as a new subscription, and an expected charge
// that is not there is listed. Lines are skipped with their number;
// nothing changes before Apply. A charge paid at another amount gets the
// usual "new price?" question. The assistant is off: no model reads
// anything.
func TestImportConversation(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	madrid, _ := time.LoadLocation("Europe/Madrid")
	now := time.Date(2026, 9, 1, 9, 0, 0, 0, madrid)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	owner.Scope = app.ScopeWrite
	subs := map[string]string{}
	for _, in := range []app.CreateSubscriptionInput{
		{Name: "Netflix", Price: "12.99", Currency: "EUR", FirstCharge: "2026-09-06"},
		{Name: "Spotify", Price: "17.99", Currency: "EUR", FirstCharge: "2026-09-28"},
		{Name: "Gym", Price: "30.00", Currency: "EUR", FirstCharge: "2026-10-01"},
	} {
		d, err := svc.CreateSubscription(ctx, owner, in)
		if err != nil {
			t.Fatal(err)
		}
		subs[in.Name] = d.Subscription.ID
	}
	now = time.Date(2026, 11, 5, 9, 0, 0, 0, madrid)

	ch := fake.New()
	eng := actions.New(svc, slog.New(slog.DiscardHandler), ch)
	asst, err := assistant.New(svc, nil, assistant.Env{}) // configured by nobody: off
	if err != nil {
		t.Fatal(err)
	}
	eng.SetAssistant(asst)
	eng.SetPublicURL("https://exhale.example.com/")
	chat := actions.ChatRef{Channel: "fake", ChatID: "chat-1"}

	var script strings.Builder
	var last actions.Message
	send := func(name string) {
		t.Helper()
		data, err := os.ReadFile("../importer/testdata/" + name)
		if name == "many.csv" {
			// Ten services, three months each: an invented export.
			var b strings.Builder
			b.WriteString("Date,Description,Amount,Balance\n")
			for i, svc := range []string{"Alpha Cloud", "Bravo News", "Charlie Music", "Delta Games", "Echo Books", "Foxtrot Fit", "Golf Club", "Hotel Wifi", "India Pay", "Juliet Mail"} {
				for _, month := range []string{"08", "09", "10"} {
					fmt.Fprintf(&b, "2026-%s-%02d,%s,-%d.99,100\n", month, 10+i, strings.ToUpper(svc), 3+i)
				}
			}
			data, err = []byte(b.String()), nil
		}
		if err != nil {
			t.Fatal(err)
		}
		last = eng.ImportFile(ctx, owner, chat, data)
		script.WriteString("\n> send " + name + "\n" + fake.Render(last))
	}
	press := func(label string) {
		t.Helper()
		d := fake.Button(last, label)
		if d == "" {
			t.Fatalf("no %q button in:\n%s", label, fake.Render(last))
		}
		if len(d) > 64 {
			t.Fatalf("button data %q exceeds Telegram's 64 bytes", d)
		}
		sent := len(ch.Log)
		rep, err := eng.Handle(ctx, owner, actions.ChatRef{Channel: "fake", ChatID: "chat-1", MessageID: "9"}, d)
		if err != nil {
			t.Fatal(err)
		}
		script.WriteString("\n> press " + label + "\n")
		if rep.Toast != "" {
			script.WriteString("(toast: " + rep.Toast + ")\n")
		}
		if rep.Edit != nil {
			script.WriteString(fake.Render(*rep.Edit))
			last = *rep.Edit
		}
		for _, l := range ch.Log[sent:] {
			script.WriteString(l)
		}
		if len(ch.Log) > sent {
			last = ch.Messages[chat.ChatID][strings.TrimPrefix(strings.SplitN(ch.Log[len(ch.Log)-1], "\n", 2)[0], "SEND #")]
		}
	}
	paid := func() int {
		l, _ := svc.ListCharges(ctx, owner, app.ListChargesInput{From: "2026-09-01", To: "2026-10-31"})
		n := 0
		for _, c := range l.Charges {
			if c.Status == domain.ChargePaid {
				n++
			}
		}
		return n
	}

	send("statement.pdf")
	press("✅ 5") // skip the new subscription
	press("⬜ 5") // and take it back
	press("✅ 1")
	if paid() != 0 {
		t.Fatal("a charge was marked paid before Apply")
	}
	press("Apply 4 of 5")
	if paid() != 3 {
		t.Fatalf("%d charges paid, want 3", paid())
	}
	press("Yes, from now on")
	d, err := svc.GetSubscription(ctx, owner, app.GetSubscriptionInput{ID: subs["Netflix"]})
	if err != nil || d.Subscription.Price.Decimal() != "15.99" {
		t.Fatalf("netflix price: %+v %v", d.Subscription.Price, err)
	}
	l, _ := svc.ListSubscriptions(ctx, owner, app.ListSubscriptionsInput{})
	if len(l.Subscriptions) != 4 {
		t.Fatalf("%d subscriptions, want 4 (Adobe added)", len(l.Subscriptions))
	}

	// The old list's buttons do nothing once it was applied.
	rep, err := eng.Handle(ctx, owner, chat, "imp|ok|zzzzzz")
	if err != nil || rep.Edit == nil || !strings.Contains(rep.Toast, "expired") {
		t.Fatalf("expired list: %+v %v", rep, err)
	}

	// Sent again, everything is already done.
	send("statement.pdf")
	// Six months of an account, then ten new services: too long to tick in
	// a chat, so the web is offered.
	send("bank.csv")
	press("Discard")
	send("many.csv")
	if rev, err := asst.LoadReview(ctx, owner); err != nil || rev.Channel != "fake" {
		t.Fatalf("the long list must wait for the web: %+v %v", rev, err)
	}
	press("Discard")
	if _, err := asst.LoadReview(ctx, owner); err == nil {
		t.Fatal("a discarded list is still waiting")
	}
	// A scan needs the model.
	send("scan.pdf")

	// Review ids are random; make the transcript stable.
	out := regexp.MustCompile(`\(imp\|(t|ok|no)\|[a-z2-7]+`).ReplaceAllString(script.String(), "(imp|$1|X")
	out = regexp.MustCompile(`(chg|aud)_[a-z0-9]+`).ReplaceAllString(out, "${1}_X")
	testutil.Golden(t, "import_conversation", out)
}
