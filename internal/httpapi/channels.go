package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/channels/email"
	"github.com/akamachinecode/exhale/internal/channels/notifiers"
	"github.com/akamachinecode/exhale/internal/domain"
)

// The notify-only channels (email, ntfy, webhooks). Owners set up the
// instance (SMTP server, ntfy server, private destinations); every member
// manages their own destinations. Like the bot token these endpoints are
// not in the op catalog: agents never handle SMTP passwords or webhook
// URLs.

func (a *authAPI) channels() (*notifiers.Manager, error) {
	if a.cfg.Channels == nil {
		return nil, huma.Error503ServiceUnavailable("notification channels are not available in this build")
	}
	return a.cfg.Channels, nil
}

func (a *authAPI) isOwner(ctx context.Context) (bool, error) {
	actor, err := a.member(ctx)
	if err != nil {
		return false, err
	}
	m, err := a.svc.Store().Q.GetMember(ctx, actor.MemberID)
	return err == nil && m.Role == string(domain.RoleOwner), nil
}

// channelsOwner returns the manager for a request that changes the
// instance's setup.
func (a *authAPI) channelsOwner(ctx context.Context) (*notifiers.Manager, error) {
	owner, err := a.isOwner(ctx)
	if err != nil {
		return nil, err
	}
	if !owner {
		return nil, huma.Error403Forbidden("only a household owner can change how notifications are sent")
	}
	return a.channels()
}

// sendError reports a failed delivery to the person who asked for it. The
// messages name hosts and status codes, never secrets.
func sendError(err error) error {
	if errors.Is(err, domain.ErrValidation) || errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrNotFound) {
		return httpError(err)
	}
	if errors.Is(err, email.ErrNotConfigured) {
		return huma.Error409Conflict("Email is not set up yet: an owner has to add the mail server first")
	}
	msg := err.Error()
	return huma.Error502BadGateway(strings.ToUpper(msg[:1]) + msg[1:])
}

// describe fills what the web shows about a link.
func (a *authAPI) describe(ctx context.Context, l auth.LinkInfo) auth.LinkInfo {
	if ch := a.cfg.Channels; ch != nil && ch.Notifies(l.Channel) {
		l.Destination, l.HasTemplate = ch.Describe(ctx, l.Channel, l.ExternalID)
	}
	return l
}

