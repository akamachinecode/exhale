package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/akamachinecode/exhale/internal/channels/telegram"
	"github.com/akamachinecode/exhale/internal/domain"
)

// TelegramControl configures the household's own Telegram bot from the web
// (ADR 0019). Like the auth endpoints it is not in the op catalog: agents
// never handle bot tokens.
type TelegramControl interface {
	Status(ctx context.Context) telegram.BotStatus
	Configure(ctx context.Context, token string) (telegram.BotStatus, error)
	Remove(ctx context.Context) (telegram.BotStatus, error)
	Username() string
}

func (a *authAPI) owner(ctx context.Context) error {
	actor, err := a.member(ctx)
	if err != nil {
		return err
	}
	m, err := a.svc.Store().Q.GetMember(ctx, actor.MemberID)
	if err != nil || m.Role != string(domain.RoleOwner) {
		return huma.Error403Forbidden("only a household owner can change the bot")
	}
	return nil
}

func settingsOp(id, method, path, summary string) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: "/api/v1/settings" + path, Summary: summary, Tags: []string{"Settings"}}
}

func registerSettings(api huma.API, a *authAPI) {
	status := func(ctx context.Context) telegram.BotStatus {
		if a.cfg.Telegram == nil {
			return telegram.BotStatus{}
		}
		return a.cfg.Telegram.Status(ctx)
	}
	huma.Register(api, settingsOp("settings_telegram", http.MethodGet, "/telegram", "Telegram bot status"),
		func(ctx context.Context, _ *struct{}) (*resp[telegram.BotStatus], error) {
			if _, err := a.member(ctx); err != nil {
				return nil, err
			}
			return &resp[telegram.BotStatus]{Body: status(ctx)}, nil
		})

	type botTokenIn struct {
		Token string `json:"token" doc:"the token BotFather gives you, like 123456789:AAH…"`
	}
	huma.Register(api, settingsOp("settings_set_telegram", http.MethodPost, "/telegram", "Set the Telegram bot token (owners only)"),
		func(ctx context.Context, in *struct{ Body botTokenIn }) (*resp[telegram.BotStatus], error) {
			if err := a.owner(ctx); err != nil {
				return nil, err
			}
			if a.cfg.Telegram == nil {
				return nil, huma.Error503ServiceUnavailable("telegram is not available in this build")
			}
			st, err := a.cfg.Telegram.Configure(ctx, in.Body.Token)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[telegram.BotStatus]{Body: st}, nil
		})

	huma.Register(api, settingsOp("settings_remove_telegram", http.MethodPost, "/telegram/remove", "Remove the Telegram bot (owners only)"),
		func(ctx context.Context, _ *struct{}) (*resp[telegram.BotStatus], error) {
			if err := a.owner(ctx); err != nil {
				return nil, err
			}
			if a.cfg.Telegram == nil {
				return &resp[telegram.BotStatus]{Body: telegram.BotStatus{}}, nil
			}
			st, err := a.cfg.Telegram.Remove(ctx)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[telegram.BotStatus]{Body: st}, nil
		})
}
