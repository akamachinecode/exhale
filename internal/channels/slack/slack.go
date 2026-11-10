// Package slack (piece ③) connects Exhale to a Slack workspace over Socket
// Mode: a WebSocket the app opens outward, so no public URL or open port is
// needed (ADR 0001, 0007). Each team runs its own Slack app, created from
// the manifest in manifest.go.
//
// Like the Telegram adapter it only renders action-layer messages (as Block
// Kit) and passes button presses and typed answers back. The app inside a
// message (ADR 0019) lives in the direct message with the bot and, as a
// permanent surface, in the app's Home tab.
//
// For the action layer a Slack chat is always a person: ChatRef.ChatID is
// the Slack user id, whatever the button was pressed in (the DM, the Home
// tab or a shared channel). Questions that need a typed answer therefore
// always continue in the DM.
package slack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	slackapi "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/domain"
)

// Name is the channel name stored in channel links.
const Name = "slack"

// homeID is the message id of the Home tab, a permanent app surface.
const homeID = actions.SurfacePrefix + "home"

const (
	textNotLinked = "This Slack account is not linked to Exhale yet. In Exhale, open Settings → Slack, press “Link Slack” and send me the code it shows."
	textBadCode   = "That code is not valid anymore: codes work once and for 10 minutes. In Exhale, open Settings → Slack and press “Link Slack” again."
	textLinked    = "✅ Linked. Reminders arrive here, and the message below is Exhale itself: tap around. The Home tab has it too."
	textFailed    = "Something went wrong. Try again in a moment."
)

// Workspace describes the workspace and the bot the tokens belong to.
type Workspace struct {
	Team      string
	TeamID    string
	TeamURL   string
	Bot       string
	BotUserID string
	AppID     string
}

// Adapter is the Slack channel for one pair of tokens.
type Adapter struct {
	api  *slackapi.Client
	sock *socketmode.Client
	svc  *app.Service
	log  *slog.Logger
	eng  atomic.Pointer[actions.Engine]

	connected atomic.Bool
	lastErr   atomic.Value // string
	workspace atomic.Pointer[Workspace]

	mu   sync.Mutex
	dms  map[string]string          // Slack user id → DM channel id
	home map[string]actions.Message // last Home view per user
	told map[string]time.Time       // unlinked users already told how to link
	work chan func(context.Context)
	// pressed, if set, hears that a button press by actor redrew a channel
	// message.
	pressed func(ts, actor string)
}

type quietLog struct{}

func (quietLog) Output(int, string) error { return nil }

func client(botToken, appToken, apiURL string) *slackapi.Client {
	opts := []slackapi.Option{
		slackapi.OptionAppLevelToken(appToken),
		slackapi.OptionHTTPClient(&http.Client{Timeout: 30 * time.Second}),
		slackapi.OptionLog(quietLog{}),
	}
	if apiURL != "" {
		opts = append(opts, slackapi.OptionAPIURL(strings.TrimRight(apiURL, "/")+"/"))
	}
	return slackapi.New(botToken, opts...)
}

// New creates the adapter. apiURL overrides the Web API URL (tests).
// SetEngine must be called before Serve.
func New(botToken, appToken, apiURL string, svc *app.Service, log *slog.Logger) *Adapter {
	api := client(botToken, appToken, apiURL)
	return &Adapter{
		api: api, sock: socketmode.New(api, socketmode.OptionLog(quietLog{})), svc: svc, log: log,
		dms: map[string]string{}, home: map[string]actions.Message{}, told: map[string]time.Time{},
		work: make(chan func(context.Context), 256),
	}
}

// SetEngine wires the action layer (which itself needs the adapter).
func (a *Adapter) SetEngine(e *actions.Engine) { a.eng.Store(e) }

func (a *Adapter) Name() string   { return Name }
func (a *Adapter) String() string { return Name }

// Connected reports whether the socket to Slack is up.
func (a *Adapter) Connected() bool { return a.connected.Load() }

// LastError is why the socket is down ("" when it is up or still trying).
func (a *Adapter) LastError() string { v, _ := a.lastErr.Load().(string); return v }

// Workspace returns what is known about the workspace and the bot.
func (a *Adapter) Workspace() Workspace {
	if w := a.workspace.Load(); w != nil {
		return *w
	}
	return Workspace{}
}

