package slack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	slackapi "github.com/slack-go/slack"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Each team runs its own Slack app, created from Manifest. Its two tokens
// come from EXHALE_SLACK_BOT_TOKEN and EXHALE_SLACK_APP_TOKEN or, more
// usually, from the web (Settings → Slack). The Manager runs the adapter
// for the current tokens and swaps it when they change, without restarting
// the server. It also posts reminders of shared subscriptions to the team
// channel, when one is set.

// Keys in the settings table.
const (
	settingBotToken    = "slack_bot_token" //nolint:gosec // a key, not a credential
	settingAppToken    = "slack_app_token" //nolint:gosec // a key, not a credential
	settingChannel     = "slack_channel"   // id of the team channel
	settingChannelName = "slack_channel_name"
)

// ErrNotRunning is returned when there is no Slack app to send through.
var ErrNotRunning = errors.New("slack is not set up")

// Status describes the Slack app for the settings UI. It never carries a
// token.
type Status struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty" enum:"env,settings" doc:"env: set by EXHALE_SLACK_BOT_TOKEN and EXHALE_SLACK_APP_TOKEN and not editable in the web"`
	Running    bool   `json:"running" doc:"the socket to Slack is open"`
	Team       string `json:"team,omitempty" doc:"workspace name"`
	Bot        string `json:"bot,omitempty" doc:"the bot's name in the workspace"`
	AppURL     string `json:"appUrl,omitempty" doc:"link that opens the app in Slack"`
	Channel    string `json:"channel,omitempty" doc:"channel where reminders of shared subscriptions are also posted"`
	Error      string `json:"error,omitempty" doc:"why the app is not connected, when Slack says so"`
	Manifest   string `json:"manifest" doc:"app manifest (YAML) to create the Slack app from"`
	CreateURL  string `json:"createUrl" doc:"opens Slack's create-app page with the manifest filled in"`
}

// Config is what the owner sets from the web. Tokens are optional once the
// app is connected, so the channel can change on its own.
type Config struct {
	BotToken string  `json:"botToken,omitempty" doc:"Bot User OAuth Token, xoxb-…"`
	AppToken string  `json:"appToken,omitempty" doc:"app-level token with connections:write, xapp-…"`
	Channel  *string `json:"channel,omitempty" doc:"channel name or id for reminders of shared subscriptions; empty to stop posting there"`
}

// Deliverer is the scheduler's delivery step.
type Deliverer interface {
	Deliver(ctx context.Context, item app.OutboxItem) error
}

// Manager runs the Slack adapter. It implements actions.Channel and the
// platform service interface.
type Manager struct {
	svc    *app.Service
	log    *slog.Logger
	envBot string
	envApp string
	apiURL string

	mu      sync.RWMutex
	cur     *Adapter
	eng     *actions.Engine
	stopped string               // why the adapter stopped, if Slack rejected the tokens
	posted  map[string]time.Time // reminders already posted to the team channel
	edited  map[string]time.Time // channel messages just redrawn by a button press, by "ts|actor"
	restart chan struct{}
}

// NewManager returns a manager. Tokens from the environment (both, or they
// are ignored) win over tokens saved from the web; apiURL overrides the
// Slack API URL (tests).
func NewManager(envBot, envApp, apiURL string, svc *app.Service, log *slog.Logger) *Manager {
	m := &Manager{svc: svc, log: log, apiURL: apiURL, restart: make(chan struct{}, 1),
		posted: map[string]time.Time{}, edited: map[string]time.Time{}}
	envBot, envApp = strings.TrimSpace(envBot), strings.TrimSpace(envApp)
	switch {
	case envBot != "" && envApp != "":
		m.envBot, m.envApp = envBot, envApp
	case envBot != "" || envApp != "":
		log.Warn("slack: set both EXHALE_SLACK_BOT_TOKEN and EXHALE_SLACK_APP_TOKEN; ignoring the one that is set")
	}
	return m
}

// SetEngine wires the action layer.
func (m *Manager) SetEngine(e *actions.Engine) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eng = e
	if m.cur != nil {
		m.cur.SetEngine(e)
	}
}

func (m *Manager) Name() string   { return Name }
func (m *Manager) String() string { return Name }

func (m *Manager) adapter() (*Adapter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cur == nil {
		return nil, ErrNotRunning
	}
	return m.cur, nil
}

// Send posts through the running app.
func (m *Manager) Send(ctx context.Context, chatID string, msg actions.Message) (string, error) {
	a, err := m.adapter()
	if err != nil {
		return "", err
	}
	return a.Send(ctx, chatID, msg)
}

// Edit edits through the running app.
func (m *Manager) Edit(ctx context.Context, chatID, messageID string, msg actions.Message) error {
	a, err := m.adapter()
	if err != nil {
		return err
	}
	return a.Edit(ctx, chatID, messageID, msg)
}

