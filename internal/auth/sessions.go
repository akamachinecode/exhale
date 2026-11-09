package auth

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Server-side web sessions (ADR 0006): scs with our own SQLite store. A
// session holds the member id and the member's session version; bumping the
// version (password change or reset) signs the member out everywhere.

const (
	keyMember  = "member"
	keyVersion = "v"
)

// SessionStore keeps scs sessions in SQLite.
type SessionStore struct{ st *store.Store }

var _ scs.CtxStore = SessionStore{}

func (s SessionStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	b, err := s.st.Q.FindSession(ctx, db.FindSessionParams{Token: token, Expiry: store.FormatTime(time.Now())})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return b, err == nil, err
}

func (s SessionStore) CommitCtx(ctx context.Context, token string, b []byte, expiry time.Time) error {
	if rand.IntN(100) == 0 { //nolint:gosec // sampling a cleanup, not a secret
		_ = s.st.Q.DeleteExpiredSessions(ctx, store.FormatTime(time.Now()))
	}
	return s.st.Q.SaveSession(ctx, db.SaveSessionParams{Token: token, Data: b, Expiry: store.FormatTime(expiry)})
}

func (s SessionStore) DeleteCtx(ctx context.Context, token string) error {
	return s.st.Q.DeleteSession(ctx, token)
}

func (s SessionStore) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

func (s SessionStore) Commit(token string, b []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, b, expiry)
}
func (s SessionStore) Delete(token string) error { return s.DeleteCtx(context.Background(), token) }

// NewSessions returns the session manager. secure marks the cookie Secure;
// set it when Exhale is served over HTTPS.
func NewSessions(st *store.Store, secure bool) *scs.SessionManager {
	sm := scs.New()
	sm.Store = SessionStore{st}
	sm.Lifetime = 30 * 24 * time.Hour
	sm.IdleTimeout = 14 * 24 * time.Hour
	sm.Cookie.Name = "exhale_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = secure
	sm.Cookie.Persist = true
	return sm
}

// StartSession signs a member in on this request's session, renewing the
// token to prevent session fixation.
func StartSession(ctx context.Context, sm *scs.SessionManager, st *store.Store, memberID string) error {
	m, err := st.Q.GetMember(ctx, memberID)
	if err != nil {
		return err
	}
	if err := sm.RenewToken(ctx); err != nil {
		return err
	}
	sm.Put(ctx, keyMember, m.ID)
	sm.Put(ctx, keyVersion, m.SessionVersion)
	return nil
}

// EndSession signs out.
func EndSession(ctx context.Context, sm *scs.SessionManager) error { return sm.Destroy(ctx) }

// RefreshSession keeps the current session valid after the member's own
// password change (which bumps the version and signs out other sessions).
func RefreshSession(ctx context.Context, sm *scs.SessionManager, st *store.Store, memberID string) error {
	return StartSession(ctx, sm, st, memberID)
}

// SessionActor returns the member signed in on this request, if any.
func SessionActor(ctx context.Context, sm *scs.SessionManager, st *store.Store) (app.Actor, bool) {
	id := sm.GetString(ctx, keyMember)
	if id == "" {
		return app.Actor{}, false
	}
	m, err := st.Q.GetMember(ctx, id)
	if err != nil || m.PasswordHash == "" || sm.GetInt64(ctx, keyVersion) != m.SessionVersion {
		return app.Actor{}, false
	}
	return app.MemberActor(store.MemberFromRow(m), app.ScopeTrusted), true
}
