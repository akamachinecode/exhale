package slack

import (
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
)

// TestUnlinkedIsIgnoredThenLinks: someone the household does not know gets
// one short explanation and nothing else; with a code from the web they are
// linked and the app opens, in the DM and in the Home tab.
func TestUnlinkedIsIgnoredThenLinks(t *testing.T) {
	w := newWorld(t)
	w.adapter(t)
	api := w.api

	first := api.say(t, "U1", "subs")
	eventually(t, "the explanation", func() bool { return api.find("chat.postMessage", "not linked to Exhale yet") })
	second := api.say(t, "U1", "home")
	eventually(t, "both envelopes acknowledged", func() bool { return api.acked(first) && api.acked(second) })
	press := api.press(t, "U1", "D1", "1762333200.000001", "a|subs|0")
	eventually(t, "a private answer to the button", func() bool { return api.find("response_url", "Link your account first") })
	home := api.openHome(t, "U1")
	eventually(t, "the Home tab of a stranger", func() bool { return strings.Contains(api.home("U1"), "not linked to Exhale yet") })
	if n := api.count("chat.postMessage"); n != 1 {
		t.Fatalf("an unlinked person gets one explanation, got %d messages", n)
	}
	if api.find("chat.postMessage", "a|subs|0") || strings.Contains(api.home("U1"), "a|subs|0") {
		t.Fatal("an unlinked person must not see the app")
	}
	eventually(t, "every envelope acknowledged", func() bool { return api.acked(press) && api.acked(home) })

	api.say(t, "U1", "link aaaaaaaaaaaaaaaa")
	eventually(t, "a wrong code refused", func() bool { return api.find("chat.postMessage", "not valid anymore") })

	code, _ := auth.CreateLinkCode(w.ctx, w.st, w.owner.MemberID, Name)
	api.say(t, "U1", "  "+code+" ")
	eventually(t, "the confirmation", func() bool { return api.find("chat.postMessage", "Linked") })
	eventually(t, "the app message", func() bool { _, b := api.last("D1"); return buttonValue(b, "Subscriptions") == "a|subs|0" })
	eventually(t, "the app message pinned", func() bool { return api.count("pins.add") == 1 })
	eventually(t, "the Home tab", func() bool { return buttonValue(api.home("U1"), "Subscriptions") == "a|subs|0" })
	if actor, err := auth.MemberForChannel(w.ctx, w.st, Name, "U1"); err != nil || actor.MemberID != w.owner.MemberID {
		t.Fatalf("U1 should be linked to the owner: %+v %v", actor, err)
	}
	if _, err := auth.RedeemLinkCode(w.ctx, w.st, Name, code, "U2"); err == nil {
		t.Fatal("a code works once")
	}
}

// TestReminderAndButton: a reminder delivered by the scheduler arrives as a
// DM, and "Already paid" updates that same message.
func TestReminderAndButton(t *testing.T) {
	w := newWorld(t)
	w.adapter(t)
	api := w.api
	w.link(t, "U1", w.owner)
	subID, chargeID := w.netflix(t, false)

	if err := w.eng.Deliver(w.ctx, app.OutboxItem{Kind: app.KindReminder, ChargeID: chargeID, MemberID: w.owner.MemberID}); err != nil {
		t.Fatal(err)
	}
	ts, blocks := api.last("D1")
	if !strings.Contains(blocks, "Netflix renews in 3 days") || buttonValue(blocks, "Already paid") != "paid|"+chargeID {
		t.Fatalf("the reminder should arrive in the DM with its buttons, got:\n%s", blocks)
	}

	updates := api.count("chat.update")
	env := api.press(t, "U1", "D1", ts, buttonValue(blocks, "Already paid"))
	eventually(t, "the same message updated", func() bool { return strings.Contains(api.message("D1", ts), "Netflix paid") })
	eventually(t, "the button press acknowledged", func() bool { return api.acked(env) })
	if got := api.count("chat.update") - updates; got != 1 {
		t.Fatalf("one update of the pressed message expected, got %d", got)
	}
	if buttonValue(api.message("D1", ts), "Undo") == "" {
		t.Fatal("the updated message should offer Undo")
	}
	got, _ := w.svc.GetSubscription(w.ctx, w.owner, app.GetSubscriptionInput{ID: subID})
	if len(got.History) == 0 || got.History[0].Status != "paid" {
		t.Fatalf("charge not marked paid: %+v", got.History)
	}
	if api.count("response_url")+api.count("chat.postEphemeral") != 0 {
		t.Fatal("a toast is not repeated when the message itself changed")
	}

	// A toast that is the only feedback is whispered to the person.
	api.press(t, "U1", "D1", ts, "nonsense")
	eventually(t, "the whisper", func() bool { return api.find("response_url", "This button has expired.") })
}