func (m *Manager) setting(ctx context.Context, key string) string {
	v, err := m.svc.Store().Q.GetSetting(ctx, key)
	if err != nil {
		return ""
	}
	return v
}

func (m *Manager) put(ctx context.Context, key, value string) error {
	return m.svc.Store().Q.PutSetting(ctx, db.PutSettingParams{Key: key, Value: value})
}

func (m *Manager) tokens(ctx context.Context) (bot, appToken, source string) {
	if m.envBot != "" {
		return m.envBot, m.envApp, "env"
	}
	bot, appToken = m.setting(ctx, settingBotToken), m.setting(ctx, settingAppToken)
	if bot == "" || appToken == "" {
		return "", "", ""
	}
	return bot, appToken, "settings"
}

// Status reports whether an app is configured and connected.
func (m *Manager) Status(ctx context.Context) Status {
	bot, _, src := m.tokens(ctx)
	st := Status{Configured: bot != "", Source: src, Manifest: Manifest, CreateURL: CreateURL()}
	if !st.Configured {
		return st
	}
	st.Channel = m.setting(ctx, settingChannelName)
	m.mu.RLock()
	defer m.mu.RUnlock()
	st.Error = m.stopped
	if m.cur != nil {
		w := m.cur.Workspace()
		st.Running, st.Team, st.Bot = m.cur.Connected(), w.Team, w.Bot
		if w.AppID != "" && w.TeamID != "" {
			st.AppURL = "https://slack.com/app_redirect?app=" + w.AppID + "&team=" + w.TeamID
		}
		if !st.Running {
			st.Error = m.cur.LastError()
		}
	}
	return st
}

// Configure checks new tokens with Slack, saves them and switches the app
// to them; it can also set or clear the team channel. It returns the new
// status.
func (m *Manager) Configure(ctx context.Context, in Config) (Status, error) {
	bot, appToken := strings.TrimSpace(in.BotToken), strings.TrimSpace(in.AppToken)
	if bot == "" && appToken == "" && in.Channel == nil {
		return Status{}, fmt.Errorf("%w: paste the bot token and the app-level token", domain.ErrValidation)
	}
	if bot != "" || appToken != "" {
		if m.envBot != "" {
			return Status{}, fmt.Errorf("%w: the Slack app is set by EXHALE_SLACK_BOT_TOKEN and EXHALE_SLACK_APP_TOKEN on the server; change it there", domain.ErrConflict)
		}
		switch {
		case !strings.HasPrefix(bot, "xoxb-"):
			return Status{}, fmt.Errorf("%w: the bot token starts with xoxb-; it is the Bot User OAuth Token under OAuth & Permissions", domain.ErrValidation)
		case !strings.HasPrefix(appToken, "xapp-"):
			return Status{}, fmt.Errorf("%w: the app-level token starts with xapp-; create it under Basic Information → App-Level Tokens with the connections:write scope", domain.ErrValidation)
		}
		w, err := m.check(ctx, bot, appToken)
		if err != nil {
			return Status{}, err
		}
		if err := m.put(ctx, settingBotToken, bot); err != nil {
			return Status{}, err
		}
		if err := m.put(ctx, settingAppToken, appToken); err != nil {
			return Status{}, err
		}
		m.log.Info("slack app configured from the web", "team", w.Team, "bot", w.Bot)
		m.mu.RLock()
		old := m.cur
		m.mu.RUnlock()
		m.kick()
		m.waitRunning(ctx, old)
	}
	if in.Channel != nil {
		if err := m.setChannel(ctx, *in.Channel); err != nil {
			return Status{}, err
		}
	}
	return m.Status(ctx), nil
}

// Remove forgets tokens saved from the web and disconnects the app.
func (m *Manager) Remove(ctx context.Context) (Status, error) {
	if m.envBot != "" {
		return Status{}, fmt.Errorf("%w: the Slack app is set by EXHALE_SLACK_BOT_TOKEN and EXHALE_SLACK_APP_TOKEN on the server; remove it there", domain.ErrConflict)
	}
	for _, k := range []string{settingBotToken, settingAppToken, settingChannel, settingChannelName} {
		if err := m.put(ctx, k, ""); err != nil {
			return Status{}, err
		}
	}
	m.kick()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := m.adapter(); err != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return m.Status(ctx), nil
}

// rejected reports whether err is Slack saying no (as opposed to Slack
// being unreachable).
func rejected(err error) bool {
	_, ok := errors.AsType[slackapi.SlackErrorResponse](err)
	return ok
}

