package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/channels/slack"
	"github.com/akamachinecode/exhale/internal/domain"
)

// fakeSlack stands in for the Slack manager: it remembers what it was
// given and answers like a connected app.
type fakeSlack struct{ got slack.Config }

func (f *fakeSlack) Status(context.Context) slack.Status {
	st := slack.Status{Manifest: slack.Manifest, CreateURL: slack.CreateURL()}
	if f.got.BotToken != "" {
		st.Configured, st.Running, st.Source, st.Team, st.Bot = true, true, "settings", "Acme", "exhale"
		st.AppURL = "https://slack.com/app_redirect?app=A1&team=T1"
	}
	if f.got.Channel != nil {
		st.Channel = *f.got.Channel
	}
	return st
}

func (f *fakeSlack) Configure(ctx context.Context, in slack.Config) (slack.Status, error) {
	if in.BotToken == "xoxb-revoked" {
		return slack.Status{}, fmt.Errorf("%w: Slack does not recognize the bot token", domain.ErrValidation)
	}
	if in.BotToken == "" { // the channel alone: keep the tokens
		in.BotToken, in.AppToken = f.got.BotToken, f.got.AppToken
	}
	f.got = in
	return f.Status(ctx), nil
}

func (f *fakeSlack) Remove(ctx context.Context) (slack.Status, error) {
	f.got = slack.Config{}
	return f.Status(ctx), nil
}

// TestSlackSettings: everyone signed in sees the status and the manifest;
// only an owner sets the tokens, which never come back; each member gets
// their own link code.
func TestSlackSettings(t *testing.T) {
	_, svc := freshServer(t)
	ctl := &fakeSlack{}
	h, _, err := New(svc, Config{Version: "test", Slack: ctl})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	if code, _ := newBrowser(t, srv.URL).do("GET", "/api/v1/settings/slack", nil); code != 401 {
		t.Fatalf("the status needs a session, got %d", code)
	}
	owner := newBrowser(t, srv.URL)
	owner.do("POST", "/api/v1/auth/setup", alex)
	_, inv := owner.do("POST", "/api/v1/auth/invites", map[string]string{"role": "member"})
	member := newBrowser(t, srv.URL)
	member.do("POST", "/api/v1/auth/join", map[string]string{"code": inv["code"].(string), "name": "Marta", "email": "marta@example.com", "password": "another long password"})

	code, st := member.do("GET", "/api/v1/settings/slack", nil)
	if code != 200 || st["configured"] != false || !strings.Contains(st["manifest"].(string), "socket_mode_enabled: true") ||
		!strings.HasPrefix(st["createUrl"].(string), "https://api.slack.com/apps?new_app=1&manifest_yaml=") {
		t.Fatalf("a member should see the status and the manifest: %d %v", code, st)
	}
	tokens := map[string]string{"botToken": "xoxb-secret-bot", "appToken": "xapp-secret-app"}
	if code, _ := member.do("POST", "/api/v1/settings/slack", tokens); code != 403 {
		t.Fatalf("members cannot set the Slack app, got %d", code)
	}
	if code, _ := member.do("POST", "/api/v1/settings/slack/remove", nil); code != 403 {
		t.Fatalf("members cannot remove the Slack app, got %d", code)
	}
	if ctl.got.BotToken != "" {
		t.Fatal("a member's tokens reached the manager")
	}
	if code, problem := owner.do("POST", "/api/v1/settings/slack", map[string]string{"botToken": "xoxb-revoked", "appToken": "xapp-x"}); code != 422 || !strings.Contains(fmt.Sprint(problem["detail"]), "bot token") {
		t.Fatalf("a rejected token should read as a validation problem: %d %v", code, problem)
	}

	code, st = owner.do("POST", "/api/v1/settings/slack", tokens)
	if code != 200 || st["running"] != true || st["team"] != "Acme" || st["bot"] != "exhale" {
		t.Fatalf("set: %d %v", code, st)
	}
	if ctl.got.BotToken != "xoxb-secret-bot" || ctl.got.AppToken != "xapp-secret-app" || ctl.got.Channel != nil {
		t.Fatalf("the manager got %+v", ctl.got)
	}
	_, again := owner.do("GET", "/api/v1/settings/slack", nil)
	for _, body := range []map[string]any{st, again} {
		if b, _ := json.Marshal(body); strings.Contains(string(b), "secret") || strings.Contains(string(b), "Token") {
			t.Fatalf("tokens must never come back: %s", b)
		}
	}
	if code, st = owner.do("POST", "/api/v1/settings/slack", map[string]string{"channel": "#subscriptions"}); code != 200 || st["channel"] != "#subscriptions" {
		t.Fatalf("channel: %d %v", code, st)
	}

	code, link := member.do("POST", "/api/v1/auth/links/slack", nil)
	if code != 200 || link["command"] != "/exhale link "+link["code"].(string) || link["url"] != "https://slack.com/app_redirect?app=A1&team=T1" {
		t.Fatalf("link code: %d %v", code, link)
	}
	ctx := context.Background()
	if _, err := auth.RedeemLinkCode(ctx, svc.Store(), "telegram", link["code"].(string), "42"); err == nil {
		t.Fatal("a Slack code must not link a Telegram chat")
	}
	if _, err := auth.RedeemLinkCode(ctx, svc.Store(), slack.Name, link["code"].(string), "U2"); err != nil {
		t.Fatalf("the code should link the Slack account: %v", err)
	}
	if actor, err := auth.MemberForChannel(ctx, svc.Store(), slack.Name, "U2"); err != nil || actor.Label != "Marta" {
		t.Fatalf("U2 should be Marta: %+v %v", actor, err)
	}
	if _, links := member.do("GET", "/api/v1/auth/links", nil); len(links["links"].([]any)) != 1 || links["links"].([]any)[0].(map[string]any)["channel"] != "slack" {
		t.Fatalf("the link should be listed: %v", links)
	}

	if code, st = owner.do("POST", "/api/v1/settings/slack/remove", nil); code != 200 || st["configured"] != false {
		t.Fatalf("remove: %d %v", code, st)
	}
}
