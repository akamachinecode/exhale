package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/time/rate"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/domain"
)

// Web accounts and sessions (ADR 0006). These endpoints are for people in
// the web UI, not for AI agents: they are not in the op catalog and MCP
// never sees them.

type localKey struct{}

// isLocal reports whether the request came from the server's own machine
// (no proxy in between).
func isLocal(ctx context.Context) bool { v, _ := ctx.Value(localKey{}).(bool); return v }

// publicAuthPaths answer without credentials.
func publicPath(p string) bool {
	switch p {
	case "/api/v1/auth/session", "/api/v1/auth/setup", "/api/v1/auth/login", "/api/v1/auth/join", "/api/v1/auth/logout",
		oidcStart, oidcCallback:
		return true
	}
	return strings.HasPrefix(p, "/api/v1/auth/invites/")
}

// limiter throttles sign-in attempts per key (IP or email) without locking
// accounts out.
type limiter struct {
	mu sync.Mutex
	m  map[string]*rate.Limiter
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil || len(l.m) > 10_000 {
		l.m = map[string]*rate.Limiter{}
	}
	lim, ok := l.m[key]
	if !ok {
		lim = rate.NewLimiter(rate.Every(6*time.Second), 10) // 10 at once, then 10 per minute
		l.m[key] = lim
	}
	return lim.Allow()
}

type ipKey struct{}

func clientIP(ctx context.Context) string { v, _ := ctx.Value(ipKey{}).(string); return v }

// SessionUser is the signed-in person.
type SessionUser struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Email       string      `json:"email"`
	Role        domain.Role `json:"role"`
	HasPassword bool        `json:"hasPassword" doc:"the account has a password (accounts made through single sign-on have none)"`
	SSOHost     string      `json:"ssoHost,omitempty" doc:"host of the identity provider this account is linked to, if any"`
}

// SSOInfo describes the configured OpenID Connect provider.
type SSOInfo struct {
	Name string `json:"name" doc:"what the sign-in button says after \"Continue with\""`
}

// AuthSession tells the web what to show: the setup wizard, the sign-in
// page or the app.
type AuthSession struct {
	SetupRequired  bool         `json:"setupRequired" doc:"nobody can sign in yet: show the setup wizard"`
	SetupNeedsCode bool         `json:"setupNeedsCode" doc:"the wizard must ask for the setup code (request not from the server's machine)"`
	HasHousehold   bool         `json:"hasHousehold" doc:"a household exists (made with exhale init); setup only creates the owner's login"`
	User           *SessionUser `json:"user,omitempty"`
	TelegramBot    string       `json:"telegramBot,omitempty" doc:"username of the Telegram bot, when Telegram is enabled"`
	SSO            *SSOInfo     `json:"sso,omitempty" doc:"present when single sign-on (OpenID Connect) is enabled; start it by navigating to /api/v1/auth/oidc/start"`
	// PasswordLoginDisabled hides the password forms: sign-in, sign-up and
	// join only go through the provider.
	PasswordLoginDisabled bool `json:"passwordLoginDisabled" doc:"password sign-in is turned off: only show single sign-on"`
}

type authAPI struct {
	svc    *app.Service
	sm     *scs.SessionManager
	cfg    Config
	ips    limiter
	emails limiter
	// Single sign-on: attempts in progress and their own throttle.
	flows     flowStore
	ssoStarts limiter
}

func (a *authAPI) session(ctx context.Context) (AuthSession, error) {
	st := a.svc.Store()
	req, err := auth.SetupRequired(ctx, st)
	if err != nil {
		return AuthSession{}, err
	}
	out := AuthSession{SetupRequired: req, SetupNeedsCode: req && !isLocal(ctx), PasswordLoginDisabled: a.passwordOff()}
	if a.cfg.OIDC != nil {
		out.SSO = &SSOInfo{Name: a.cfg.OIDC.Name()}
	}
	if a.cfg.Telegram != nil {
		out.TelegramBot = a.cfg.Telegram.Username()
	}
	if _, err := a.svc.Owner(ctx); err == nil {
		out.HasHousehold = true
	}
	if actor, ok := auth.SessionActor(ctx, a.sm, st); ok {
		m, err := st.Q.GetMember(ctx, actor.MemberID)
		if err == nil {
			out.User = &SessionUser{ID: m.ID, Name: m.Name, Email: m.Email, Role: domain.Role(m.Role), HasPassword: m.PasswordHash != "", SSOHost: auth.IdentityHost(m)}
		}
	}
	return out, nil
}

func (a *authAPI) throttle(ctx context.Context, email string) error {
	if !a.ips.allow(clientIP(ctx)) || (email != "" && !a.emails.allow(strings.ToLower(strings.TrimSpace(email)))) {
		return huma.Error429TooManyRequests("too many attempts; wait a minute and try again")
	}
	return nil
}

