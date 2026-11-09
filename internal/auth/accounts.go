package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alexedwards/argon2id"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Web accounts (ADR 0006): passwords hashed with argon2id, a one-time setup
// code for the first account, and invite links for the rest of the
// household.

// ErrBadCredentials is returned for a wrong email or password. It does not
// say which one was wrong.
var ErrBadCredentials = errors.New("wrong email or password")

// MinPasswordLength follows NIST SP 800-63B: length over composition rules.
const MinPasswordLength = 10

// argon2id parameters: 64 MiB, one pass, two lanes (OWASP 2025 baseline).
var params = &argon2id.Params{Memory: 64 * 1024, Iterations: 1, Parallelism: 2, SaltLength: 16, KeyLength: 32}

// dummyHash equalizes the time of logins for unknown emails.
var dummyHash, _ = argon2id.CreateHash("exhale-dummy-password", params)

// HashPassword returns the stored form of a password.
func HashPassword(p string) (string, error) { return argon2id.CreateHash(p, params) }

// checkPassword reports whether p matches hash, in constant time.
func checkPassword(p, hash string) bool {
	if hash == "" {
		_, _ = argon2id.ComparePasswordAndHash(p, dummyHash)
		return false
	}
	ok, err := argon2id.ComparePasswordAndHash(p, hash)
	return err == nil && ok
}

// ValidatePassword checks a new password.
func ValidatePassword(p string) error {
	switch n := utf8.RuneCountInString(p); {
	case n < MinPasswordLength:
		return fmt.Errorf("%w: the password needs at least %d characters", domain.ErrValidation, MinPasswordLength)
	case n > 256:
		return fmt.Errorf("%w: the password is too long", domain.ErrValidation)
	}
	return nil
}

// NormalizeEmail validates an email address and lowercases it.
func NormalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || !strings.Contains(email, "@") {
		return "", fmt.Errorf("%w: %q is not a valid email address", domain.ErrValidation, email)
	}
	return email, nil
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return "", fmt.Errorf("%w: a name of 1 to 80 characters is required", domain.ErrValidation)
	}
	return name, nil
}

// SetupRequired reports whether nobody can sign in yet, so the setup wizard
// must run.
func SetupRequired(ctx context.Context, st *store.Store) (bool, error) {
	n, err := st.Q.CountMembersWithPassword(ctx)
	return n == 0, err
}

const (
	settingSetupCode    = "setup_code_hash"
	settingSetupExpires = "setup_code_expires"
)

// NewSetupCode issues the one-time code the setup wizard asks for when it is
// opened from another machine. It replaces any earlier code and is valid for
// 24 hours.
func NewSetupCode(ctx context.Context, st *store.Store) (string, error) {
	code := Random(10)
	if err := st.Q.PutSetting(ctx, db.PutSettingParams{Key: settingSetupCode, Value: Hash(code)}); err != nil {
		return "", err
	}
	exp := store.FormatTime(time.Now().Add(24 * time.Hour))
	return code, st.Q.PutSetting(ctx, db.PutSettingParams{Key: settingSetupExpires, Value: exp})
}

// HasSetupCode reports whether an unexpired setup code exists, so a restart
// does not invalidate a code someone already has.
func HasSetupCode(ctx context.Context, st *store.Store) bool {
	h, err := st.Q.GetSetting(ctx, settingSetupCode)
	if err != nil || h == "" {
		return false
	}
	exp, err := st.Q.GetSetting(ctx, settingSetupExpires)
	return err == nil && store.ParseTime(exp).After(time.Now())
}

func checkSetupCode(ctx context.Context, st *store.Store, code string) bool {
	h, err := st.Q.GetSetting(ctx, settingSetupCode)
	if err != nil || h == "" {
		return false
	}
	exp, err := st.Q.GetSetting(ctx, settingSetupExpires)
	if err != nil || store.ParseTime(exp).Before(time.Now()) {
		return false
	}
	return Hash(strings.TrimSpace(code)) == h
}

