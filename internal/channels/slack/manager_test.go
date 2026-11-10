package slack

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/store/db"
)

func (w *world) manager(t *testing.T, envBot, envApp string) *Manager {
	t.Helper()
	m := NewManager(envBot, envApp, w.api.apiURL(), w.svc, w.log)
	w.eng = actions.New(w.svc, w.log, m)
	m.SetEngine(w.eng)
	go m.Serve(w.ctx)
	return m
}

func (w *world) saved(key string) string {
	v, _ := w.st.Q.GetSetting(w.ctx, key)
	return v
}

// TestManagerConfiguredFromTheWeb: no tokens at start; the owner pastes
// them in the web. They are checked with Slack before being saved, never
// handed back, and the app starts, swaps and stops without a restart.
func TestManagerConfiguredFromTheWeb(t *testing.T) {
	w := newWorld(t)
	api := w.api
	m := w.manager(t, "", "")

	st := m.Status(w.ctx)
	if st.Configured || st.Running || !strings.Contains(st.Manifest, "socket_mode_enabled: true") || !strings.HasPrefix(st.CreateURL, "https://api.slack.com/apps?new_app=1&manifest_yaml=") {
		t.Fatalf("not set up yet, but the manifest is always there: %+v", st)
	}
	if _, err := m.Send(w.ctx, "U1", actions.Message{Title: "x"}); err == nil {
		t.Fatal("sending without an app should fail")
	}

	// Refused before anything reaches Slack or the database.
	for name, in := range map[string]Config{
		"nothing":       {},
		"swapped":       {BotToken: testApp, AppToken: testBot},
		"no app token":  {BotToken: testBot},
		"not a token":   {BotToken: "hello", AppToken: "world"},
		"channel alone": {Channel: new("#subscriptions")},
	} {
		if _, err := m.Configure(w.ctx, in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if api.count("auth.test")+api.count("apps.connections.open") != 0 {
		t.Fatal("malformed tokens should not be sent to Slack")
	}

	// Refused by Slack: nothing is saved.
	api.reject["xoxb-revoked"] = true
	api.reject["xapp-revoked"] = true
	if _, err := m.Configure(w.ctx, Config{BotToken: "xoxb-revoked", AppToken: testApp}); err == nil || !strings.Contains(err.Error(), "bot token") {
		t.Fatalf("a bot token Slack rejects was accepted: %v", err)
	}
	_, err := m.Configure(w.ctx, Config{BotToken: testBot, AppToken: "xapp-revoked"})
	if err == nil || !strings.Contains(err.Error(), "app-level token") {
		t.Fatalf("an app-level token Slack rejects was accepted: %v", err)
	}
	if strings.Contains(err.Error(), testBot) || strings.Contains(err.Error(), "xapp-revoked") {
		t.Fatalf("an error must not carry a token: %v", err)
	}
	if w.saved(settingBotToken) != "" || w.saved(settingAppToken) != "" || m.Status(w.ctx).Configured {
		t.Fatal("tokens were saved before Slack accepted both")
	}

	st, err = m.Configure(w.ctx, Config{BotToken: " " + testBot + " ", AppToken: testApp})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Team != "Acme" || st.Bot != "exhale" || st.Source != "settings" || st.AppURL != "https://slack.com/app_redirect?app=A1&team=T1" {
		t.Fatalf("the app should run after configure: %+v", st)
	}
	if w.saved(settingBotToken) != testBot || w.saved(settingAppToken) != testApp {
		t.Fatal("tokens were not saved")
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), "xoxb-") || strings.Contains(string(b), "xapp-") {
		t.Fatalf("the status must never carry a token: %s", b)
	}
	if _, err := m.Send(w.ctx, "U1", actions.Message{Title: "hello"}); err != nil {
		t.Fatalf("send through the configured app: %v", err)
	}

	// Hot swap: new tokens open a new socket; the old one is closed.
	const bot2, app2 = "xoxb-second-workspace-3333", "xapp-1-A2-second-4444"
	if st, err = m.Configure(w.ctx, Config{BotToken: bot2, AppToken: app2}); err != nil || !st.Running {
		t.Fatalf("swap: %+v %v", st, err)
	}
	api.mu.Lock()
	opens, sockets := strings.Join(api.opens, " "), api.sockets
	api.mu.Unlock()
	if !strings.HasSuffix(opens, app2) || sockets != 2 {
		t.Fatalf("the new app-level token should open a second socket: %d sockets, opened by %q", sockets, opens)
	}
	if _, err := m.Send(w.ctx, "U1", actions.Message{Title: "after the swap"}); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	last := api.calls[len(api.calls)-1]
	api.mu.Unlock()
	if last.Method != "chat.postMessage" || last.Token != bot2 {
		t.Fatalf("messages should go out with the new bot token, got %s", last.Method)
	}

	st, err = m.Remove(w.ctx)
	if err != nil || st.Configured || st.Running {
		t.Fatalf("removed: %+v %v", st, err)
	}
	if w.saved(settingBotToken) != "" || w.saved(settingAppToken) != "" {
		t.Fatal("tokens should be gone")
	}
	if _, err := m.Send(w.ctx, "U1", actions.Message{Title: "x"}); err == nil {
		t.Fatal("sending after removal should fail")
	}
}

// TestManagerEnvironmentWins: tokens from the environment run the app and
// cannot be changed from the web.
func TestManagerEnvironmentWins(t *testing.T) {
	w := newWorld(t)
	m := w.manager(t, testBot, testApp)
	eventually(t, "the app from the environment", func() bool { return m.Status(w.ctx).Running })
	if st := m.Status(w.ctx); st.Source != "env" {
		t.Fatalf("source: %+v", st)
	}
	if _, err := m.Configure(w.ctx, Config{BotToken: "xoxb-other", AppToken: "xapp-other"}); err == nil {
		t.Fatal("tokens from the environment must not be overridden from the web")
	}
	if _, err := m.Remove(w.ctx); err == nil {
		t.Fatal("tokens from the environment must not be removed from the web")
	}
	if half := NewManager(testBot, "", w.api.apiURL(), w.svc, w.log); half.Status(w.ctx).Configured {
		t.Fatal("one token alone is not a configuration")
	}
}

// TestManagerStopsOnRejectedTokens: Slack revokes the app; the status says
// why instead of spinning.
func TestManagerStopsOnRejectedTokens(t *testing.T) {
	w := newWorld(t)
	w.api.reject[testApp] = true
	_ = w.st.Q.PutSetting(w.ctx, db.PutSettingParams{Key: settingBotToken, Value: testBot})
	_ = w.st.Q.PutSetting(w.ctx, db.PutSettingParams{Key: settingAppToken, Value: testApp})
	m := w.manager(t, "", "")
	eventually(t, "the reason", func() bool { st := m.Status(w.ctx); return !st.Running && strings.Contains(st.Error, "invalid_auth") })
	if strings.Contains(m.Status(w.ctx).Error, "xapp-") {
		t.Fatal("the reason must not carry a token")
	}
	// Fixed in the web: it starts again.
	delete(w.api.reject, testApp)
	if st, err := m.Configure(w.ctx, Config{BotToken: testBot, AppToken: testApp}); err != nil || !st.Running || st.Error != "" {
		t.Fatalf("after fixing: %+v %v", st, err)
	}
}

// TestTeamChannel: reminders of shared subscriptions are also posted once
// to the team channel, where each person acts as themselves.
func TestTeamChannel(t *testing.T) {
	w := newWorld(t)
	api := w.api
	m := w.manager(t, "", "")
	if _, err := m.Configure(w.ctx, Config{BotToken: testBot, AppToken: testApp}); err != nil {
		t.Fatal(err)
	}
	w.link(t, "U1", w.owner)

	// A teammate joins from Slack with an invite, no web needed.
	invite, _, err := auth.CreateInvite(w.ctx, w.st, w.owner, "member")
	if err != nil {
		t.Fatal(err)
	}
	api.slash(t, "U2", "C123", "join "+invite)
	eventually(t, "the welcome", func() bool { return api.find("response_url", "Welcome to Acme, Marta G") })
	marta, err := auth.MemberForChannel(w.ctx, w.st, Name, "U2")
	if err != nil {
		t.Fatal(err)
	}
	api.slash(t, "U2", "C123", "join "+invite)
	eventually(t, "an invite works once", func() bool { return api.find("response_url", "already linked") })

	if _, err := m.Configure(w.ctx, Config{Channel: new("nowhere")}); err == nil || !strings.Contains(err.Error(), "#nowhere") {
		t.Fatalf("a channel the app cannot post in was accepted: %v", err)
	}
	st, err := m.Configure(w.ctx, Config{Channel: new("subscriptions")})
	if err != nil || st.Channel != "#subscriptions" || !st.Running {
		t.Fatalf("channel: %+v %v", st, err)
	}
	if _, b := api.last("C777"); !strings.Contains(b, "Exhale posts renewals here") {
		t.Fatalf("the channel should be told, got:\n%s", b)
	}

	_, shared := w.netflix(t, false)
	d, err := w.svc.CreateSubscription(w.ctx, w.owner, app.CreateSubscriptionInput{Name: "Therapy", Price: "60", Currency: "EUR", Unit: "month", FirstCharge: "2026-11-08", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	private := d.Upcoming[0].ID
	deliver := m.Deliverer(w.eng)
	for _, it := range []app.OutboxItem{
		{Kind: app.KindReminder, ChargeID: shared, MemberID: w.owner.MemberID},
		{Kind: app.KindReminder, ChargeID: shared, MemberID: marta.MemberID},
		{Kind: app.KindReminder, ChargeID: private, MemberID: w.owner.MemberID},
	} {
		if err := deliver.Deliver(w.ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	inChannel, dms := 0, 0
	api.mu.Lock()
	for key, blocks := range api.msgs {
		ch, _, _ := strings.Cut(key, "|")
		switch {
		case ch == "C777" && strings.Contains(blocks, "Therapy"):
			t.Error("a private subscription reached the channel")
		case ch == "C777" && strings.Contains(blocks, "Netflix renews"):
			inChannel++
		case strings.Contains(blocks, "Netflix renews"):
			dms++
		}
	}
	api.mu.Unlock()
	if inChannel != 1 || dms != 2 {
		t.Fatalf("one post in the channel and one DM each expected, got %d and %d", inChannel, dms)
	}
	if _, b := api.last("D1"); !strings.Contains(b, "Therapy renews") {
		t.Fatal("the private reminder should still reach its owner's DM")
	}

	ts, blocks := api.last("C777")
	// A stranger in the channel cannot act.
	api.press(t, "U9", "C777", ts, buttonValue(blocks, "Already paid"))
	eventually(t, "the stranger told to link", func() bool { return api.find("response_url", "Link your account first") })
	if strings.Contains(api.message("C777", ts), "Netflix paid") {
		t.Fatal("an unlinked person changed a charge")
	}

	// A question that needs typing goes to the DM of whoever pressed.
	api.press(t, "U2", "C777", ts, buttonValue(blocks, "Amount"))
	eventually(t, "the question in Marta's DM", func() bool { _, b := api.last("D2"); return strings.Contains(b, "New amount for Netflix") })
	eventually(t, "a pointer in the channel", func() bool { return api.find("response_url", "I sent you a direct message") })

	// Marta marks it paid in the channel: the message changes for everyone,
	// as Marta.
	api.press(t, "U2", "C777", ts, buttonValue(blocks, "Already paid"))
	eventually(t, "paid in the channel", func() bool { return strings.Contains(api.message("C777", ts), "Netflix paid") })
	if buttonValue(api.message("C777", ts), "Undo") == "" {
		t.Fatal("the channel message should keep its Undo")
	}
	log, _ := w.svc.ListAudit(w.ctx, w.owner, app.AuditLogInput{})
	byMarta := false
	for _, e := range log.Entries {
		byMarta = byMarta || (e.Action == "charge.paid" && e.ActorLabel == "Marta G (Slack)")
	}
	if !byMarta {
		t.Fatalf("the change should be Marta's: %+v", log.Entries)
	}

	// A change from elsewhere (the web) reaches the channel message too.
	if _, err := w.svc.SetChargeStatus(w.ctx, w.owner, app.SetChargeStatusInput{ChargeID: shared, Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the channel message follows", func() bool {
		b := api.message("C777", ts)
		return strings.Contains(b, "Netflix renews in 3 days") && strings.Contains(b, "Changed by Alex")
	})

	if st, err := m.Configure(w.ctx, Config{Channel: new("")}); err != nil || st.Channel != "" {
		t.Fatalf("clearing the channel: %+v %v", st, err)
	}
}

// TestGuideCarriesTheManifest keeps the manifest in the guide equal to the
// one the web shows.
func TestGuideCarriesTheManifest(t *testing.T) {
	guide, err := os.ReadFile("../../../docs/guides/slack.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(guide), Manifest) {
		t.Fatal("docs/guides/slack.md must contain the manifest exactly as in manifest.go")
	}
}
