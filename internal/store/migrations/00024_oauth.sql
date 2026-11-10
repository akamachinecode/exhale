-- +goose Up
-- ADR 0018 milestone 3, ADR 0024: remote MCP. Exhale is its own OAuth 2.1
-- authorization server, so cloud assistants connect through a consent
-- screen instead of a pasted token. Nothing secret is stored in clear:
-- client secrets, codes and tokens are SHA-256 hashes, like api_tokens.

-- An application that asked to connect. "dynamic" ones registered
-- themselves (RFC 7591) and their id is ours; for "metadata" ones the id is
-- the https URL of the document that describes them, kept here as a cache.
CREATE TABLE oauth_clients (
    id            TEXT PRIMARY KEY,
    kind          TEXT NOT NULL CHECK (kind IN ('dynamic', 'metadata')),
    name          TEXT NOT NULL,
    uri           TEXT NOT NULL DEFAULT '',
    redirect_uris TEXT NOT NULL,           -- JSON array
    secret_hash   TEXT NOT NULL DEFAULT '', -- '' for public clients
    created_at    TEXT NOT NULL,
    last_used_at  TEXT,                    -- last consent or token request
    fresh_until   TEXT                     -- metadata documents: fetch again after this
);

-- What a member allowed an application to do. Revoking deletes the row and,
-- with it, every token. session_version is the member's at consent time: a
-- password change bumps the member's and so ends the grant.
CREATE TABLE oauth_grants (
    id              TEXT PRIMARY KEY,
    member_id       TEXT NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    client_id       TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    scope           TEXT NOT NULL CHECK (scope IN ('read', 'write')),
    resource        TEXT NOT NULL, -- the MCP endpoint the tokens are for (RFC 8707)
    session_version INTEGER NOT NULL,
    created_at      TEXT NOT NULL,
    last_used_at    TEXT
);
CREATE INDEX oauth_grants_member ON oauth_grants (member_id);
CREATE INDEX oauth_grants_client ON oauth_grants (client_id);

-- Authorization codes: a few minutes, single use, bound to everything the
-- token request must repeat. A used code remembers its grant, so a second
-- use revokes what the first one obtained.
CREATE TABLE oauth_codes (
    code_hash      TEXT PRIMARY KEY,
    client_id      TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    member_id      TEXT NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    redirect_uri   TEXT NOT NULL,
    code_challenge TEXT NOT NULL, -- PKCE, S256
    scope          TEXT NOT NULL CHECK (scope IN ('read', 'write')),
    resource       TEXT NOT NULL,
    session_version INTEGER NOT NULL,
    expires_at     TEXT NOT NULL,
    used_at        TEXT,
    grant_id       TEXT REFERENCES oauth_grants (id) ON DELETE SET NULL
);
CREATE INDEX oauth_codes_expiry ON oauth_codes (expires_at);

-- Access tokens (one hour) and refresh tokens (rotated on every use; a used
-- one stays until it expires so that presenting it again is noticed and
-- ends the grant).
CREATE TABLE oauth_tokens (
    token_hash TEXT PRIMARY KEY,
    grant_id   TEXT NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('access', 'refresh')),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT
);
CREATE INDEX oauth_tokens_grant ON oauth_tokens (grant_id);
CREATE INDEX oauth_tokens_expiry ON oauth_tokens (expires_at);

-- +goose Down
DROP TABLE oauth_tokens;
DROP TABLE oauth_codes;
DROP TABLE oauth_grants;
DROP TABLE oauth_clients;