var secrets = regexp.MustCompile(`wss?://\S+|xox[a-z]-[\w-]+|xapp-[\w-]+`)

// safe removes anything secret from an error before it is logged or shown:
// Socket Mode URLs carry a ticket, and tokens must never reach a log.
func safe(err error) string {
	if err == nil {
		return ""
	}
	return secrets.ReplaceAllString(err.Error(), "[redacted]")
}

// Serve keeps the socket open until ctx is cancelled. The library
// reconnects when the socket drops or Slack asks for a refresh; Serve
// returns an error only when Slack rejects the tokens.
func (a *Adapter) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.identify(ctx)
	done := make(chan error, 1)
	go func() { done <- a.sock.RunContext(ctx) }()
	go a.worker(ctx)
	for {
		select {
		case <-ctx.Done():
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				a.log.Debug("slack: socket closed", "err", safe(err))
			}
			a.connected.Store(false)
			return nil
		case err := <-done:
			a.connected.Store(false)
			if ctx.Err() != nil {
				return nil
			}
			a.lastErr.Store(safe(err))
			return fmt.Errorf("slack socket: %s", safe(err))
		case ev := <-a.sock.Events:
			a.dispatch(ctx, ev)
		}
	}
}

// identify reads the workspace and bot names (auth.test).
func (a *Adapter) identify(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	t, err := a.api.AuthTestContext(cctx)
	if err != nil {
		a.log.Warn("slack: could not read the workspace", "err", safe(err))
		return
	}
	w := a.Workspace()
	w.Team, w.TeamID, w.TeamURL, w.Bot, w.BotUserID = t.Team, t.TeamID, t.URL, t.User, t.UserID
	a.workspace.Store(&w)
}

// dispatch acknowledges an envelope right away (Slack waits 3 seconds) and
// queues the work behind it, so answers keep the order they were typed in.
func (a *Adapter) dispatch(ctx context.Context, ev socketmode.Event) {
	if ev.Request != nil && ev.Request.EnvelopeID != "" {
		if err := a.sock.AckCtx(ctx, ev.Request.EnvelopeID, nil); err != nil {
			a.log.Warn("slack: could not acknowledge", "err", safe(err))
		}
	}
	switch ev.Type {
	case socketmode.EventTypeConnecting:
		a.connected.Store(false)
	case socketmode.EventTypeConnectionError:
		a.connected.Store(false)
		if e, ok := ev.Data.(*slackapi.ConnectionErrorEvent); ok {
			a.lastErr.Store(safe(e.ErrorObj))
			a.log.Warn("slack: could not connect, retrying", "attempt", e.Attempt, "err", safe(e.ErrorObj))
		}
	case socketmode.EventTypeInvalidAuth:
		a.lastErr.Store("invalid_auth")
	case socketmode.EventTypeConnected:
		a.connected.Store(true)
		a.lastErr.Store("")
	case socketmode.EventTypeHello:
		a.connected.Store(true)
		a.lastErr.Store("")
		if ev.Request != nil && ev.Request.ConnectionInfo.AppID != "" {
			w := a.Workspace()
			w.AppID = ev.Request.ConnectionInfo.AppID
			a.workspace.Store(&w)
		}
	case socketmode.EventTypeEventsAPI:
		outer, ok := ev.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		switch e := outer.InnerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			a.enqueue(func(ctx context.Context) { a.handleMessage(ctx, e) })
		case *slackevents.AppHomeOpenedEvent:
			a.enqueue(func(ctx context.Context) { a.handleHomeOpened(ctx, e) })
		}
	case socketmode.EventTypeInteractive:
		if cb, ok := ev.Data.(slackapi.InteractionCallback); ok && cb.Type == slackapi.InteractionTypeBlockActions {
			a.enqueue(func(ctx context.Context) { a.handleAction(ctx, cb) })
		}
	case socketmode.EventTypeSlashCommand:
		if cmd, ok := ev.Data.(slackapi.SlashCommand); ok {
			a.enqueue(func(ctx context.Context) { a.handleSlash(ctx, cmd) })
		}
	case socketmode.EventTypeDisconnect, socketmode.EventTypeIncomingError, socketmode.EventTypeErrorWriteFailed, socketmode.EventTypeErrorBadMessage:
		// the library reconnects or skips the frame
	}
}

func (a *Adapter) enqueue(fn func(context.Context)) {
	select {
	case a.work <- fn:
	default:
		a.log.Warn("slack: too busy, an event was dropped")
	}
}

