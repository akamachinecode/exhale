// Package auth resolves who is calling: API tokens for scripts and AI agents,
// one-time codes for linking chat accounts, and web accounts and sessions
// (ADR 0006).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// TokenPrefix marks Exhale personal access tokens (secret-scanning friendly).
const TokenPrefix = "exhale_pat_"

// ErrUnauthorized is returned for missing or invalid credentials.
var ErrUnauthorized = errors.New("unauthorized")

var enc = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// Hash returns the stored form of a secret. Tokens are high-entropy random
// values, so a fast hash is the right tool (passwords use argon2id).
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Random returns n random bytes encoded in lowercase base32.
func Random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return enc.EncodeToString(b)
}

// CreateToken issues a token for a member and returns its plaintext once.
func CreateToken(ctx context.Context, st *store.Store, memberID, name string, scope app.Scope) (string, error) {
	switch scope {
	case app.ScopeRead, app.ScopeWrite, app.ScopeTrusted:
	default:
		return "", fmt.Errorf("%w: unknown scope %q", domain.ErrValidation, scope)
	}
	plain := TokenPrefix + Random(32)
	err := st.Q.CreateAPIToken(ctx, db.CreateAPITokenParams{
		ID: domain.NewID("tok"), MemberID: memberID, Name: name, TokenHash: Hash(plain),
		Scope: string(scope), CreatedAt: store.FormatTime(time.Now()),
	})
	return plain, err
}

// ResolveToken returns the actor of a plaintext token.
func ResolveToken(ctx context.Context, st *store.Store, plain string) (app.Actor, error) {
	if !strings.HasPrefix(plain, TokenPrefix) {
		return app.Actor{}, ErrUnauthorized
	}
	t, err := st.Q.GetAPITokenByHash(ctx, Hash(plain))
	if err != nil {
		return app.Actor{}, ErrUnauthorized
	}
	m, err := st.Q.GetMember(ctx, t.MemberID)
	if err != nil {
		return app.Actor{}, ErrUnauthorized
	}
	_ = st.Q.TouchAPIToken(ctx, db.TouchAPITokenParams{LastUsedAt: store.Ptr(store.FormatTime(time.Now())), ID: t.ID})
	return app.Actor{Kind: app.ActorAgent, MemberID: m.ID, HouseholdID: m.HouseholdID, Label: t.Name, Scope: app.Scope(t.Scope)}, nil
}

// BearerToken extracts the token of an "Authorization: Bearer …" header.
func BearerToken(h http.Header) string {
	v := h.Get("Authorization")
	if after, ok := strings.CutPrefix(v, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// CreateLinkCode returns a one-time code (valid 10 minutes) that links a
// chat account to a member.
func CreateLinkCode(ctx context.Context, st *store.Store, memberID, channel string) (string, error) {
	code := Random(10)
	err := st.Q.CreateLinkCode(ctx, db.CreateLinkCodeParams{
		CodeHash: Hash(code), MemberID: memberID, Channel: channel,
		ExpiresAt: store.FormatTime(time.Now().Add(10 * time.Minute)),
	})
	return code, err
}

// RedeemLinkCode links externalID on channel to the code's member.
func RedeemLinkCode(ctx context.Context, st *store.Store, channel, code, externalID string) (string, error) {
	memberID, err := st.Q.TakeLinkCode(ctx, db.TakeLinkCodeParams{CodeHash: Hash(strings.TrimSpace(code)), Channel: channel, ExpiresAt: store.FormatTime(time.Now())})
	if err != nil {
		return "", ErrUnauthorized
	}
	err = st.Q.LinkChannel(ctx, db.LinkChannelParams{ID: domain.NewID("lnk"), MemberID: memberID, Channel: channel, ExternalID: externalID, CreatedAt: store.FormatTime(time.Now())})
	return memberID, err
}

// MemberForChannel returns the actor linked to a chat account.
func MemberForChannel(ctx context.Context, st *store.Store, channel, externalID string) (app.Actor, error) {
	id, err := st.Q.MemberForChannel(ctx, db.MemberForChannelParams{Channel: channel, ExternalID: externalID})
	if err != nil {
		return app.Actor{}, ErrUnauthorized
	}
	m, err := st.Q.GetMember(ctx, id)
	if err != nil {
		return app.Actor{}, ErrUnauthorized
	}
	return app.MemberActor(store.MemberFromRow(m), app.ScopeWrite), nil
}

// TokenInfo describes an API token without its secret.
type TokenInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
}

// ListTokens returns a member's active tokens.
func ListTokens(ctx context.Context, st *store.Store, memberID string) ([]TokenInfo, error) {
	rows, err := st.Q.ListAPITokens(ctx, memberID)
	out := make([]TokenInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, TokenInfo{ID: r.ID, Name: r.Name, Scope: r.Scope, CreatedAt: r.CreatedAt, LastUsedAt: store.Str(r.LastUsedAt)})
	}
	return out, err
}

