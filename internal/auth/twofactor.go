package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Two-factor sign-in (ADR 0025): after a correct password, a code from an
// authenticator app or a one-time backup code. Only password sign-in asks
// for it; single sign-on leaves it to the identity provider, and API and
// OAuth tokens are not affected.

// ErrBadCode is returned for a wrong, expired or already used code.
var ErrBadCode = errors.New("the code is wrong or was already used")

// BackupCodeCount is how many backup codes an enrolment gives.
const BackupCodeCount = 10

// TwoFactorStatus is what the account settings show.
type TwoFactorStatus struct {
	Enabled         bool `json:"enabled"`
	BackupCodesLeft int  `json:"backupCodesLeft" doc:"unused backup codes"`
}

// TwoFactorSetup is what an authenticator app needs. It is shown during
// enrolment only; the secret is never returned again.
type TwoFactorSetup struct {
	Secret string `json:"secret" doc:"base32 secret to type into the app"`
	URI    string `json:"uri" doc:"otpauth:// link, shown as a QR code"`
}

// TwoFactorEnabled reports whether a member must give a code after the password.
func TwoFactorEnabled(ctx context.Context, st *store.Store, memberID string) (bool, error) {
	t, err := st.Q.GetTOTP(ctx, memberID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && t.EnabledAt != nil, err
}

// GetTwoFactorStatus returns a member's two-factor state.
func GetTwoFactorStatus(ctx context.Context, st *store.Store, memberID string) (TwoFactorStatus, error) {
	on, err := TwoFactorEnabled(ctx, st, memberID)
	if err != nil || !on {
		return TwoFactorStatus{}, err
	}
	n, err := st.Q.CountBackupCodesLeft(ctx, memberID)
	return TwoFactorStatus{Enabled: true, BackupCodesLeft: int(n)}, err
}

// BeginTwoFactor checks the password and starts an enrolment with a new
// secret. Nothing changes at sign-in until EnableTwoFactor confirms a code.
func BeginTwoFactor(ctx context.Context, st *store.Store, memberID, password string) (TwoFactorSetup, error) {
	m, err := st.Q.GetMember(ctx, memberID)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	if m.PasswordHash == "" {
		return TwoFactorSetup{}, fmt.Errorf("%w: two-factor codes protect password sign-in, and this account has no password; your identity provider handles it", domain.ErrValidation)
	}
	if !checkPassword(password, m.PasswordHash) {
		return TwoFactorSetup{}, fmt.Errorf("%w: the password is wrong", domain.ErrValidation)
	}
	secret := newTOTPSecret()
	n, err := st.Q.PutPendingTOTP(ctx, db.PutPendingTOTPParams{MemberID: m.ID, Secret: secret, CreatedAt: store.FormatTime(time.Now())})
	if err != nil {
		return TwoFactorSetup{}, err
	}
	if n == 0 {
		return TwoFactorSetup{}, fmt.Errorf("%w: two-factor sign-in is already on", domain.ErrConflict)
	}
	account := m.Email
	if account == "" {
		account = m.Name
	}
	return TwoFactorSetup{Secret: secret, URI: totpURI(secret, account)}, nil
}

// EnableTwoFactor confirms an enrolment with a first code from the app and
// returns the backup codes, shown once. Every session of the member is
// signed out; the caller renews its own (RefreshSession).
func EnableTwoFactor(ctx context.Context, st *store.Store, actor app.Actor, code string) ([]string, error) {
	t, err := st.Q.GetTOTP(ctx, actor.MemberID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && t.EnabledAt != nil) {
		return nil, fmt.Errorf("%w: start again from the account settings", domain.ErrConflict)
	}
	if err != nil {
		return nil, err
	}
	if time.Since(store.ParseTime(t.CreatedAt)) > time.Hour {
		return nil, fmt.Errorf("%w: this setup has expired; start again", domain.ErrConflict)
	}
	step, ok := checkTOTP(t.Secret, normalizeCode(code), time.Now(), 0)
	if !ok {
		return nil, fmt.Errorf("%w: the code does not match; check the time on your phone and try the next one", domain.ErrValidation)
	}
	var codes []string
	err = st.Tx(ctx, func(q *db.Queries) error {
		n, err := q.EnableTOTP(ctx, db.EnableTOTPParams{EnabledAt: store.Ptr(store.FormatTime(time.Now())), Step: int64(step), MemberID: actor.MemberID, Secret: t.Secret}) //nolint:gosec // steps fit in int64
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("%w: start again from the account settings", domain.ErrConflict)
		}
		if codes, err = replaceBackupCodes(ctx, q, actor.MemberID); err != nil {
			return err
		}
		if err := q.BumpSessionVersion(ctx, actor.MemberID); err != nil {
			return err
		}
		return audit(ctx, q, actor, "two_factor.enable", actor.MemberID)
	})
	return codes, err
}

