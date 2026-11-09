package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Each Exhale runs its own bot, created by the user with @BotFather. The
// token comes from EXHALE_TELEGRAM_TOKEN or, more usually, from the web,
// where the onboarding guides the user through BotFather (ADR 0019). The
// Manager runs the adapter for the current token and swaps it when the
// token changes, without restarting the server.

const settingToken = "telegram_token"

// ErrNotRunning is returned when there is no bot to send through.
var ErrNotRunning = errors.New("telegram is not set up")

// BotStatus describes the bot for the settings UI.
type BotStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty" enum:"env,settings" doc:"env: set by EXHALE_TELEGRAM_TOKEN and not editable in the web"`
	Username   string `json:"username,omitempty" doc:"the bot's @username, without @"`
	Name       string `json:"name,omitempty" doc:"the bot's display name"`
	Running    bool   `json:"running"`
}

// Manager runs the Telegram adapter. It implements actions.Channel and the
// platform service interface.
type Manager struct {
	svc      *app.Service
	log      *slog.Logger
	envToken string
	apiURL   string

	mu      sync.RWMutex
	cur     *Adapter
	name    string
	eng     *actions.Engine
	restart chan struct{}
}

// NewManager returns a manager. envToken (may be empty) wins over a token
// saved from the web; apiURL overrides the Bot API URL (tests).
func NewManager(envToken, apiURL string, svc *app.Service, log *slog.Logger) *Manager {
	return &Manager{svc: svc, log: log, envToken: strings.TrimSpace(envToken), apiURL: apiURL, restart: make(chan struct{}, 1)}
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
func (m *Manager) String() string { return "telegram" }

func (m *Manager) adapter() (*Adapter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cur == nil {
		return nil, ErrNotRunning
	}
	return m.cur, nil
}

// Send posts through the running bot.
func (m *Manager) Send(ctx context.Context, chatID string, msg actions.Message) (string, error) {
	a, err := m.adapter()
	if err != nil {
		return "", err
	}
	return a.Send(ctx, chatID, msg)
}

// Edit edits through the running bot.
func (m *Manager) Edit(ctx context.Context, chatID, messageID string, msg actions.Message) error {
	a, err := m.adapter()
	if err != nil {
		return err
	}
	return a.Edit(ctx, chatID, messageID, msg)
}

// Username is the running bot's @username ("" if none).
func (m *Manager) Username() string {
	a, err := m.adapter()
	if err != nil {
		return ""
	}
	return a.Username()
}

func (m *Manager) token(ctx context.Context) (token, source string) {
	if m.envToken != "" {
		return m.envToken, "env"
	}
	t, err := m.svc.Store().Q.GetSetting(ctx, settingToken)
	if err != nil || t == "" {
		return "", ""
	}
	return t, "settings"
}

// Status reports whether a bot is configured and running.
func (m *Manager) Status(ctx context.Context) BotStatus {
	tok, src := m.token(ctx)
	st := BotStatus{Configured: tok != "", Source: src}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cur != nil {
		st.Running, st.Username, st.Name = true, m.cur.Username(), m.name
	}
	return st
}

// Configure checks a token with Telegram, saves it and switches the bot to
// it. It returns the new status.
func (m *Manager) Configure(ctx context.Context, token string) (BotStatus, error) {
	if m.envToken != "" {
		return BotStatus{}, fmt.Errorf("%w: the bot is set by EXHALE_TELEGRAM_TOKEN on the server; change it there", domain.ErrConflict)
	}
	token = strings.TrimSpace(token)
	if !strings.Contains(token, ":") || len(token) < 20 {
		return BotStatus{}, fmt.Errorf("%w: that does not look like a bot token; BotFather's looks like 123456789:AAH…", domain.ErrValidation)
	}
	me, err := m.check(ctx, token)
	if err != nil {
		return BotStatus{}, err
	}
	if err := m.svc.Store().Q.PutSetting(ctx, db.PutSettingParams{Key: settingToken, Value: token}); err != nil {
		return BotStatus{}, err
	}
	m.log.Info("telegram bot configured from the web", "bot", me.Username)
	m.kick()
	return m.waitRunning(ctx), nil
}

// Remove forgets a token saved from the web and stops the bot.
func (m *Manager) Remove(ctx context.Context) (BotStatus, error) {
	if m.envToken != "" {
		return BotStatus{}, fmt.Errorf("%w: the bot is set by EXHALE_TELEGRAM_TOKEN on the server; remove it there", domain.ErrConflict)
	}
	if err := m.svc.Store().Q.PutSetting(ctx, db.PutSettingParams{Key: settingToken, Value: ""}); err != nil {
		return BotStatus{}, err
	}
	m.kick()
	time.Sleep(50 * time.Millisecond)
	return m.Status(ctx), nil
}

type botInfo struct{ Username, Name string }

func (m *Manager) check(ctx context.Context, token string) (botInfo, error) {
	opts := []bot.Option{bot.WithSkipGetMe()}
	if m.apiURL != "" {
		opts = append(opts, bot.WithServerURL(m.apiURL))
	}
	b, err := bot.New(token, opts...)
	if err != nil {
		return botInfo{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	me, err := b.GetMe(cctx)
	if err != nil {
		if strings.Contains(err.Error(), "Unauthorized") || strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "404") {
			return botInfo{}, fmt.Errorf("%w: Telegram does not recognize this token; copy it again from BotFather", domain.ErrValidation)
		}
		return botInfo{}, fmt.Errorf("could not reach Telegram to check the token: %w", err)
	}
	return botInfo{Username: me.Username, Name: me.FirstName}, nil
}

func (m *Manager) kick() {
	select {
	case m.restart <- struct{}{}:
	default:
	}
}

func (m *Manager) waitRunning(ctx context.Context) BotStatus {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := m.Status(ctx); st.Running && st.Username != "" {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	return m.Status(ctx)
}

// Serve runs the bot for the current token and restarts it when the token
// changes, until ctx ends.
func (m *Manager) Serve(ctx context.Context) error {
	for {
		tok, _ := m.token(ctx)
		if tok == "" {
			m.log.Info("telegram is not set up: add your bot in the web (Settings → Telegram) or set EXHALE_TELEGRAM_TOKEN")
			select {
			case <-ctx.Done():
				return nil
			case <-m.restart:
				continue
			}
		}
		a, err := New(tok, m.apiURL, m.svc, m.log)
		if err != nil {
			m.log.Error("telegram: bad token", "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-m.restart:
				continue
			}
		}
		info, _ := m.check(ctx, tok)
		if info.Username != "" {
			a.username.Store(info.Username)
		}
		m.mu.Lock()
		if m.eng != nil {
			a.SetEngine(m.eng)
		}
		m.cur, m.name = a, info.Name
		m.mu.Unlock()
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { _ = a.Serve(runCtx); close(done) }()
		m.log.Info("telegram enabled", "bot", a.Username())
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return nil
		case <-m.restart:
			cancel()
			<-done
			m.mu.Lock()
			m.cur, m.name = nil, ""
			m.mu.Unlock()
		}
	}
}