func (a *Adapter) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case fn := <-a.work:
			a.run(ctx, fn)
		}
	}
}

func (a *Adapter) run(ctx context.Context, fn func(context.Context)) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			a.log.Error("slack: handler panicked", "panic", fmt.Sprint(r))
		}
	}()
	fn(ctx)
}

// --- sending -------------------------------------------------------------------

func isUser(id string) bool { return strings.HasPrefix(id, "U") || strings.HasPrefix(id, "W") }

func (a *Adapter) remember(user, channel string) {
	if user == "" || !strings.HasPrefix(channel, "D") {
		return
	}
	a.mu.Lock()
	a.dms[user] = channel
	a.mu.Unlock()
}

// dm returns the direct-message channel with a user, opening it if needed.
func (a *Adapter) dm(ctx context.Context, user string) (string, error) {
	a.mu.Lock()
	ch := a.dms[user]
	a.mu.Unlock()
	if ch != "" {
		return ch, nil
	}
	c, _, _, err := a.api.OpenConversationContext(ctx, &slackapi.OpenConversationParameters{Users: []string{user}})
	if err != nil {
		return "", fmt.Errorf("conversations.open: %w", err)
	}
	a.remember(user, c.ID)
	return c.ID, nil
}

// conversation maps a chat id of the action layer to a Slack conversation:
// a user id means the DM with that user, anything else is a channel.
func (a *Adapter) conversation(ctx context.Context, chatID string) (string, error) {
	if isUser(chatID) {
		return a.dm(ctx, chatID)
	}
	return chatID, nil
}

// post sends a message and returns the conversation it landed in and its ts.
func (a *Adapter) post(ctx context.Context, chatID string, m actions.Message) (channel, ts string, err error) {
	ch, err := a.conversation(ctx, chatID)
	if err != nil {
		return "", "", err
	}
	return a.api.PostMessageContext(ctx, ch, messageOptions(m)...)
}

// Send posts a message and returns its id (Slack's ts).
func (a *Adapter) Send(ctx context.Context, chatID string, m actions.Message) (string, error) {
	_, ts, err := a.post(ctx, chatID, m)
	return ts, err
}

// Edit replaces a message in place. The Home tab is edited by publishing
// its view again.
func (a *Adapter) Edit(ctx context.Context, chatID, messageID string, m actions.Message) error {
	if messageID == homeID {
		return a.publishHome(ctx, chatID, m)
	}
	ch, err := a.conversation(ctx, chatID)
	if err != nil {
		return err
	}
	_, _, _, err = a.api.UpdateMessageContext(ctx, ch, messageID, messageOptions(m)...)
	return err
}

func (a *Adapter) publishHome(ctx context.Context, user string, m actions.Message) error {
	if _, err := a.api.PublishViewContext(ctx, slackapi.PublishViewContextRequest{UserID: user, View: homeView(m)}); err != nil {
		return fmt.Errorf("views.publish: %w", err)
	}
	a.mu.Lock()
	a.home[user] = m
	a.mu.Unlock()
	return nil
}

// say posts plain text to the DM with a user.
func (a *Adapter) say(ctx context.Context, user, text string) {
	ch, err := a.dm(ctx, user)
	if err == nil {
		_, _, err = a.api.PostMessageContext(ctx, ch, slackapi.MsgOptionText(escape(text), false), slackapi.MsgOptionDisableLinkUnfurl())
	}
	if err != nil {
		a.log.Warn("slack: reply failed", "err", safe(err))
	}
}

// whisper shows text only to the person who acted: through the response URL
// of their action when there is one (it works in any conversation), else as
// an ephemeral message.
func (a *Adapter) whisper(ctx context.Context, t target, text string) {
	var err error
	switch {
	case t.responseURL != "":
		_, _, err = a.api.PostMessageContext(ctx, t.channel, slackapi.MsgOptionResponseURL(t.responseURL, slackapi.ResponseTypeEphemeral), slackapi.MsgOptionText(escape(text), false))
	case t.channel != "":
		_, err = a.api.PostEphemeralContext(ctx, t.channel, t.user, slackapi.MsgOptionText(escape(text), false))
	default:
		a.say(ctx, t.user, text)
	}
	if err != nil {
		a.log.Warn("slack: ephemeral reply failed", "err", safe(err))
	}
}