func registerChannels(api huma.API, a *authAPI) {
	status := func(ctx context.Context, ch *notifiers.Manager) (*resp[notifiers.ChannelsStatus], error) {
		owner, err := a.isOwner(ctx)
		if err != nil {
			return nil, err
		}
		return &resp[notifiers.ChannelsStatus]{Body: ch.Status(ctx, owner)}, nil
	}
	huma.Register(api, settingsOp("settings_channels", http.MethodGet, "/channels", "Email, ntfy and webhook setup"),
		func(ctx context.Context, _ *struct{}) (*resp[notifiers.ChannelsStatus], error) {
			if _, err := a.member(ctx); err != nil {
				return nil, err
			}
			if a.cfg.Channels == nil {
				return &resp[notifiers.ChannelsStatus]{}, nil
			}
			return status(ctx, a.cfg.Channels)
		})

	type privateDestinationsIn struct {
		AllowPrivate bool `json:"allowPrivate" doc:"let webhooks and ntfy reach private-network addresses (a server on your LAN); loopback and cloud metadata addresses stay blocked"`
	}
	huma.Register(api, settingsOp("settings_set_channels", http.MethodPost, "/channels", "Allow or block private-network destinations (owners only)"),
		func(ctx context.Context, in *struct{ Body privateDestinationsIn }) (*resp[notifiers.ChannelsStatus], error) {
			ch, err := a.channelsOwner(ctx)
			if err != nil {
				return nil, err
			}
			if err := ch.SetAllowPrivate(ctx, in.Body.AllowPrivate); err != nil {
				return nil, httpError(err)
			}
			return status(ctx, ch)
		})

	huma.Register(api, settingsOp("settings_set_smtp", http.MethodPost, "/smtp", "Set the mail server for email reminders (owners only)"),
		func(ctx context.Context, in *struct{ Body notifiers.SMTPInput }) (*resp[notifiers.ChannelsStatus], error) {
			ch, err := a.channelsOwner(ctx)
			if err != nil {
				return nil, err
			}
			if err := ch.SetSMTP(ctx, in.Body); err != nil {
				return nil, httpError(err)
			}
			return status(ctx, ch)
		})

	huma.Register(api, settingsOp("settings_remove_smtp", http.MethodPost, "/smtp/remove", "Remove the mail server (owners only)"),
		func(ctx context.Context, _ *struct{}) (*resp[notifiers.ChannelsStatus], error) {
			ch, err := a.channelsOwner(ctx)
			if err != nil {
				return nil, err
			}
			if err := ch.RemoveSMTP(ctx); err != nil {
				return nil, httpError(err)
			}
			return status(ctx, ch)
		})

	// Test sends are throttled per member: they reach other people's
	// servers and inboxes.
	tests := &limiter{}
	throttle := func(memberID string) error {
		if !tests.allow(memberID) {
			return huma.Error429TooManyRequests("too many test messages; wait a minute and try again")
		}
		return nil
	}

	type testEmailOut struct {
		SentTo string `json:"sentTo"`
	}
	huma.Register(api, settingsOp("settings_test_smtp", http.MethodPost, "/smtp/test", "Send a test email to your own address (owners only)"),
		func(ctx context.Context, _ *struct{}) (*resp[testEmailOut], error) {
			ch, err := a.channelsOwner(ctx)
			if err != nil {
				return nil, err
			}
			actor, _ := a.member(ctx)
			if err := throttle(actor.MemberID); err != nil {
				return nil, err
			}
			m, err := a.svc.Store().Q.GetMember(ctx, actor.MemberID)
			if err != nil {
				return nil, httpError(err)
			}
			to, err := email.Address(m.Email)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("Your account has no email address to send the test to")
			}
			if err := ch.Test(ctx, email.Name, to); err != nil {
				return nil, sendError(err)
			}
			return &resp[testEmailOut]{Body: testEmailOut{SentTo: to}}, nil
		})

	huma.Register(api, settingsOp("settings_set_ntfy", http.MethodPost, "/ntfy", "Set the ntfy server (owners only)"),
		func(ctx context.Context, in *struct{ Body notifiers.NtfyInput }) (*resp[notifiers.ChannelsStatus], error) {
			ch, err := a.channelsOwner(ctx)
			if err != nil {
				return nil, err
			}
			if err := ch.SetNtfy(ctx, in.Body); err != nil {
				return nil, httpError(err)
			}
			return status(ctx, ch)
		})

	huma.Register(api, settingsOp("settings_remove_ntfy", http.MethodPost, "/ntfy/remove", "Go back to the public ntfy server (owners only)"),
		func(ctx context.Context, _ *struct{}) (*resp[notifiers.ChannelsStatus], error) {
			ch, err := a.channelsOwner(ctx)
			if err != nil {
				return nil, err
			}
			if err := ch.RemoveNtfy(ctx); err != nil {
				return nil, httpError(err)
			}
			return status(ctx, ch)
		})

	type destinationIn struct {
		Channel     string `json:"channel" enum:"email,ntfy,webhook"`
		Destination string `json:"destination" doc:"email: the address; ntfy: your topic; webhook: the URL to POST to"`
		Template    string `json:"template,omitempty" doc:"webhook only: JSON body with {{variables}}; empty sends the default body"`
	}
	huma.Register(api, authOp("auth_add_destination", http.MethodPost, "/links/destination", "Add an email address, ntfy topic or webhook for your reminders"),
		func(ctx context.Context, in *struct{ Body destinationIn }) (*resp[auth.LinkInfo], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			ch, err := a.channels()
			if err != nil {
				return nil, err
			}
			dest, err := ch.Validate(ctx, in.Body.Channel, in.Body.Destination, in.Body.Template)
			if err != nil {
				return nil, httpError(err)
			}
			link, err := auth.AddDestination(ctx, a.svc.Store(), actor.MemberID, in.Body.Channel, dest)
			if err != nil {
				return nil, httpError(err)
			}
			// Only now is the destination this member's: save its template.
			if err := ch.SetTemplate(ctx, dest, in.Body.Template); err != nil {
				return nil, httpError(err)
			}
			return &resp[auth.LinkInfo]{Body: a.describe(ctx, link)}, nil
		})

	type testLinkIn struct {
		ID string `json:"id" doc:"id of one of your links"`
	}
	huma.Register(api, authOp("auth_test_link", http.MethodPost, "/links/test", "Send a test reminder to one of your destinations"),
		func(ctx context.Context, in *struct{ Body testLinkIn }) (*resp[empty], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			ch, err := a.channels()
			if err != nil {
				return nil, err
			}
			link, err := auth.Link(ctx, a.svc.Store(), actor.MemberID, in.Body.ID)
			if err != nil {
				return nil, httpError(err)
			}
			if err := throttle(actor.MemberID); err != nil {
				return nil, err
			}
			if err := ch.Test(ctx, link.Channel, link.ExternalID); err != nil {
				return nil, sendError(err)
			}
			return &resp[empty]{}, nil
		})
}
