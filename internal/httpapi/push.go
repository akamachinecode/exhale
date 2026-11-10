package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/channels/notifiers"
)

// Web Push (ADR 0026). Devices are managed by the person who owns them;
// listing, testing and removing other devices use the links endpoints like
// every destination. The action endpoint is public: the signed token in
// the notification is the credential, so buttons work with no session.

// pushActionPath is called by the service worker.
const pushActionPath = "/api/v1/push/action"

func pushOp(id, method, path, summary string) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: "/api/v1/push" + path, Summary: summary, Tags: []string{"Push"}}
}

func registerPush(api huma.API, a *authAPI) {
	huma.Register(api, pushOp("push_status", http.MethodGet, "", "The key browsers subscribe to push notifications with"),
		func(ctx context.Context, _ *struct{}) (*resp[notifiers.PushStatus], error) {
			if _, err := a.member(ctx); err != nil {
				return nil, err
			}
			ch, err := a.channels()
			if err != nil {
				return nil, err
			}
			st, err := ch.PushStatus(ctx)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[notifiers.PushStatus]{Body: st}, nil
		})

	huma.Register(api, pushOp("push_add_device", http.MethodPost, "/devices", "Turn on push notifications in this browser"),
		func(ctx context.Context, in *struct{ Body notifiers.PushDevice }) (*resp[auth.LinkInfo], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			ch, err := a.channels()
			if err != nil {
				return nil, err
			}
			id, err := ch.AddDevice(ctx, actor.MemberID, in.Body)
			if err != nil {
				return nil, httpError(err)
			}
			link, err := auth.Link(ctx, a.svc.Store(), actor.MemberID, id)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[auth.LinkInfo]{Body: a.describe(ctx, link)}, nil
		})

	type endpointIn struct {
		Endpoint string `json:"endpoint" doc:"PushSubscription.endpoint of this browser"`
	}
	type deviceOut struct {
		ID string `json:"id,omitempty" doc:"link id of this browser; empty when push is not on for you here"`
	}
	// POST, not GET: an endpoint is a capability and stays out of URLs.
	huma.Register(api, pushOp("push_find_device", http.MethodPost, "/devices/find", "Is push on for you in this browser?"),
		func(ctx context.Context, in *struct{ Body endpointIn }) (*resp[deviceOut], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			links, err := auth.ListLinks(ctx, a.svc.Store(), actor.MemberID)
			if err != nil {
				return nil, httpError(err)
			}
			for _, l := range links {
				if l.Channel == "webpush" && l.ExternalID == in.Body.Endpoint {
					return &resp[deviceOut]{Body: deviceOut{ID: l.ID}}, nil
				}
			}
			return &resp[deviceOut]{}, nil
		})

	huma.Register(api, pushOp("push_remove_device", http.MethodPost, "/devices/remove", "Turn off push notifications in this browser"),
		func(ctx context.Context, in *struct{ Body endpointIn }) (*resp[empty], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			ch, err := a.channels()
			if err != nil {
				return nil, err
			}
			if err := ch.RemoveDevice(ctx, actor.MemberID, in.Body.Endpoint); err != nil {
				return nil, httpError(err)
			}
			return &resp[empty]{}, nil
		})

	type actionIn struct {
		Token string `json:"token" maxLength:"1024" doc:"the token of the button, from the notification"`
	}
	huma.Register(api, pushOp("push_action", http.MethodPost, "/action", "Run a notification's button (no session: the token is the credential)"),
		func(ctx context.Context, in *struct{ Body actionIn }) (*resp[actions.PushResult], error) {
			ch, err := a.channels()
			if err != nil {
				return nil, err
			}
			out, err := ch.PushAction(ctx, in.Body.Token)
			if errors.Is(err, notifiers.ErrActionExpired) {
				return nil, huma.Error401Unauthorized("This button has expired. Open Exhale to do it there.")
			}
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[actions.PushResult]{Body: out}, nil
		})
}