// target is where a reply goes: the person, and the message or Home tab
// their button belongs to.
type target struct {
	user        string
	channel     string // conversation of the pressed message ("" for typed text and Home)
	ts          string // the pressed message
	responseURL string
	home        bool
	actor       string // label of the member who pressed
}

func (t target) direct() bool { return t.channel == "" || strings.HasPrefix(t.channel, "D") }

// apply shows a reply. Slack has no toast: a toast is whispered when it is
// the only feedback, and left out when the screen itself changed.
func (a *Adapter) apply(ctx context.Context, t target, rep actions.Reply) {
	if rep.Edit != nil {
		var err error
		switch {
		case t.home:
			err = a.publishHome(ctx, t.user, *rep.Edit)
		case t.ts != "":
			if !t.direct() && a.pressed != nil {
				a.pressed(t.ts, t.actor)
			}
			_, _, _, err = a.api.UpdateMessageContext(ctx, t.channel, t.ts, messageOptions(*rep.Edit)...)
		}
		if err != nil {
			a.log.Warn("slack: edit failed", "err", safe(err))
		}
	}
	if rep.Send != nil {
		if _, _, err := a.post(ctx, t.user, *rep.Send); err != nil {
			a.log.Warn("slack: send failed", "err", safe(err))
		}
	}
	switch {
	case t.home && rep.Edit == nil && (rep.Toast != "" || rep.Send != nil):
		note := rep.Toast
		if rep.Send != nil {
			note = strings.TrimSpace(note + " — continue in the Messages tab.")
		}
		a.homeNote(ctx, t.user, note)
	case rep.Toast == "":
	case rep.Edit == nil && rep.Send == nil:
		a.whisper(ctx, t, rep.Toast)
	case rep.Send != nil && !t.direct():
		a.whisper(ctx, t, rep.Toast+" — I sent you a direct message.")
	}
}

// homeNote shows a short notice at the foot of the Home tab.
func (a *Adapter) homeNote(ctx context.Context, user, note string) {
	a.mu.Lock()
	m, ok := a.home[user]
	a.mu.Unlock()
	if !ok {
		a.say(ctx, user, note)
		return
	}
	m.Footer = note
	if _, err := a.api.PublishViewContext(ctx, slackapi.PublishViewContextRequest{UserID: user, View: homeView(m)}); err != nil {
		a.log.Warn("slack: could not update the Home tab", "err", safe(err))
	}
}

// --- receiving -----------------------------------------------------------------

func (a *Adapter) member(ctx context.Context, user string) (app.Actor, error) {
	actor, err := auth.MemberForChannel(ctx, a.svc.Store(), Name, user)
	if err != nil {
		return actor, err
	}
	actor.Label += " (Slack)"
	return actor, nil
}

func chatOf(user string) actions.ChatRef { return actions.ChatRef{Channel: Name, ChatID: user} }

// handleMessage handles text typed in the DM with the bot.
func (a *Adapter) handleMessage(ctx context.Context, e *slackevents.MessageEvent) {
	if e.ChannelType != "im" || e.SubType != "" || e.BotID != "" || e.User == "" || e.User == a.Workspace().BotUserID {
		return // only what a person typed: not our own messages nor edits
	}
	a.remember(e.User, e.Channel)
	text := unwrap(e.Text)
	if text == "" {
		return
	}
	user := e.User
	reply := func(s string) { a.say(ctx, user, s) }
	word, rest := command(text)
	switch {
	case word == "link" && codePattern.MatchString(rest):
		a.link(ctx, user, rest, reply)
		return
	case word == "join" && codePattern.MatchString(rest):
		a.join(ctx, user, "", rest, reply)
		return
	}
	actor, err := a.member(ctx, user)
	if err != nil {
		if codePattern.MatchString(text) {
			a.link(ctx, user, text, reply)
			return
		}
		a.explain(ctx, user) // unlinked chats get nothing else (ADR 0006)
		return
	}
	eng := a.eng.Load()
	if rep, ok, err := eng.HandleText(ctx, actor, chatOf(user), text); ok {
		if err != nil {
			a.log.Error("slack text", "err", safe(err))
			reply(textFailed)
			return
		}
		a.apply(ctx, target{user: user}, rep)
		return
	}
	if !a.open(ctx, actor, user, word) {
		reply(help())
	}
}

