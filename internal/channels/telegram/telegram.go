// Package telegram (piece ②) connects Exhale to a Telegram bot over long
// polling: an outbound connection, so no public URL or open port is needed
// (ADR 0001, 0007). It only renders action-layer messages as inline
// keyboards and passes button presses back. Its home message is the app
// itself: one pinned message that navigates by editing in place (ADR 0019).
package telegram

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
)

// Name is the channel name stored in channel links.
const Name = "telegram"

// Adapter is the Telegram channel.
type Adapter struct {
	b   *bot.Bot
	svc *app.Service
	eng *actions.Engine
	log *slog.Logger
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

func (a *Adapter) Name() string   { return Name }
func (a *Adapter) String() string { return "telegram" }

// Serve long-polls until ctx is cancelled. The library reconnects with
// backoff on network errors; Telegram keeps updates for 24 h meanwhile.
func (a *Adapter) Serve(ctx context.Context) error {
	cmds := make([]models.BotCommand, len(actions.Commands))
	for i, c := range actions.Commands {
		cmds[i] = models.BotCommand{Command: c.Command, Description: c.Description}
	}
	if _, err := a.b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: cmds}); err != nil {
		a.log.Warn("telegram: could not set the command menu", "err", err)
	}
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
	b.WriteString("<b>" + html.EscapeString(m.Title) + "</b>")
	for _, l := range m.Lines {
		b.WriteString("\n" + html.EscapeString(l))
	}
	for _, s := range m.Sections {
		b.WriteString("\n\n<b>" + html.EscapeString(s.Heading) + "</b>")
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
	if code, ok := strings.CutPrefix(text, "/start "); ok {
		if _, err := auth.RedeemLinkCode(ctx, a.svc.Store(), Name, strings.TrimSpace(code), chatID); err != nil {
			a.reply(ctx, chatID, "This link has expired or was already used. Create a new one in Exhale.")
			return
		}
		a.reply(ctx, chatID, "✅ Linked. Reminders arrive here, and the message below is Exhale itself: tap around.")
		if actor, err := auth.MemberForChannel(ctx, a.svc.Store(), Name, chatID); err == nil {
			a.openApp(ctx, actor, chat, actions.RouteHome)
		}
		return
	}
	actor, err := auth.MemberForChannel(ctx, a.svc.Store(), Name, chatID)
	if err != nil {
		if strings.HasPrefix(text, "/start") {
			a.reply(ctx, chatID, "Hi! This is a private Exhale bot. Open Exhale → Settings → Channels → Telegram to link this chat.")
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
		if r, ok := actions.CommandRoute(cmd); ok {
			a.openApp(ctx, actor, chat, r)
			return
		}
	}
	var help strings.Builder
	help.WriteString("Commands:")
	for _, c := range actions.Commands {
		help.WriteString("\n/" + c.Command + " — " + c.Description)
	}
	a.reply(ctx, chatID, help.String())
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