// TestAddWizardInDM: the add wizard, typed and tapped in the DM. Commands
// are plain words here: Slack keeps the slash for itself.
func TestAddWizardInDM(t *testing.T) {
	w := newWorld(t)
	w.adapter(t)
	api := w.api
	w.link(t, "U1", w.owner)

	shows := func(what string) {
		t.Helper()
		eventually(t, what, func() bool { _, b := api.last("D1"); return strings.Contains(b, what) })
	}
	api.say(t, "U1", "Add")
	shows("What is it called?")
	api.say(t, "U1", "Netflix &amp; friends")
	shows("How much is each charge?")
	api.say(t, "U1", "13,99")
	shows("How often is it charged?")

	ts, blocks := api.last("D1")
	tap := func(label, then string) {
		t.Helper()
		v := buttonValue(api.message("D1", ts), label)
		if v == "" {
			t.Fatalf("no %q button in:\n%s", label, api.message("D1", ts))
		}
		api.press(t, "U1", "D1", ts, v)
		eventually(t, then, func() bool { return strings.Contains(api.message("D1", ts), then) })
	}
	if buttonValue(blocks, "Every month") != "a|af|month|1" {
		t.Fatalf("frequency buttons expected, got:\n%s", blocks)
	}
	tap("Every month", "When is the next charge?")
	if !strings.Contains(api.message("D1", ts), `"text":"·"`) {
		t.Fatalf("calendar padding should be sendable buttons:\n%s", api.message("D1", ts))
	}
	tap("12", "Which category?") // no payment methods yet: that step is skipped
	tap("Skip", "Add it")
	tap("Add it", "added")
	list, _ := w.svc.ListSubscriptions(w.ctx, w.owner, app.ListSubscriptionsInput{})
	if len(list.Subscriptions) != 1 || list.Subscriptions[0].Name != "Netflix & friends" || list.Subscriptions[0].NextCharge.String() != "2026-11-12" {
		t.Fatalf("the subscription was not created as typed: %+v", list.Subscriptions)
	}

	// With nothing pending, a word opens a screen and anything else lists the commands.
	api.say(t, "U1", "/upcoming")
	shows("Next 30 days")
	api.say(t, "U1", "what can you do?")
	eventually(t, "the help", func() bool { return api.find("chat.postMessage", "upcoming — Charges in the next 30 days") })
}

// TestSlashCommand: /exhale [screen] opens the app in the DM, wherever it
// was typed, and retires the app message it replaces.
func TestSlashCommand(t *testing.T) {
	w := newWorld(t)
	w.adapter(t)
	api := w.api

	api.slash(t, "U1", "C123", "subs")
	eventually(t, "the explanation for a stranger", func() bool { return api.find("response_url", "not linked to Exhale yet") })

	code, _ := auth.CreateLinkCode(w.ctx, w.st, w.owner.MemberID, Name)
	api.slash(t, "U1", "C123", "link "+code)
	eventually(t, "linked from the slash command", func() bool { return api.find("response_url", "Linked") })
	eventually(t, "the first app message", func() bool { return api.count("pins.add") == 1 })
	first, _ := api.last("D1")

	env := api.slash(t, "U1", "C123", "subs")
	eventually(t, "the subscriptions screen in the DM", func() bool { _, b := api.last("D1"); return strings.Contains(b, "Subscriptions (0)") })
	eventually(t, "pinned, the old one unpinned", func() bool { return api.count("pins.add") == 2 && api.count("pins.remove") == 1 })
	eventually(t, "the old app message retired", func() bool { return strings.Contains(api.message("D1", first), "moved to a newer message") })
	eventually(t, "where it opened", func() bool { return api.find("response_url", "open in our direct messages") })
	eventually(t, "the slash command acknowledged", func() bool { return api.acked(env) })
	if strings.Contains(api.home("U1"), "moved to a newer message") {
		t.Fatal("the Home tab is never retired by an app message")
	}

	before := api.count("response_url")
	api.slash(t, "U1", "D1", "")
	eventually(t, "home, from the DM itself", func() bool { _, b := api.last("D1"); return buttonValue(b, "Summary") != "" })
	api.slash(t, "U1", "D1", "dance")
	eventually(t, "the help", func() bool { return api.find("response_url", "Type one of these") })
	if got := api.count("response_url") - before; got != 1 {
		t.Fatalf("typed in the DM, only the help is whispered; got %d whispers", got)
	}
}