// command splits "Subs" or "/link abc" into a lowercase command word and
// the rest.
func command(text string) (word, rest string) {
	word, rest, _ = strings.Cut(strings.TrimSpace(text), " ")
	return strings.ToLower(strings.TrimPrefix(word, "/")), strings.TrimSpace(rest)
}

// open runs a command word: a flow such as add, or an app screen. It
// reports false for anything else.
func (a *Adapter) open(ctx context.Context, actor app.Actor, user, word string) bool {
	if word == "" {
		return false
	}
	eng := a.eng.Load()
	if rep, ok := eng.Command(ctx, actor, chatOf(user), "/"+word); ok {
		a.apply(ctx, target{user: user}, rep)
		return true
	}
	if r, ok := actions.CommandRoute("/" + word); ok {
		a.openApp(ctx, actor, user, r)
		return true
	}
	return false
}

func help() string {
	var b strings.Builder
	b.WriteString("Type one of these, here or after /exhale anywhere:")
	for _, c := range actions.Commands {
		b.WriteString("\n• " + c.Command + " — " + c.Description)
	}
	return b.String()
}

// explain tells an unlinked person how to link, at most once an hour.
func (a *Adapter) explain(ctx context.Context, user string) {
	a.mu.Lock()
	last := a.told[user]
	if len(a.told) > 1000 {
		a.told = map[string]time.Time{}
	}
	fresh := time.Since(last) > time.Hour
	if fresh {
		a.told[user] = time.Now()
	}
	a.mu.Unlock()
	if fresh {
		a.say(ctx, user, textNotLinked)
	}
}

// link redeems a one-time code from the web and opens the app.
func (a *Adapter) link(ctx context.Context, user, code string, reply func(string)) {
	if _, err := auth.RedeemLinkCode(ctx, a.svc.Store(), Name, code, user); err != nil {
		reply(textBadCode)
		return
	}
	reply(textLinked)
	a.welcome(ctx, user)
}

// join accepts an invite: the person becomes a member with this Slack
// account linked, without needing to reach the web.
func (a *Adapter) join(ctx context.Context, user, name, code string, reply func(string)) {
	if u, err := a.api.GetUserInfoContext(ctx, user); err == nil && u != nil {
		if n := strings.TrimSpace(u.RealName); n != "" {
			name = n
		} else if u.Name != "" {
			name = u.Name
		}
	}
	actor, team, err := auth.JoinFromChat(ctx, a.svc.Store(), code, Name, user, name)
	switch {
	case errors.Is(err, domain.ErrConflict):
		reply("This Slack account is already linked to Exhale.")
		return
	case err != nil:
		reply("This invite has expired or was already used. Ask for a new one.")
		return
	}
	reply(fmt.Sprintf("👋 Welcome to %s, %s. Your reminders arrive in our direct messages, and Exhale itself is there and in the Home tab.", team, actor.Label))
	a.welcome(ctx, user)
}

func (a *Adapter) welcome(ctx context.Context, user string) {
	actor, err := a.member(ctx, user)
	if err != nil {
		return
	}
	a.openApp(ctx, actor, user, actions.RouteHome)
	a.showHome(ctx, actor, user)
}

// openApp posts a new app message in the DM (ADR 0019), pins it and unpins
// the one it replaces.
func (a *Adapter) openApp(ctx context.Context, actor app.Actor, user, route string) {
	eng := a.eng.Load()
	m, err := eng.OpenApp(ctx, actor, route)
	if err != nil {
		a.log.Error("slack app", "route", route, "err", safe(err))
		a.say(ctx, user, "Something went wrong opening Exhale.")
		return
	}
	ch, ts, err := a.post(ctx, user, m)
	if err != nil {
		a.log.Warn("slack: send failed", "err", safe(err))
		return
	}
	old, err := eng.AppSent(ctx, actor, chatOf(user), ts, route)
	if err != nil {
		a.log.Warn("slack: could not record the app message", "err", safe(err))
	}
	for _, o := range old {
		_ = a.api.RemovePinContext(ctx, ch, slackapi.NewRefToMessage(ch, o))
	}
	if err := a.api.AddPinContext(ctx, ch, slackapi.NewRefToMessage(ch, ts)); err != nil {
		a.log.Debug("slack: could not pin the app message", "err", safe(err))
	}
}