// RegenerateBackupCodes replaces every backup code after checking the
// password. The old ones stop working.
func RegenerateBackupCodes(ctx context.Context, st *store.Store, actor app.Actor, password string) ([]string, error) {
	m, err := st.Q.GetMember(ctx, actor.MemberID)
	if err != nil {
		return nil, err
	}
	if !checkPassword(password, m.PasswordHash) {
		return nil, fmt.Errorf("%w: the password is wrong", domain.ErrValidation)
	}
	if on, err := TwoFactorEnabled(ctx, st, m.ID); err != nil {
		return nil, err
	} else if !on {
		return nil, fmt.Errorf("%w: two-factor sign-in is off", domain.ErrConflict)
	}
	var codes []string
	err = st.Tx(ctx, func(q *db.Queries) error {
		if codes, err = replaceBackupCodes(ctx, q, m.ID); err != nil {
			return err
		}
		return audit(ctx, q, actor, "two_factor.backup_codes", m.ID)
	})
	return codes, err
}

// DisableTwoFactor turns two-factor sign-in off for the actor, with the
// password and a code (or a backup code).
func DisableTwoFactor(ctx context.Context, st *store.Store, actor app.Actor, password, code string) error {
	m, err := st.Q.GetMember(ctx, actor.MemberID)
	if err != nil {
		return err
	}
	if !checkPassword(password, m.PasswordHash) {
		return fmt.Errorf("%w: the password is wrong", domain.ErrValidation)
	}
	if err := CheckSecondFactor(ctx, st, m.ID, code); err != nil {
		if errors.Is(err, ErrBadCode) {
			return fmt.Errorf("%w: %w", domain.ErrValidation, err)
		}
		return err
	}
	return clearTwoFactor(ctx, st, actor, m.ID)
}

// ResetTwoFactor turns another member's two-factor sign-in off, for someone
// who lost their phone and their backup codes. Owners only.
func ResetTwoFactor(ctx context.Context, st *store.Store, actor app.Actor, memberID string) error {
	if err := requireOwner(ctx, st, actor); err != nil {
		return err
	}
	m, err := memberOf(ctx, st, actor, memberID)
	if err != nil {
		return err
	}
	if on, err := TwoFactorEnabled(ctx, st, m.ID); err != nil {
		return err
	} else if !on {
		return fmt.Errorf("%w: %s does not use two-factor sign-in", domain.ErrConflict, m.Name)
	}
	return clearTwoFactor(ctx, st, actor, m.ID)
}

// ResetTwoFactorFromServer turns a member's two-factor sign-in off from the
// command line, for lock-out recovery. It reports whether it was on.
func ResetTwoFactorFromServer(ctx context.Context, st *store.Store, memberID string) (bool, error) {
	on, err := TwoFactorEnabled(ctx, st, memberID)
	if err != nil || !on {
		return false, err
	}
	m, err := st.Q.GetMember(ctx, memberID)
	if err != nil {
		return false, err
	}
	actor := app.Actor{Kind: app.ActorSystem, HouseholdID: m.HouseholdID, Label: "exhale user reset-2fa", Scope: app.ScopeTrusted}
	return true, clearTwoFactor(ctx, st, actor, memberID)
}

