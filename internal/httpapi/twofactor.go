package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Two-factor sign-in (ADR 0025). A correct password for an account with
// two-factor on does not start a session: it returns a token for a pending
// login, which lives a few minutes in the server's memory and becomes a
// session only with a code. Like the other account endpoints, none of this
// is in the op catalog.

const (
	loginTwoFactorPath = "/api/v1/auth/login/two-factor"
	pendingLoginTTL    = 5 * time.Minute
	// pendingLoginTries is how many wrong codes one pending login takes
	// before the password must be entered again.
	pendingLoginTries = 3
	pendingLoginMax   = 1000
)

// codeRate throttles codes per member. A 6-digit code is easier to guess
// than a password: 5 at once, then one every 30 s. Per IP, codes take the
// sign-in numbers.
var codeRate = struct {
	every   time.Duration
	burst   int
	ipBurst int // with the sign-in rate (one every 6 s)
}{30 * time.Second, 5, 10}

// TwoFactorStep is the pending login a code finishes.
type TwoFactorStep struct {
	Token     string    `json:"token" doc:"send it back with the code; it works for a few minutes"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type pendingLogin struct {
	memberID string
	version  int64  // session_version when the password was checked
	password string // hash checked; a password change in between ends it
	expires  time.Time
	tries    int
}

// pendingLogins keeps logins waiting for a code. A restart only asks people
// to enter their password again.
type pendingLogins struct {
	mu sync.Mutex
	m  map[string]*pendingLogin
}

func (s *pendingLogins) put(m db.Member) (string, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]*pendingLogin{}
	}
	now := time.Now()
	if len(s.m) >= pendingLoginMax/2 {
		for k, v := range s.m {
			if v.expires.Before(now) {
				delete(s.m, k)
			}
		}
	}
	if len(s.m) >= pendingLoginMax {
		return "", time.Time{}, false
	}
	id := auth.Random(32)
	p := &pendingLogin{memberID: m.ID, version: m.SessionVersion, password: m.PasswordHash, expires: now.Add(pendingLoginTTL)}
	s.m[id] = p
	return id, p.expires, true
}

func (s *pendingLogins) get(id string) (pendingLogin, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[id]
	if !ok || p.expires.Before(time.Now()) {
		delete(s.m, id)
		return pendingLogin{}, false
	}
	return *p, true
}

// fail counts a wrong code; too many end the pending login.
func (s *pendingLogins) fail(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.m[id]; ok {
		p.tries++
		if p.tries >= pendingLoginTries {
			delete(s.m, id)
		}
	}
}

func (s *pendingLogins) drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}

var errLoginExpired = huma.NewError(http.StatusGone, "this sign-in has expired; enter your password again")

// askForCode returns the pending-login answer when m has two-factor on, and
// nil when the password alone signs in.
func (a *authAPI) askForCode(ctx context.Context, m db.Member) (*resp[AuthSession], error) {
	on, err := auth.TwoFactorEnabled(ctx, a.svc.Store(), m.ID)
	if err != nil {
		return nil, httpError(err)
	}
	if !on {
		return nil, nil
	}
	token, exp, ok := a.pending.put(m)
	if !ok {
		return nil, huma.Error429TooManyRequests("too many sign-ins in progress; try again in a few minutes")
	}
	s, err := a.session(ctx)
	if err != nil {
		return nil, httpError(err)
	}
	s.TwoFactor = &TwoFactorStep{Token: token, ExpiresAt: exp}
	return &resp[AuthSession]{Body: s}, nil
}

func (a *authAPI) throttleCode(ctx context.Context, memberID string) error {
	if !a.codeIPs.allow(clientIP(ctx)) || !a.codeMembers.allow(memberID) {
		return huma.Error429TooManyRequests("too many attempts; wait a minute and try again")
	}
	return nil
}

type backupCodesOut struct {
	BackupCodes []string `json:"backupCodes" doc:"one-time codes for when the phone is not at hand; shown only now"`
}

func registerTwoFactor(api huma.API, a *authAPI) {
	a.codeMembers.every, a.codeMembers.burst = codeRate.every, codeRate.burst
	a.codeIPs.every, a.codeIPs.burst = 6*time.Second, codeRate.ipBurst

	type loginCodeIn struct {
		Token string `json:"token"`
		Code  string `json:"code" maxLength:"32" doc:"6 digits from the authenticator app, or a backup code"`
	}
	huma.Register(api, authOp("auth_login_two_factor", http.MethodPost, "/login/two-factor", "Finish signing in with a two-factor code"),
		func(ctx context.Context, in *struct{ Body loginCodeIn }) (*resp[AuthSession], error) {
			if err := a.passwordRefused(); err != nil {
				return nil, err
			}
			if !a.codeIPs.allow(clientIP(ctx)) {
				return nil, huma.Error429TooManyRequests("too many attempts; wait a minute and try again")
			}
			p, ok := a.pending.get(in.Body.Token)
			if !ok {
				return nil, errLoginExpired
			}
			if !a.codeMembers.allow(p.memberID) {
				return nil, huma.Error429TooManyRequests("too many attempts; wait a minute and try again")
			}
			st := a.svc.Store()
			m, err := st.Q.GetMember(ctx, p.memberID)
			if err != nil || m.SessionVersion != p.version || m.PasswordHash != p.password {
				a.pending.drop(in.Body.Token)
				return nil, errLoginExpired
			}
			if err := auth.CheckSecondFactor(ctx, st, m.ID, in.Body.Code); errors.Is(err, auth.ErrBadCode) {
				a.pending.fail(in.Body.Token)
				a.log().Info("two-factor code refused", "member", m.ID, "ip", clientIP(ctx))
				return nil, huma.Error401Unauthorized(err.Error())
			} else if err != nil {
				return nil, httpError(err)
			}
			a.pending.drop(in.Body.Token)
			return a.signIn(ctx, m.ID)
		})

	huma.Register(api, authOp("auth_two_factor", http.MethodGet, "/two-factor", "Your two-factor sign-in"),
		func(ctx context.Context, _ *struct{}) (*resp[auth.TwoFactorStatus], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			s, err := auth.GetTwoFactorStatus(ctx, a.svc.Store(), actor.MemberID)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[auth.TwoFactorStatus]{Body: s}, nil
		})

	type passwordOnly struct {
		Password string `json:"password"`
	}
	huma.Register(api, authOp("auth_two_factor_setup", http.MethodPost, "/two-factor/setup", "Start turning on two-factor sign-in: a new secret for the authenticator app"),
		func(ctx context.Context, in *struct{ Body passwordOnly }) (*resp[auth.TwoFactorSetup], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			if err := a.throttle(ctx, ""); err != nil {
				return nil, err
			}
			s, err := auth.BeginTwoFactor(ctx, a.svc.Store(), actor.MemberID, in.Body.Password)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[auth.TwoFactorSetup]{Body: s}, nil
		})

	type codeOnly struct {
		Code string `json:"code" maxLength:"32"`
	}
	huma.Register(api, authOp("auth_two_factor_enable", http.MethodPost, "/two-factor/enable", "Confirm the first code and turn two-factor sign-in on (signs out your other sessions)"),
		func(ctx context.Context, in *struct{ Body codeOnly }) (*resp[backupCodesOut], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			if err := a.throttleCode(ctx, actor.MemberID); err != nil {
				return nil, err
			}
			codes, err := auth.EnableTwoFactor(ctx, a.svc.Store(), actor, in.Body.Code)
			if err != nil {
				return nil, httpError(err)
			}
			if err := auth.RefreshSession(ctx, a.sm, a.svc.Store(), actor.MemberID); err != nil {
				return nil, httpError(err)
			}
			return &resp[backupCodesOut]{Body: backupCodesOut{BackupCodes: codes}}, nil
		})

	huma.Register(api, authOp("auth_two_factor_backup_codes", http.MethodPost, "/two-factor/backup-codes", "Replace your backup codes"),
		func(ctx context.Context, in *struct{ Body passwordOnly }) (*resp[backupCodesOut], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			if err := a.throttle(ctx, ""); err != nil {
				return nil, err
			}
			codes, err := auth.RegenerateBackupCodes(ctx, a.svc.Store(), actor, in.Body.Password)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[backupCodesOut]{Body: backupCodesOut{BackupCodes: codes}}, nil
		})

	type disableIn struct {
		Password string `json:"password"`
		Code     string `json:"code" maxLength:"32" doc:"6 digits from the authenticator app, or a backup code"`
	}
	huma.Register(api, authOp("auth_two_factor_disable", http.MethodPost, "/two-factor/disable", "Turn two-factor sign-in off"),
		func(ctx context.Context, in *struct{ Body disableIn }) (*resp[empty], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			if err := a.throttle(ctx, ""); err != nil {
				return nil, err
			}
			if err := a.throttleCode(ctx, actor.MemberID); err != nil {
				return nil, err
			}
			if err := auth.DisableTwoFactor(ctx, a.svc.Store(), actor, in.Body.Password, in.Body.Code); err != nil {
				return nil, httpError(err)
			}
			return &resp[empty]{}, nil
		})

	// Member management: an owner sees who uses it and can turn it off for
	// someone locked out.
	type twoFactorMembersOut struct {
		Members []string `json:"members" doc:"ids of the members with two-factor sign-in on"`
	}
	huma.Register(api, huma.Operation{OperationID: "members_two_factor", Method: http.MethodGet, Path: "/api/v1/members/two-factor", Summary: "Members with two-factor sign-in on (owners only)", Tags: []string{"Members"}},
		func(ctx context.Context, _ *struct{}) (*resp[twoFactorMembersOut], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			ids, err := auth.TwoFactorMembers(ctx, a.svc.Store(), actor)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[twoFactorMembersOut]{Body: twoFactorMembersOut{Members: ids}}, nil
		})

	type resetIn struct {
		ID string `json:"id" doc:"member whose two-factor sign-in to turn off"`
	}
	huma.Register(api, huma.Operation{OperationID: "members_reset_two_factor", Method: http.MethodPost, Path: "/api/v1/members/two-factor/reset", Summary: "Turn off another member's two-factor sign-in (owners only)", Tags: []string{"Members"}},
		func(ctx context.Context, in *struct{ Body resetIn }) (*resp[MemberDone], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			if in.Body.ID == actor.MemberID {
				return nil, huma.Error422UnprocessableEntity("turn your own off from your account settings")
			}
			if err := auth.ResetTwoFactor(ctx, a.svc.Store(), actor, in.Body.ID); err != nil {
				return nil, httpError(err)
			}
			return &resp[MemberDone]{Body: MemberDone{OK: true}}, nil
		})
}
