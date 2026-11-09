-- +goose Up
-- ADR 0006: web sign-in. Server-side sessions (scs), revocable per member by
-- bumping session_version; household invites; one login email per person.
CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BLOB NOT NULL,
    expiry TEXT NOT NULL
);
CREATE INDEX sessions_expiry ON sessions (expiry);

ALTER TABLE members ADD COLUMN session_version INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX members_email ON members (email) WHERE email <> '';

CREATE TABLE invites (
    code_hash    TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    role         TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    created_by   TEXT NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    expires_at   TEXT NOT NULL,
    used_at      TEXT
);

-- A household created by an empty bootstrap (fixed in this release) has no
-- name or currency and no data; drop it so the setup wizard can run.
DELETE FROM households
WHERE currency = ''
  AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.household_id = households.id);

-- +goose Down
DROP TABLE invites;
DROP INDEX members_email;
ALTER TABLE members DROP COLUMN session_version;
DROP TABLE sessions;