// TestAppHome: the Home tab is the home screen, navigates by publishing
// again, stays live as data changes and sends typed questions to the DM.
func TestAppHome(t *testing.T) {
	w := newWorld(t)
	w.adapter(t)
	go w.eng.Sync(w.ctx)
	api := w.api
	w.link(t, "U1", w.owner)
	eventually(t, "the Home tab after linking", func() bool { return api.home("U1") != "" })

	published := api.count("views.publish")
	env := api.openHome(t, "U1")
	eventually(t, "the home view", func() bool { return api.count("views.publish") == published+1 })
	if h := api.home("U1"); !strings.Contains(h, "🏠 Acme") || buttonValue(h, "Subscriptions") != "a|subs|0" {
		t.Fatalf("the Home tab should show the home screen:\n%s", h)
	}
	eventually(t, "app_home_opened acknowledged", func() bool { return api.acked(env) })

	api.pressHome(t, "U1", "a|subs|0")
	eventually(t, "the subscriptions screen", func() bool { return strings.Contains(api.home("U1"), "Subscriptions (0)") })

	// Live: a change from anywhere redraws the screen the tab shows.
	w.netflix(t, false)
	eventually(t, "the new subscription in the Home tab", func() bool { return strings.Contains(api.home("U1"), "Netflix · €19.99/mo") })

	// A question that needs typing continues in the DM.
	api.pressHome(t, "U1", "a|add")
	eventually(t, "the question in the DM", func() bool { _, b := api.last("D1"); return strings.Contains(b, "What is it called?") })
	eventually(t, "a pointer in the Home tab", func() bool { return strings.Contains(api.home("U1"), "continue in the Messages tab") })
	api.say(t, "U1", "Spotify")
	eventually(t, "the wizard goes on in the DM", func() bool { _, b := api.last("D1"); return strings.Contains(b, "How much is each charge?") })

	// Charge buttons inside the Home tab keep the way back.
	api.say(t, "U1", "home") // typed as an answer: the price question rejects it
	api.slash(t, "U1", "D1", "home")
	api.openHome(t, "U1")
	eventually(t, "home again", func() bool { return strings.HasPrefix(buttonValue(api.home("U1"), "Netflix"), "a|chg|") })
	api.pressHome(t, "U1", buttonValue(api.home("U1"), "Netflix"))
	eventually(t, "the charge screen", func() bool { return buttonValue(api.home("U1"), "Already paid") != "" })
	api.pressHome(t, "U1", buttonValue(api.home("U1"), "Already paid"))
	eventually(t, "paid, with the way home", func() bool {
		h := api.home("U1")
		return strings.Contains(h, "Netflix paid") && buttonValue(h, "Home") == "a|home"
	})
	if _, b := api.last("D1"); strings.Contains(b, "moved to a newer message") {
		t.Fatal("the Home tab must not retire the app message in the DM")
	}
}

// TestReconnect: when the socket drops, the adapter opens a new one and
// keeps working.
func TestReconnect(t *testing.T) {
	w := newWorld(t)
	a := w.adapter(t)
	api := w.api
	w.link(t, "U1", w.owner)

	api.drop()
	eventually(t, "a second socket", func() bool {
		api.mu.Lock()
		defer api.mu.Unlock()
		return api.sockets == 2 && len(api.opens) == 2 && api.conn != nil
	})
	eventually(t, "connected again", a.Connected)
	env := api.say(t, "U1", "summary")
	eventually(t, "work after the reconnect", func() bool { _, b := api.last("D1"); return strings.Contains(b, "📊 Summary") })
	eventually(t, "the acknowledgement on the new socket", func() bool { return api.acked(env) })
	if a.LastError() != "" {
		t.Fatalf("no error should remain after reconnecting: %q", a.LastError())
	}
}
