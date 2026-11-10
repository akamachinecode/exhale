package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/channels/slack"
)

// SlackControl configures the team's own Slack app from the web (ADR 0007,
// 0019). Like the Telegram endpoints it is not in the op catalog: agents
// never handle tokens.
type SlackControl interface {
	Status(ctx context.Context) slack.Status
	Configure(ctx context.Context, in slack.Config) (slack.Status, error)
	Remove(ctx context.Context) (slack.Status, error)
}

// SlackStatus and SlackConfig name the Slack types in the API contract.
type (
	SlackStatus slack.Status
	SlackConfig slack.Config
)

// SlackLinkCode is a one-time code that links a Slack account to a member.
type SlackLinkCode struct {
	Code    string    `json:"code"`
	Command string    `json:"command" doc:"what to type in Slack, anywhere"`
	URL     string    `json:"url,omitempty" doc:"link that opens the app in Slack, when it is connected"`
	Expires time.Time `json:"expiresAt"`
}

func registerSlack(api huma.API, a *authAPI) {
	status := func(ctx context.Context) slack.Status {
		if a.cfg.Slack == nil {
			return slack.Status{Manifest: slack.Manifest, CreateURL: slack.CreateURL()}
		}
		return a.cfg.Slack.Status(ctx)
	}
	huma.Register(api, settingsOp("settings_slack", http.MethodGet, "/slack", "Slack app status and the manifest to create it"),
		func(ctx context.Context, _ *struct{}) (*resp[SlackStatus], error) {
			if _, err := a.member(ctx); err != nil {
				return nil, err
			}
			return &resp[SlackStatus]{Body: SlackStatus(status(ctx))}, nil
		})

	huma.Register(api, settingsOp("settings_set_slack", http.MethodPost, "/slack", "Set the Slack app tokens or the team channel (owners only)"),
		func(ctx context.Context, in *struct{ Body SlackConfig }) (*resp[SlackStatus], error) {
			if err := a.owner(ctx); err != nil {
				return nil, err
			}
			if a.cfg.Slack == nil {
				return nil, huma.Error503ServiceUnavailable("slack is not available in this build")
			}
			st, err := a.cfg.Slack.Configure(ctx, slack.Config(in.Body))
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[SlackStatus]{Body: SlackStatus(st)}, nil
		})

	huma.Register(api, settingsOp("settings_remove_slack", http.MethodPost, "/slack/remove", "Disconnect the Slack app (owners only)"),
		func(ctx context.Context, _ *struct{}) (*resp[SlackStatus], error) {
			if err := a.owner(ctx); err != nil {
				return nil, err
			}
			if a.cfg.Slack == nil {
				return &resp[SlackStatus]{Body: SlackStatus(status(ctx))}, nil
			}
			st, err := a.cfg.Slack.Remove(ctx)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[SlackStatus]{Body: SlackStatus(st)}, nil
		})

	huma.Register(api, authOp("auth_link_slack", http.MethodPost, "/links/slack", "Get a one-time code to link Slack"),
		func(ctx context.Context, _ *struct{}) (*resp[SlackLinkCode], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			code, err := auth.CreateLinkCode(ctx, a.svc.Store(), actor.MemberID, slack.Name)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[SlackLinkCode]{Body: SlackLinkCode{
				Code: code, Command: "/exhale link " + code, URL: status(ctx).AppURL, Expires: time.Now().Add(10 * time.Minute),
			}}, nil
		})
}