// check validates both tokens: auth.test for the bot token and
// apps.connections.open for the app-level one.
func (m *Manager) check(ctx context.Context, bot, appToken string) (Workspace, error) {
	api := client(bot, appToken, m.apiURL)
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	t, err := api.AuthTestContext(cctx)
	switch {
	case err != nil && rejected(err):
		return Workspace{}, fmt.Errorf("%w: Slack does not recognize the bot token (%s); copy the Bot User OAuth Token again from OAuth & Permissions", domain.ErrValidation, safe(err))
	case err != nil:
		return Workspace{}, fmt.Errorf("could not reach Slack to check the tokens: %s", safe(err))
	case t.BotID == "":
		return Workspace{}, fmt.Errorf("%w: that is not a bot token; use the Bot User OAuth Token (xoxb-…)", domain.ErrValidation)
	}
	if _, _, err := api.StartSocketModeContext(cctx); err != nil {
		if rejected(err) {
			return Workspace{}, fmt.Errorf("%w: Slack does not accept the app-level token (%s); it needs the connections:write scope and Socket Mode switched on", domain.ErrValidation, safe(err))
		}
		return Workspace{}, fmt.Errorf("could not reach Slack to check the app-level token: %s", safe(err))
	}
	return Workspace{Team: t.Team, TeamID: t.TeamID, TeamURL: t.URL, Bot: t.User, BotUserID: t.UserID}, nil
}

var channelID = regexp.MustCompile(`^[CG][A-Z0-9]{6,}$`)

// setChannel sets the team channel, proving the app can post there by
// saying so in the channel. An empty name clears it.
func (m *Manager) setChannel(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		if err := m.put(ctx, settingChannel, ""); err != nil {
			return err
		}
		return m.put(ctx, settingChannelName, "")
	}
	a, err := m.adapter()
	if err != nil {
		return fmt.Errorf("%w: connect the Slack app first", domain.ErrConflict)
	}
	target := name
	if !channelID.MatchString(name) {
		name = "#" + strings.TrimPrefix(name, "#")
		target = name
	}
	hello := actions.Message{Title: "🔔 Exhale posts renewals here", Lines: []string{
		"Reminders of shared subscriptions show up in this channel. Anyone who linked their Slack account in Exhale can act on them; private subscriptions stay private.",
	}}
	id, _, err := a.post(ctx, target, hello)
	switch {
	case err != nil && rejected(err):
		return fmt.Errorf("%w: Slack would not let the app post in %s (%s); for a private channel, invite the app there first with /invite", domain.ErrValidation, name, safe(err))
	case err != nil:
		return fmt.Errorf("could not reach Slack: %s", safe(err))
	}
	if err := m.put(ctx, settingChannel, id); err != nil {
		return err
	}
	return m.put(ctx, settingChannelName, name)
}

func (m *Manager) kick() {
	select {
	case m.restart <- struct{}{}:
	default:
	}
}

// waitRunning waits, for a few seconds, until an adapter other than old is
// connected: the one started for the tokens just saved.
func (m *Manager) waitRunning(ctx context.Context, old *Adapter) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		m.mu.RLock()
		cur := m.cur
		m.mu.RUnlock()
		if cur != nil && cur != old && cur.Connected() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// retryRejected is how long the manager waits before trying tokens Slack
// rejected again (an app can be reinstalled or re-enabled meanwhile).
var retryRejected = 5 * time.Minute

// Serve runs the app for the current tokens and restarts it when they
// change, until ctx ends.
func (m *Manager) Serve(ctx context.Context) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go m.syncChannel(ctx)
	for {
		bot, appToken, _ := m.tokens(ctx)
		if bot == "" {
			m.log.Info("slack is not set up: add your Slack app in the web (Settings → Slack) or set EXHALE_SLACK_BOT_TOKEN and EXHALE_SLACK_APP_TOKEN")
			select {
			case <-ctx.Done():
				return nil
			case <-m.restart:
				continue
			}
		}
		a := New(bot, appToken, m.apiURL, m.svc, m.log)
		a.pressed = m.touch
		m.mu.Lock()
		if m.eng != nil {
			a.SetEngine(m.eng)
		}
		m.cur, m.stopped = a, ""
		m.mu.Unlock()
		runCtx, cancel := context.WithCancel(ctx)
		var failed error // set before stopped is closed
		stopped := make(chan struct{})
		go func() { failed = a.Serve(runCtx); close(stopped) }()
		m.log.Info("slack enabled")
		select {
		case <-ctx.Done():
			cancel()
			<-stopped
			return nil
		case <-m.restart:
			cancel()
			<-stopped
		case <-stopped: // Slack rejected the tokens
			cancel()
		}
		m.mu.Lock()
		m.cur = nil
		if failed != nil {
			m.stopped = failed.Error()
		}
		m.mu.Unlock()
		if failed == nil {
			continue
		}
		m.log.Error("slack stopped: Slack rejected the tokens", "err", failed.Error())
		select {
		case <-ctx.Done():
			return nil
		case <-m.restart:
		case <-time.After(retryRejected):
		}
	}
}

// --- the team channel ----------------------------------------------------------