// handleHomeOpened draws the Home tab when someone opens it.
func (a *Adapter) handleHomeOpened(ctx context.Context, e *slackevents.AppHomeOpenedEvent) {
	if e.Tab != "home" {
		a.remember(e.User, e.Channel)
		return
	}
	actor, err := a.member(ctx, e.User)
	if err != nil {
		a.homeUnlinked(ctx, e.User)
		return
	}
	a.showHome(ctx, actor, e.User)
}

// showHome publishes the home screen in the Home tab and registers it as an
// app surface, so the action layer keeps it live like the app message.
func (a *Adapter) showHome(ctx context.Context, actor app.Actor, user string) {
	eng := a.eng.Load()
	m, err := eng.OpenApp(ctx, actor, actions.RouteHome)
	if err != nil {
		a.log.Error("slack home", "err", safe(err))
		return
	}
	if err := a.publishHome(ctx, user, m); err != nil {
		a.log.Warn("slack: could not publish the Home tab", "err", safe(err))
		return
	}
	if err := eng.AppSurface(ctx, actor, chatOf(user), homeID, actions.RouteHome); err != nil {
		a.log.Warn("slack: could not record the Home tab", "err", safe(err))
	}
}

func (a *Adapter) homeUnlinked(ctx context.Context, user string) {
	m := actions.Message{Title: "Exhale", Lines: []string{textNotLinked, "You can also type: /exhale link CODE"}}
	if _, err := a.api.PublishViewContext(ctx, slackapi.PublishViewContextRequest{UserID: user, View: homeView(m)}); err != nil {
		a.log.Warn("slack: could not publish the Home tab", "err", safe(err))
	}
}

// handleAction runs a button, pressed in the DM, the Home tab or a shared
// channel, as the person who pressed it.
func (a *Adapter) handleAction(ctx context.Context, cb slackapi.InteractionCallback) {
	if len(cb.ActionCallback.BlockActions) == 0 {
		return
	}
	act := cb.ActionCallback.BlockActions[0]
	if !strings.HasPrefix(act.ActionID, actionPrefix) || act.Value == "" {
		return // a link button: Slack opened the page
	}
	t := target{user: cb.User.ID, responseURL: cb.ResponseURL}
	chat := chatOf(t.user)
	if cb.Container.Type == "view" || (cb.Container.Type == "" && cb.View.ID != "") {
		t.home, chat.MessageID = true, homeID
	} else {
		t.channel = cmpOr(cb.Container.ChannelID, cb.Channel.ID)
		t.ts = cmpOr(cb.Container.MessageTs, cb.Message.Timestamp)
		chat.MessageID = t.ts
		a.remember(t.user, t.channel)
	}
	actor, err := a.member(ctx, t.user)
	if err != nil {
		if t.home {
			a.homeUnlinked(ctx, t.user)
		} else {
			a.whisper(ctx, t, "Link your account first. "+textNotLinked)
		}
		return
	}
	t.actor = actor.Label
	rep, err := a.eng.Load().Handle(ctx, actor, chat, act.Value)
	if err != nil {
		a.log.Error("slack button", "data", act.Value, "err", safe(err))
		rep = actions.Reply{Toast: textFailed}
	}
	a.apply(ctx, t, rep)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// handleSlash runs /exhale [screen | add | link CODE | join CODE].
func (a *Adapter) handleSlash(ctx context.Context, cmd slackapi.SlashCommand) {
	user := cmd.UserID
	t := target{user: user, channel: cmd.ChannelID, responseURL: cmd.ResponseURL}
	reply := func(s string) { a.whisper(ctx, t, s) }
	word, rest := command(unwrap(cmd.Text))
	switch word {
	case "link":
		a.link(ctx, user, rest, reply)
		return
	case "join":
		a.join(ctx, user, cmd.UserName, rest, reply)
		return
	}
	actor, err := a.member(ctx, user)
	if err != nil {
		reply(textNotLinked)
		return
	}
	if word == "" {
		word = "home"
	}
	// A command leaves a pending question unanswered, as in Telegram.
	_, _, _ = a.eng.Load().HandleText(ctx, actor, chatOf(user), "/"+word)
	if !a.open(ctx, actor, user, word) {
		reply(help())
		return
	}
	// Typed anywhere but in the DM with the bot, say where Exhale opened.
	if ch, err := a.dm(ctx, user); err == nil && ch != cmd.ChannelID {
		reply("Exhale is open in our direct messages.")
	}
}
