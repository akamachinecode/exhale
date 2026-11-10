// Package telegram (piece ②) connects Exhale to a Telegram bot over long
// polling: an outbound connection, so no public URL or open port is needed
// (ADR 0001, 0007). It only renders action-layer messages as inline
// keyboards and passes button presses back. Its home message is the app
// itself: one pinned message that navigates by editing in place (ADR 0019).
package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/domain"
)

// Name is the channel name stored in channel links.
const Name = "telegram"

// Adapter is the Telegram channel.
type Adapter struct {
	username atomic.Value // string, from getMe
	b        *bot.Bot
	svc      *app.Service
	eng      *actions.Engine
	log      *slog.Logger
}

// New creates the adapter. serverURL overrides the Bot API URL (tests).
// SetEngine must be called before Serve.
func New(token, serverURL string, svc *app.Service, log *slog.Logger) (*Adapter, error) {
	a := &Adapter{svc: svc, log: log}
	opts := []bot.Option{bot.WithDefaultHandler(a.handle), bot.WithSkipGetMe(), bot.WithNotAsyncHandlers(),
		bot.WithErrorsHandler(func(err error) { log.Warn("telegram", "err", err) })}
	if serverURL != "" {
		opts = append(opts, bot.WithServerURL(serverURL))
	}
	b, err := bot.New(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}
	a.b = b
	return a, nil
}

// SetEngine wires the action layer (which itself needs the adapter).
func (a *Adapter) SetEngine(e *actions.Engine) { a.eng = e }

func (a *Adapter) Name() string { return Name }

// Username is the bot's @username without the @ ("" until known), used for
// t.me deep links.
func (a *Adapter) Username() string { v, _ := a.username.Load().(string); return v }

func (a *Adapter) String() string { return "telegram" }

// Serve long-polls until ctx is cancelled. The library reconnects with
// backoff on network errors; Telegram keeps updates for 24 h meanwhile.
func (a *Adapter) Serve(ctx context.Context) error {
	if me, err := a.b.GetMe(ctx); err == nil {
		a.username.Store(me.Username)
	} else {
		a.log.Warn("telegram: could not read the bot's name", "err", err)
	}
	cmds := make([]models.BotCommand, len(actions.Commands))
	for i, c := range actions.Commands {
		cmds[i] = models.BotCommand{Command: c.Command, Description: c.Description}
	}
	if _, err := a.b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: cmds}); err != nil {
		a.log.Warn("telegram: could not set the command menu", "err", err)
	}
	// The profile people see before pressing Start, so a fresh bot from
	// BotFather needs no manual setup.
	_, _ = a.b.SetMyShortDescription(ctx, &bot.SetMyShortDescriptionParams{ShortDescription: "Your subscriptions, handled. Reminders you can act on, right here."})
	_, _ = a.b.SetMyDescription(ctx, &bot.SetMyDescriptionParams{Description: "This is your private Exhale bot. It reminds you before every charge and lets you fix anything from the message: mark it paid, move the date, change the card or cancel. Only people you link from Exhale can use it."})
	a.b.Start(ctx)
	return nil
}

// Send posts a message and returns its id.
func (a *Adapter) Send(ctx context.Context, chatID string, m actions.Message) (string, error) {
	msg, err := a.b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: format(m), ParseMode: models.ParseModeHTML, ReplyMarkup: keyboard(m)})
	if err != nil {
		return "", err
	}
	return strconv.Itoa(msg.ID), nil
}

// Edit replaces a message in place.
func (a *Adapter) Edit(ctx context.Context, chatID, messageID string, m actions.Message) error {
	id, err := strconv.Atoi(messageID)
	if err != nil {
		return err
	}
	_, err = a.b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: id, Text: format(m), ParseMode: models.ParseModeHTML, ReplyMarkup: keyboard(m)})
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}

func format(m actions.Message) string {
	var b strings.Builder
	if m.Title != "" { // an assistant's answer has no title, only lines
		b.WriteString("<b>" + html.EscapeString(m.Title) + "</b>")
	}
	for _, l := range m.Lines {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(html.EscapeString(l))
	}
	for _, s := range m.Sections {
		b.WriteString("\n")
		if s.Heading != "" {
			b.WriteString("\n<b>" + html.EscapeString(s.Heading) + "</b>")
		}
		for _, l := range s.Lines {
			b.WriteString("\n" + html.EscapeString(l))
		}
		if len(s.Table) > 0 {
			b.WriteString("\n<pre>" + html.EscapeString(s.TableText()) + "</pre>")
		}
	}
	if m.Footer != "" {
		b.WriteString("\n<i>" + html.EscapeString(m.Footer) + "</i>")
	}
	return b.String()
}