// SetupInput creates the first account and, if needed, the household.
type SetupInput struct {
	Code          string `json:"code,omitempty" doc:"setup code printed in the server log; not needed from the server's own machine"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	Password      string `json:"password"`
	HouseholdName string `json:"householdName,omitempty"`
	Currency      string `json:"currency,omitempty"`
	Timezone      string `json:"timezone,omitempty"`
	Locale        string `json:"locale,omitempty"`
}

// Setup creates the owner account. On a fresh instance it creates the
// household too; on one made with `exhale init` it gives the existing owner
// a login. local skips the setup code for requests from the server's own
// machine.
func Setup(ctx context.Context, svc *app.Service, in SetupInput, local bool) (app.Actor, error) {
	st := svc.Store()
	if req, err := SetupRequired(ctx, st); err != nil || !req {
		return app.Actor{}, fmt.Errorf("%w: setup is already done; sign in instead", domain.ErrConflict)
	}
	if !local && !checkSetupCode(ctx, st, in.Code) {
		return app.Actor{}, fmt.Errorf("%w: the setup code is wrong or expired; see the server log or run `exhale setup-code`", domain.ErrValidation)
	}
	name, err := validateName(in.Name)
	if err != nil {
		return app.Actor{}, err
	}
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return app.Actor{}, err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return app.Actor{}, err
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return app.Actor{}, err
	}
	owner, err := svc.Owner(ctx)
	if errors.Is(err, domain.ErrNotFound) {
		owner, err = svc.Bootstrap(ctx, app.BootstrapInput{
			HouseholdName: strings.TrimSpace(in.HouseholdName), OwnerName: name, OwnerEmail: email,
			Currency: in.Currency, Timezone: in.Timezone, Locale: in.Locale,
		})
	}
	if err != nil {
		return app.Actor{}, err
	}
	if err := st.Q.SetMemberCredentials(ctx, db.SetMemberCredentialsParams{Name: name, Email: email, PasswordHash: hash, ID: owner.MemberID}); err != nil {
		return app.Actor{}, err
	}
	_ = st.Q.PutSetting(ctx, db.PutSettingParams{Key: settingSetupCode, Value: ""})
	return memberActor(ctx, st, owner.MemberID)
}

// Login checks an email and password and returns the member.
func Login(ctx context.Context, st *store.Store, email, password string) (db.Member, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	m, err := st.Q.MemberByEmail(ctx, email)
	if err != nil {
		checkPassword(password, "")
		return db.Member{}, ErrBadCredentials
	}
	if !checkPassword(password, m.PasswordHash) {
		return db.Member{}, ErrBadCredentials
	}
	return m, nil
}

// ChangePassword replaces a member's password after checking the current
// one. Every other session of the member is signed out.
func ChangePassword(ctx context.Context, st *store.Store, memberID, current, next string) error {
	m, err := st.Q.GetMember(ctx, memberID)
	if err != nil {
		return err
	}
	if !checkPassword(current, m.PasswordHash) {
		return fmt.Errorf("%w: the current password is wrong", domain.ErrValidation)
	}
	return SetPassword(ctx, st, memberID, next)
}

// SetPassword sets a password without checking the old one (CLI recovery)
// and signs the member out everywhere.
func SetPassword(ctx context.Context, st *store.Store, memberID, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return st.Q.SetMemberPassword(ctx, db.SetMemberPasswordParams{PasswordHash: hash, ID: memberID})
}

// CreateInvite returns a one-time invite code (valid 7 days) to join the
// actor's household. Only owners can invite.
func CreateInvite(ctx context.Context, st *store.Store, actor app.Actor, role domain.Role) (string, time.Time, error) {
	if err := requireOwner(ctx, st, actor); err != nil {
		return "", time.Time{}, err
	}
	if role != domain.RoleOwner && role != domain.RoleMember {
		return "", time.Time{}, fmt.Errorf("%w: role must be owner or member", domain.ErrValidation)
	}
	code := Random(16)
	exp := time.Now().Add(7 * 24 * time.Hour)
	err := st.Q.CreateInvite(ctx, db.CreateInviteParams{CodeHash: Hash(code), HouseholdID: actor.HouseholdID, Role: string(role), CreatedBy: actor.MemberID, ExpiresAt: store.FormatTime(exp)})
	return code, exp, err
}

// InviteInfo describes a pending invite to the person opening it.
type InviteInfo struct {
	HouseholdName string `json:"householdName"`
	InvitedBy     string `json:"invitedBy"`
	Role          string `json:"role"`
}

// LookupInvite returns what an invite is for.
func LookupInvite(ctx context.Context, st *store.Store, code string) (InviteInfo, error) {
	inv, err := st.Q.GetInvite(ctx, db.GetInviteParams{CodeHash: Hash(strings.TrimSpace(code)), ExpiresAt: store.FormatTime(time.Now())})
	if err != nil {
		return InviteInfo{}, fmt.Errorf("%w: this invite has expired or was already used", domain.ErrNotFound)
	}
	h, err := st.Q.GetHousehold(ctx, inv.HouseholdID)
	if err != nil {
		return InviteInfo{}, err
	}
	by, _ := st.Q.GetMember(ctx, inv.CreatedBy)
	return InviteInfo{HouseholdName: h.Name, InvitedBy: by.Name, Role: inv.Role}, nil
}

// JoinInput accepts an invite.
type JoinInput struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Join creates a member from an invite.
func Join(ctx context.Context, st *store.Store, in JoinInput) (app.Actor, error) {
	name, err := validateName(in.Name)
	if err != nil {
		return app.Actor{}, err
	}
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return app.Actor{}, err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return app.Actor{}, err
	}
	if _, err := st.Q.MemberByEmail(ctx, email); err == nil {
		return app.Actor{}, fmt.Errorf("%w: an account with this email already exists", domain.ErrConflict)
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return app.Actor{}, err
	}
	codeHash := Hash(strings.TrimSpace(in.Code))
	id := domain.NewID(domain.PrefixMember)
	err = st.Tx(ctx, func(q *db.Queries) error {
		inv, err := q.GetInvite(ctx, db.GetInviteParams{CodeHash: codeHash, ExpiresAt: store.FormatTime(time.Now())})
		if err != nil {
			return fmt.Errorf("%w: this invite has expired or was already used", domain.ErrNotFound)
		}
		if n, err := q.UseInvite(ctx, db.UseInviteParams{UsedAt: store.Ptr(store.FormatTime(time.Now())), CodeHash: codeHash}); err != nil || n != 1 {
			return fmt.Errorf("%w: this invite was already used", domain.ErrConflict)
		}
		return q.CreateMember(ctx, db.CreateMemberParams{ID: id, HouseholdID: inv.HouseholdID, Name: name, Email: email, Role: inv.Role, PasswordHash: hash, CreatedAt: store.FormatTime(time.Now())})
	})
	if err != nil {
		return app.Actor{}, err
	}
	return memberActor(ctx, st, id)
}

// JoinFromChat accepts an invite from a chat app: it creates the member
// (named after the chat profile, no web login yet) and links the chat. It
// returns the member and the household name.
func JoinFromChat(ctx context.Context, st *store.Store, code, channel, externalID, name string) (app.Actor, string, error) {
	if _, err := st.Q.MemberForChannel(ctx, db.MemberForChannelParams{Channel: channel, ExternalID: externalID}); err == nil {
		return app.Actor{}, "", fmt.Errorf("%w: this chat is already linked", domain.ErrConflict)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Member"
	}
	if utf8.RuneCountInString(name) > 80 {
		name = string([]rune(name)[:80])
	}
	codeHash := Hash(code)
	id := domain.NewID(domain.PrefixMember)
	var household string
	err := st.Tx(ctx, func(q *db.Queries) error {
		now := store.FormatTime(time.Now())
		inv, err := q.GetInvite(ctx, db.GetInviteParams{CodeHash: codeHash, ExpiresAt: now})
		if err != nil {
			return fmt.Errorf("%w: this invite has expired or was already used", domain.ErrNotFound)
		}
		if n, err := q.UseInvite(ctx, db.UseInviteParams{UsedAt: store.Ptr(now), CodeHash: codeHash}); err != nil || n != 1 {
			return fmt.Errorf("%w: this invite was already used", domain.ErrNotFound)
		}
		h, err := q.GetHousehold(ctx, inv.HouseholdID)
		if err != nil {
			return err
		}
		household = h.Name
		if err := q.CreateMember(ctx, db.CreateMemberParams{ID: id, HouseholdID: inv.HouseholdID, Name: name, Role: inv.Role, CreatedAt: now}); err != nil {
			return err
		}
		return q.LinkChannel(ctx, db.LinkChannelParams{ID: domain.NewID("lnk"), MemberID: id, Channel: channel, ExternalID: externalID, CreatedAt: now})
	})
	if err != nil {
		return app.Actor{}, "", err
	}
	a, err := memberActor(ctx, st, id)
	return a, household, err
}

func requireOwner(ctx context.Context, st *store.Store, actor app.Actor) error {
	m, err := st.Q.GetMember(ctx, actor.MemberID)
	if err != nil || m.Role != string(domain.RoleOwner) || actor.Kind != app.ActorMember {
		return fmt.Errorf("%w: only a household owner can do this", app.ErrForbidden)
	}
	return nil
}

// memberActor returns the web actor of a member.
func memberActor(ctx context.Context, st *store.Store, id string) (app.Actor, error) {
	m, err := st.Q.GetMember(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return app.Actor{}, ErrUnauthorized
	}
	if err != nil {
		return app.Actor{}, err
	}
	return app.MemberActor(store.MemberFromRow(m), app.ScopeTrusted), nil
}