// Deliverer wraps the delivery step so that a reminder of a shared
// subscription is also posted once to the team channel. A failure there
// never fails the delivery: the scheduler would send the DMs again.
func (m *Manager) Deliverer(next Deliverer) Deliverer { return channelDeliverer{m, next} }

type channelDeliverer struct {
	m    *Manager
	next Deliverer
}

func (d channelDeliverer) Deliver(ctx context.Context, item app.OutboxItem) error {
	err := d.next.Deliver(ctx, item)
	d.m.share(ctx, item)
	return err
}

func recent(seen map[string]time.Time, key string, within time.Duration) bool {
	for k, at := range seen {
		if time.Since(at) > 48*time.Hour {
			delete(seen, k)
		}
	}
	at, ok := seen[key]
	return ok && time.Since(at) < within
}

// share posts the reminder to the team channel if its subscription is
// shared. Every member gets their own outbox item for the same reminder;
// the channel gets it once.
func (m *Manager) share(ctx context.Context, item app.OutboxItem) {
	channel := m.setting(ctx, settingChannel)
	a, err := m.adapter()
	if channel == "" || err != nil {
		return
	}
	m.mu.Lock()
	eng := m.eng
	key := item.Kind + "|" + item.ChargeID + "|" + item.DueAt.UTC().Format(time.RFC3339)
	dup := recent(m.posted, key, 48*time.Hour)
	m.mu.Unlock()
	if eng == nil || dup {
		return
	}
	actor, err := m.svc.ActorForMember(ctx, item.MemberID)
	if err != nil || !m.isShared(ctx, actor, item.ChargeID) {
		return
	}
	msg, err := eng.Render(ctx, actor, item.Kind, item.ChargeID)
	if err != nil {
		return
	}
	_, ts, err := a.post(ctx, channel, msg)
	if err != nil {
		m.log.Warn("slack: could not post the reminder to the team channel", "err", safe(err))
		return
	}
	m.mu.Lock()
	m.posted[key] = time.Now()
	m.mu.Unlock()
	// No member: the action layer leaves this message alone and syncChannel
	// keeps it current for everyone.
	if err := m.svc.Store().Q.SaveMessage(ctx, db.SaveMessageParams{ID: domain.NewID("msg"), Channel: Name, ChatID: channel, MessageID: ts,
		ChargeID: store.Ptr(item.ChargeID), CreatedAt: store.FormatTime(time.Now())}); err != nil {
		m.log.Warn("slack: could not record the channel message", "err", err)
	}
}

// isShared reports whether the charge belongs to a subscription the whole
// household sees. Private subscriptions never reach the channel.
func (m *Manager) isShared(ctx context.Context, actor app.Actor, chargeID string) bool {
	q := m.svc.Store().Q
	c, err := q.GetCharge(ctx, chargeID)
	if err != nil {
		return false
	}
	s, err := q.GetSubscription(ctx, db.GetSubscriptionParams{ID: c.SubscriptionID, HouseholdID: actor.HouseholdID})
	return err == nil && s.Visibility == string(domain.VisibilityShared)
}

// touch notes that a button press by actor just redrew a channel message,
// so the sync does not redraw it for that same change, without its Undo.
func (m *Manager) touch(ts, actor string) {
	m.mu.Lock()
	m.edited[ts+"|"+actor] = time.Now()
	m.mu.Unlock()
}

// syncChannel re-edits the reminders posted to the team channel whenever
// their charge changes, from any source (the action layer does the same
// for each member's own messages).
func (m *Manager) syncChannel(ctx context.Context) {
	events, stop := m.svc.Bus().Subscribe(128)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type == bus.ChargeUpdated && ev.ChargeID != "" {
				m.refreshChannel(ctx, ev)
			}
		}
	}
}

func (m *Manager) refreshChannel(ctx context.Context, ev bus.Event) {
	rows, err := m.svc.Store().Q.ListMessagesForCharge(ctx, store.Ptr(ev.ChargeID))
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.Channel != Name || row.MemberID != nil {
			continue
		}
		a, err := m.adapter()
		m.mu.Lock()
		eng := m.eng
		pressed := recent(m.edited, row.MessageID+"|"+ev.ActorLabel, 3*time.Second)
		m.mu.Unlock()
		if err != nil || eng == nil || pressed {
			continue
		}
		actor, err := m.svc.Owner(ctx)
		if err != nil {
			return
		}
		msg, err := eng.Render(ctx, actor, "", ev.ChargeID)
		if err != nil {
			continue
		}
		if ev.ActorLabel != "" {
			msg.Footer = "Changed by " + ev.ActorLabel
		}
		if err := a.Edit(ctx, row.ChatID, row.MessageID, msg); err != nil {
			m.log.Debug("slack: could not re-edit the channel message", "err", safe(err))
		}
	}
}