func keyboard(m actions.Message) models.ReplyMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, len(m.Rows))
	for _, r := range m.Rows {
		row := make([]models.InlineKeyboardButton, 0, len(r))
		for _, btn := range r {
			if btn.URL != "" {
				row = append(row, models.InlineKeyboardButton{Text: btn.Label, URL: btn.URL})
				continue
			}
			row = append(row, models.InlineKeyboardButton{Text: btn.Label, CallbackData: btn.Data})
		}
		rows = append(rows, row)
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (a *Adapter) handle(ctx context.Context, _ *bot.Bot, u *models.Update) {
	switch {
	case u.CallbackQuery != nil:
		a.handleButton(ctx, u.CallbackQuery)
	case u.Message != nil && u.Message.Text != "":
		a.handleText(ctx, u.Message)
	}
}

func (a *Adapter) reply(ctx context.Context, chatID string, text string) {
	if _, err := a.b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text}); err != nil {
		a.log.Warn("telegram reply failed", "err", err)
	}
}

func (a *Adapter) handleText(ctx context.Context, m *models.Message) {
	chatID := strconv.FormatInt(m.Chat.ID, 10)
	chat := actions.ChatRef{Channel: Name, ChatID: chatID}
	text := strings.TrimSpace(m.Text)
	if code, ok := strings.CutPrefix(text, "/start join-"); ok {
		a.join(ctx, m, chat, code)
		return
	}
	if code, ok := strings.CutPrefix(text, "/start "); ok {
		if _, err := auth.RedeemLinkCode(ctx, a.svc.Store(), Name, strings.TrimSpace(code), chatID); err != nil {
			a.reply(ctx, chatID, "That code is not valid anymore: codes work once and for 10 minutes. "+
				"In Exhale, press “Link Telegram” again and scan the new QR code (or send the new /start message).")
			return
		}
		// Pins left by an earlier link (e.g. an older database) would point to
		// screens that no longer exist.
		_, _ = a.b.UnpinAllChatMessages(ctx, &bot.UnpinAllChatMessagesParams{ChatID: chatID})
		a.reply(ctx, chatID, "✅ Linked. Reminders arrive here, and the message below is Exhale itself: tap around.")
		if actor, err := auth.MemberForChannel(ctx, a.svc.Store(), Name, chatID); err == nil {
			a.openApp(ctx, actor, chat, actions.RouteHome)
		}
		return
	}
	actor, err := auth.MemberForChannel(ctx, a.svc.Store(), Name, chatID)
	if err != nil {
		if strings.HasPrefix(text, "/start") {
			a.reply(ctx, chatID, "Hi! This is a private Exhale bot. To link this chat, open Exhale, press “Link Telegram” and scan the QR code.")
		}
		return // unlinked chats are ignored (ADR 0006)
	}
	if rep, ok, err := a.eng.HandleText(ctx, actor, chat, text); ok {
		if err != nil {
			a.log.Error("telegram text", "err", err)
			return
		}
		a.apply(ctx, chat, 0, rep)
		return
	}
	if cmd, _, _ := strings.Cut(text, " "); strings.HasPrefix(cmd, "/") {
		if rep, ok := a.eng.Command(ctx, actor, chat, cmd); ok {
			a.apply(ctx, chat, 0, rep)
			return
		}
		if r, ok := actions.CommandRoute(cmd); ok {
			a.openApp(ctx, actor, chat, r)
			return
		}
	}
	if !strings.HasPrefix(text, "/") && a.eng.CanAsk(ctx) {
		// Off the polling loop: a model takes seconds, and buttons must keep
		// working meanwhile.
		go a.ask(context.WithoutCancel(ctx), actor, chat, text)
		return
	}
	var help strings.Builder
	help.WriteString("Commands:")
	for _, c := range actions.Commands {
		help.WriteString("\n/" + c.Command + " — " + c.Description)
	}
	a.reply(ctx, chatID, help.String())
}

// ask sends free text to the built-in assistant (ADR 0018) and posts what
// it answers. "typing…" shows while the model works.
func (a *Adapter) ask(ctx context.Context, actor app.Actor, chat actions.ChatRef, text string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	typing, stop := context.WithCancel(ctx)
	go func() {
		for {
			_, _ = a.b.SendChatAction(typing, &bot.SendChatActionParams{ChatID: chat.ChatID, Action: models.ChatActionTyping})
			select {
			case <-typing.Done():
				return
			case <-time.After(4 * time.Second): // Telegram shows it for five
			}
		}
	}()
	msgs, _ := a.eng.Ask(ctx, actor, chat, text)
	stop()
	for _, m := range msgs {
		if _, err := a.Send(ctx, chat.ChatID, m); err != nil {
			a.log.Warn("telegram send failed", "err", err)
		}
	}
}