// TwoFactorMembers lists the members of the actor's household with
// two-factor sign-in on. Owners only.
func TwoFactorMembers(ctx context.Context, st *store.Store, actor app.Actor) ([]string, error) {
	if err := requireOwner(ctx, st, actor); err != nil {
		return nil, err
	}
	return st.Q.ListTwoFactorMembers(ctx, actor.HouseholdID)
}

// CheckSecondFactor accepts a code from the app (6 digits) or a backup code
// and uses it up: an app code's time step and a backup code never work again.
func CheckSecondFactor(ctx context.Context, st *store.Store, memberID, code string) error {
	t, err := st.Q.GetTOTP(ctx, memberID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && t.EnabledAt == nil) {
		return ErrBadCode
	}
	if err != nil {
		return err
	}
	code = normalizeCode(code)
	if isDigits(code) {
		step, ok := checkTOTP(t.Secret, code, time.Now(), uint64(t.LastStep)) //nolint:gosec // never negative
		if !ok {
			return ErrBadCode
		}
		n, err := st.Q.UseTOTPStep(ctx, db.UseTOTPStepParams{Step: int64(step), MemberID: memberID}) //nolint:gosec // steps fit in int64
		if err != nil {
			return err
		}
		if n != 1 { // another request used this code first
			return ErrBadCode
		}
		return nil
	}
	if len(code) != backupCodeLen {
		return ErrBadCode
	}
	n, err := st.Q.UseBackupCode(ctx, db.UseBackupCodeParams{UsedAt: store.Ptr(store.FormatTime(time.Now())), MemberID: memberID, CodeHash: Hash(code)})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrBadCode
	}
	return nil
}

const backupCodeLen = 10

// normalizeCode drops what people add when typing a code: spaces, dashes,
// capitals.
func normalizeCode(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '\t':
			return -1
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, strings.TrimSpace(s))
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// replaceBackupCodes stores BackupCodeCount new codes (hashed) and returns
// them as people read them: two groups of five characters.
func replaceBackupCodes(ctx context.Context, q *db.Queries, memberID string) ([]string, error) {
	if err := q.DeleteBackupCodes(ctx, memberID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, BackupCodeCount)
	for len(codes) < BackupCodeCount {
		c := Random(7)[:backupCodeLen] // 50 bits
		if isDigits(c) {
			continue // would read as an app code
		}
		if err := q.AddBackupCode(ctx, db.AddBackupCodeParams{MemberID: memberID, CodeHash: Hash(c)}); err != nil {
			return nil, err
		}
		codes = append(codes, c[:5]+"-"+c[5:])
	}
	return codes, nil
}

func clearTwoFactor(ctx context.Context, st *store.Store, actor app.Actor, memberID string) error {
	return st.Tx(ctx, func(q *db.Queries) error {
		if _, err := q.DeleteTOTP(ctx, memberID); err != nil {
			return err
		}
		if err := q.DeleteBackupCodes(ctx, memberID); err != nil {
			return err
		}
		action := "two_factor.disable"
		if actor.MemberID != memberID {
			action = "two_factor.reset"
		}
		return audit(ctx, q, actor, action, memberID)
	})
}

// audit records a two-factor change in the activity log. These changes
// cannot be undone from there.
func audit(ctx context.Context, q *db.Queries, actor app.Actor, action, memberID string) error {
	m, err := q.GetMember(ctx, memberID)
	if err != nil {
		return err
	}
	after, _ := json.Marshal(map[string]string{"member": m.Name})
	return q.CreateAudit(ctx, db.CreateAuditParams{
		ID: domain.NewID("aud"), At: store.FormatTime(time.Now()), ActorKind: string(actor.Kind),
		ActorID: actor.MemberID, ActorLabel: actor.Label, Action: action, Entity: "member",
		EntityID: memberID, After: string(after),
	})
}