// RevokeToken revokes one of a member's tokens.
func RevokeToken(ctx context.Context, st *store.Store, memberID, id string) error {
	n, err := st.Q.RevokeAPIToken(ctx, db.RevokeAPITokenParams{RevokedAt: store.Ptr(store.FormatTime(time.Now())), ID: id, MemberID: memberID})
	if err == nil && n == 0 {
		return fmt.Errorf("%w: token %s", domain.ErrNotFound, id)
	}
	return err
}

// LinkInfo describes a linked chat account or notify-only destination.
type LinkInfo struct {
	ID        string `json:"id"`
	Channel   string `json:"channel"`
	CreatedAt string `json:"createdAt"`
	// Destination and HasTemplate are filled by the API for notify-only
	// channels (email, ntfy, webhook); ExternalID never leaves the server.
	Destination string `json:"destination,omitempty" doc:"email address, ntfy topic or webhook host; empty for chat accounts"`
	HasTemplate bool   `json:"hasTemplate,omitempty" doc:"the webhook has its own body template"`
	ExternalID  string `json:"-"`
}

// ListLinks returns a member's linked chat accounts.
func ListLinks(ctx context.Context, st *store.Store, memberID string) ([]LinkInfo, error) {
	rows, err := st.Q.ChannelLinksForMember(ctx, memberID)
	out := make([]LinkInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, LinkInfo{ID: r.ID, Channel: r.Channel, CreatedAt: r.CreatedAt, ExternalID: r.ExternalID})
	}
	return out, err
}

// Unlink removes one of a member's chat links.
func Unlink(ctx context.Context, st *store.Store, memberID, id string) error {
	n, err := st.Q.DeleteChannelLink(ctx, db.DeleteChannelLinkParams{ID: id, MemberID: memberID})
	if err == nil && n == 0 {
		return fmt.Errorf("%w: link %s", domain.ErrNotFound, id)
	}
	return err
}

// DesktopTokenName names the token of the menu bar icon (ADR 0032).
const DesktopTokenName = "Exhale menu bar" //nolint:gosec // a name, not a secret

// DesktopTokenFile is the file, in the data directory, that holds it.
const DesktopTokenFile = "desktop-token"

// EnsureDesktopToken makes sure path holds a working token of memberID for
// the menu bar icon, which runs as the same person on the same computer:
// whoever can read the file can read the database next to it anyway. A
// missing or revoked token is replaced, and the old ones are revoked.
func EnsureDesktopToken(ctx context.Context, st *store.Store, memberID, path string) error {
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec // our own data directory
		// Looked up, not resolved: resolving records a use every minute.
		if t, err := st.Q.GetAPITokenByHash(ctx, Hash(strings.TrimSpace(string(b)))); err == nil && t.MemberID == memberID {
			return nil
		}
	}
	old, err := ListTokens(ctx, st, memberID)
	if err != nil {
		return err
	}
	for _, t := range old {
		if t.Name == DesktopTokenName {
			_ = RevokeToken(ctx, st, memberID, t.ID)
		}
	}
	plain, err := CreateToken(ctx, st, memberID, DesktopTokenName, app.ScopeWrite)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(plain+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