// join accepts a household invite from Telegram: the person becomes a
// member with this chat linked, without needing to reach the web.
func (a *Adapter) join(ctx context.Context, m *models.Message, chat actions.ChatRef, code string) {
	name := strings.TrimSpace(m.From.FirstName + " " + m.From.LastName)
	if name == "" {
		name = m.From.Username
	}
	actor, household, err := auth.JoinFromChat(ctx, a.svc.Store(), strings.TrimSpace(code), Name, chat.ChatID, name)
	switch {
	case errors.Is(err, domain.ErrConflict):
		a.reply(ctx, chat.ChatID, "This chat is already linked to Exhale.")
		return
	case err != nil:
		a.reply(ctx, chat.ChatID, "This invite has expired or was already used. Ask for a new one.")
		return
	}
	_, _ = a.b.UnpinAllChatMessages(ctx, &bot.UnpinAllChatMessagesParams{ChatID: chat.ChatID})
	a.reply(ctx, chat.ChatID, fmt.Sprintf("👋 Welcome to %s, %s. Your reminders arrive here, and the message below is Exhale itself: tap around.", household, actor.Label))
	a.openApp(ctx, actor, chat, actions.RouteHome)
}

// openApp posts a new app message (ADR 0019), pins it and unpins the one it
// replaces.
func (a *Adapter) openApp(ctx context.Context, actor app.Actor, chat actions.ChatRef, route string) {
	m, err := a.eng.OpenApp(ctx, actor, route)
	if err != nil {
		a.log.Error("telegram app", "route", route, "err", err)
		a.reply(ctx, chat.ChatID, "Something went wrong opening Exhale.")
		return
	}
	id, err := a.Send(ctx, chat.ChatID, m)
	if err != nil {
		a.log.Warn("telegram send failed", "err", err)
		return
	}
	old, err := a.eng.AppSent(ctx, actor, chat, id, route)
	if err != nil {
		a.log.Warn("telegram: could not record the app message", "err", err)
	}
	for _, o := range old {
		if n, err := strconv.Atoi(o); err == nil {
			_, _ = a.b.UnpinChatMessage(ctx, &bot.UnpinChatMessageParams{ChatID: chat.ChatID, MessageID: n})
		}
	}
	if n, err := strconv.Atoi(id); err == nil {
		if _, err := a.b.PinChatMessage(ctx, &bot.PinChatMessageParams{ChatID: chat.ChatID, MessageID: n, DisableNotification: true}); err != nil {
			a.log.Debug("telegram: could not pin the app message", "err", err)
		}
	}
}

func (a *Adapter) handleButton(ctx context.Context, q *models.CallbackQuery) {
	if q.Message.Message == nil {
		_, _ = a.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: "This message is too old."})
		return
	}
	chatID := strconv.FormatInt(q.Message.Message.Chat.ID, 10)
	actor, err := auth.MemberForChannel(ctx, a.svc.Store(), Name, chatID)
	if err != nil {
		_, _ = a.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: "This chat is not linked to Exhale."})
		return
	}
	actor.Label += " (Telegram)"
	chat := actions.ChatRef{Channel: Name, ChatID: chatID, MessageID: strconv.Itoa(q.Message.Message.ID)}
	rep, err := a.eng.Handle(ctx, actor, chat, q.Data)
	if err != nil {
		a.log.Error("telegram button", "data", q.Data, "err", err)
		_, _ = a.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: "Something went wrong."})
		return
	}
	_, _ = a.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: rep.Toast})
	a.apply(ctx, chat, q.Message.Message.ID, rep)
}

func (a *Adapter) apply(ctx context.Context, chat actions.ChatRef, messageID int, rep actions.Reply) {
	if rep.Edit != nil && messageID != 0 {
		if err := a.Edit(ctx, chat.ChatID, strconv.Itoa(messageID), *rep.Edit); err != nil {
			a.log.Warn("telegram edit failed", "err", err)
		}
	}
	if rep.Send != nil {
		if _, err := a.Send(ctx, chat.ChatID, *rep.Send); err != nil {
			a.log.Warn("telegram send failed", "err", err)
		}
	}
}