func (a *authAPI) signIn(ctx context.Context, memberID string) (*resp[AuthSession], error) {
	if err := auth.StartSession(ctx, a.sm, a.svc.Store(), memberID); err != nil {
		return nil, httpError(err)
	}
	// The session is written when the response is; read it back from ctx.
	s, err := a.session(ctx)
	if err != nil {
		return nil, httpError(err)
	}
	return &resp[AuthSession]{Body: s}, nil
}

func (a *authAPI) member(ctx context.Context) (app.Actor, error) {
	actor, ok := actorFrom(ctx)
	if !ok || actor.Kind != app.ActorMember {
		return app.Actor{}, huma.Error401Unauthorized("sign in first")
	}
	return actor, nil
}

func authOp(id, method, path, summary string) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: "/api/v1/auth" + path, Summary: summary, Tags: []string{"Auth"}}
}

type empty struct{}

func registerAuth(api huma.API, a *authAPI) {
	huma.Register(api, authOp("auth_session", http.MethodGet, "/session", "Current session and setup state"),
		func(ctx context.Context, _ *struct{}) (*resp[AuthSession], error) {
			s, err := a.session(ctx)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[AuthSession]{Body: s}, nil
		})

	huma.Register(api, authOp("auth_setup", http.MethodPost, "/setup", "Create the first account"),
		func(ctx context.Context, in *struct{ Body auth.SetupInput }) (*resp[AuthSession], error) {
			if err := a.passwordRefused(); err != nil {
				return nil, err
			}
			if err := a.throttle(ctx, ""); err != nil {
				return nil, err
			}
			actor, err := auth.Setup(ctx, a.svc, in.Body, isLocal(ctx))
			if err != nil {
				return nil, httpError(err)
			}
			return a.signIn(ctx, actor.MemberID)
		})

	type loginIn struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	huma.Register(api, authOp("auth_login", http.MethodPost, "/login", "Sign in"),
		func(ctx context.Context, in *struct{ Body loginIn }) (*resp[AuthSession], error) {
			if err := a.passwordRefused(); err != nil {
				return nil, err
			}
			if err := a.throttle(ctx, in.Body.Email); err != nil {
				return nil, err
			}
			m, err := auth.Login(ctx, a.svc.Store(), in.Body.Email, in.Body.Password)
			if errors.Is(err, auth.ErrBadCredentials) {
				return nil, huma.Error401Unauthorized(err.Error())
			}
			if err != nil {
				return nil, httpError(err)
			}
			return a.signIn(ctx, m.ID)
		})

	huma.Register(api, authOp("auth_logout", http.MethodPost, "/logout", "Sign out"),
		func(ctx context.Context, _ *struct{}) (*resp[empty], error) {
			if err := auth.EndSession(ctx, a.sm); err != nil {
				return nil, httpError(err)
			}
			return &resp[empty]{}, nil
		})

	type passwordIn struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	huma.Register(api, authOp("auth_change_password", http.MethodPost, "/password", "Change your password (signs out your other sessions)"),
		func(ctx context.Context, in *struct{ Body passwordIn }) (*resp[empty], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			if err := a.throttle(ctx, ""); err != nil {
				return nil, err
			}
			if err := auth.ChangePassword(ctx, a.svc.Store(), actor.MemberID, in.Body.Current, in.Body.New); err != nil {
				return nil, httpError(err)
			}
			if err := auth.RefreshSession(ctx, a.sm, a.svc.Store(), actor.MemberID); err != nil {
				return nil, httpError(err)
			}
			return &resp[empty]{}, nil
		})

	type inviteIn struct {
		Role domain.Role `json:"role" enum:"owner,member"`
	}
	type inviteOut struct {
		Code        string    `json:"code"`
		Path        string    `json:"path" doc:"web path to open, e.g. /join/abc…; only useful if the invited person can reach this server"`
		TelegramURL string    `json:"telegramUrl,omitempty" doc:"t.me link that joins from Telegram, from anywhere; present when the bot runs"`
		ExpiresAt   time.Time `json:"expiresAt"`
	}
	huma.Register(api, authOp("auth_create_invite", http.MethodPost, "/invites", "Invite someone to the household (owners only)"),
		func(ctx context.Context, in *struct{ Body inviteIn }) (*resp[inviteOut], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			code, exp, err := auth.CreateInvite(ctx, a.svc.Store(), actor, in.Body.Role)
			if err != nil {
				return nil, httpError(err)
			}
			out := inviteOut{Code: code, Path: "/join/" + code, ExpiresAt: exp}
			if a.cfg.Telegram != nil {
				if bot := a.cfg.Telegram.Username(); bot != "" {
					out.TelegramURL = "https://t.me/" + bot + "?start=join-" + code
				}
			}
			return &resp[inviteOut]{Body: out}, nil
		})

	huma.Register(api, authOp("auth_get_invite", http.MethodGet, "/invites/{code}", "What an invite is for"),
		func(ctx context.Context, in *struct {
			Code string `path:"code"`
		}) (*resp[auth.InviteInfo], error) {
			if err := a.throttle(ctx, ""); err != nil {
				return nil, err
			}
			info, err := auth.LookupInvite(ctx, a.svc.Store(), in.Code)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[auth.InviteInfo]{Body: info}, nil
		})

	huma.Register(api, authOp("auth_join", http.MethodPost, "/join", "Accept an invite and create your account"),
		func(ctx context.Context, in *struct{ Body auth.JoinInput }) (*resp[AuthSession], error) {
			if err := a.passwordRefused(); err != nil {
				return nil, err
			}
			if err := a.throttle(ctx, ""); err != nil {
				return nil, err
			}
			actor, err := auth.Join(ctx, a.svc.Store(), in.Body)
			if err != nil {
				return nil, httpError(err)
			}
			return a.signIn(ctx, actor.MemberID)
		})

	type tokensOut struct {
		Tokens []auth.TokenInfo `json:"tokens"`
	}
	huma.Register(api, authOp("auth_list_tokens", http.MethodGet, "/tokens", "Your API tokens"),
		func(ctx context.Context, _ *struct{}) (*resp[tokensOut], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			list, err := auth.ListTokens(ctx, a.svc.Store(), actor.MemberID)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[tokensOut]{Body: tokensOut{Tokens: list}}, nil
		})

	type tokenIn struct {
		Name  string `json:"name" minLength:"1" maxLength:"60"`
		Scope string `json:"scope" enum:"read,write,trusted" doc:"read: look only; write: change with confirmations; trusted: change without asking"`
	}
	type tokenOut struct {
		Token string `json:"token" doc:"shown only once"`
	}
	huma.Register(api, authOp("auth_create_token", http.MethodPost, "/tokens", "Create an API token for an AI agent or script"),
		func(ctx context.Context, in *struct{ Body tokenIn }) (*resp[tokenOut], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			tok, err := auth.CreateToken(ctx, a.svc.Store(), actor.MemberID, strings.TrimSpace(in.Body.Name), app.Scope(in.Body.Scope))
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[tokenOut]{Body: tokenOut{Token: tok}}, nil
		})

	type idIn struct {
		ID string `json:"id"`
	}
	huma.Register(api, authOp("auth_revoke_token", http.MethodPost, "/tokens/revoke", "Revoke an API token"),
		func(ctx context.Context, in *struct{ Body idIn }) (*resp[empty], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			if err := auth.RevokeToken(ctx, a.svc.Store(), actor.MemberID, in.Body.ID); err != nil {
				return nil, httpError(err)
			}
			return &resp[empty]{}, nil
		})

	type linksOut struct {
		Links []auth.LinkInfo `json:"links"`
	}
	huma.Register(api, authOp("auth_list_links", http.MethodGet, "/links", "Your linked chat accounts and notification destinations"),
		func(ctx context.Context, _ *struct{}) (*resp[linksOut], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			list, err := auth.ListLinks(ctx, a.svc.Store(), actor.MemberID)
			if err != nil {
				return nil, httpError(err)
			}
			for i := range list {
				list[i] = a.describe(ctx, list[i])
			}
			return &resp[linksOut]{Body: linksOut{Links: list}}, nil
		})

	type linkCodeOut struct {
		Code    string    `json:"code"`
		Command string    `json:"command" doc:"message to send to the bot"`
		URL     string    `json:"url,omitempty" doc:"deep link that opens the bot with the code, when the bot is known"`
		Expires time.Time `json:"expiresAt"`
	}
	huma.Register(api, authOp("auth_link_telegram", http.MethodPost, "/links/telegram", "Get a one-time code to link Telegram"),
		func(ctx context.Context, _ *struct{}) (*resp[linkCodeOut], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			code, err := auth.CreateLinkCode(ctx, a.svc.Store(), actor.MemberID, "telegram")
			if err != nil {
				return nil, httpError(err)
			}
			out := linkCodeOut{Code: code, Command: "/start " + code, Expires: time.Now().Add(10 * time.Minute)}
			if a.cfg.Telegram != nil {
				if bot := a.cfg.Telegram.Username(); bot != "" {
					out.URL = "https://t.me/" + bot + "?start=" + code
				}
			}
			return &resp[linkCodeOut]{Body: out}, nil
		})

	huma.Register(api, authOp("auth_unlink", http.MethodPost, "/links/unlink", "Unlink a chat account or remove a destination"),
		func(ctx context.Context, in *struct{ Body idIn }) (*resp[empty], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			link, err := auth.Link(ctx, a.svc.Store(), actor.MemberID, in.Body.ID)
			if err != nil {
				return nil, httpError(err)
			}
			if err := auth.Unlink(ctx, a.svc.Store(), actor.MemberID, in.Body.ID); err != nil {
				return nil, httpError(err)
			}
			if a.cfg.Channels != nil {
				_ = a.cfg.Channels.Forget(ctx, link.Channel, link.ExternalID)
			}
			return &resp[empty]{}, nil
		})
}
