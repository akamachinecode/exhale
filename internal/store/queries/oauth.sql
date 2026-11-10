-- OAuth 2.1 authorization server for remote MCP (ADR 0024).

-- name: CreateOAuthClient :exec
INSERT INTO oauth_clients (id, kind, name, uri, redirect_uris, secret_hash, created_at)
VALUES (?, 'dynamic', ?, ?, ?, ?, ?);

-- name: SaveOAuthMetadataClient :exec
-- The cached copy of a client metadata document, replaced on every fetch.
INSERT INTO oauth_clients (id, kind, name, uri, redirect_uris, created_at, fresh_until)
VALUES (sqlc.arg(id), 'metadata', sqlc.arg(name), sqlc.arg(uri), sqlc.arg(redirect_uris), sqlc.arg(created_at), sqlc.arg(fresh_until))
ON CONFLICT (id) DO UPDATE SET name = excluded.name, uri = excluded.uri,
    redirect_uris = excluded.redirect_uris, fresh_until = excluded.fresh_until;

-- name: GetOAuthClient :one
SELECT * FROM oauth_clients WHERE id = ?;

-- name: TouchOAuthClient :exec
UPDATE oauth_clients SET last_used_at = ? WHERE id = ?;

-- name: CountOAuthDynamicClients :one
SELECT count(*) FROM oauth_clients WHERE kind = 'dynamic';

-- name: EvictOAuthClient :execrows
-- Makes room for a new registration: drops the oldest one nobody ever
-- approved.
DELETE FROM oauth_clients WHERE id = (
    SELECT c.id FROM oauth_clients c
    WHERE c.kind = 'dynamic' AND c.last_used_at IS NULL
      AND NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id = c.id)
    ORDER BY c.created_at LIMIT 1);

-- name: CreateOAuthCode :exec
INSERT INTO oauth_codes (code_hash, client_id, member_id, redirect_uri, code_challenge, scope, resource, session_version, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetOAuthCode :one
SELECT * FROM oauth_codes WHERE code_hash = ?;

-- name: UseOAuthCode :execrows
-- Single use: only the first caller changes a row.
UPDATE oauth_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL;

-- name: SetOAuthCodeGrant :exec
UPDATE oauth_codes SET grant_id = ? WHERE code_hash = ?;

-- name: CreateOAuthGrant :exec
INSERT INTO oauth_grants (id, member_id, client_id, scope, resource, session_version, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetOAuthGrant :one
SELECT * FROM oauth_grants WHERE id = ?;

-- name: TouchOAuthGrant :exec
-- At most one write per minute and grant, however busy the assistant is.
UPDATE oauth_grants SET last_used_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND (last_used_at IS NULL OR last_used_at < sqlc.arg(stale));

-- name: DeleteOAuthGrant :execrows
DELETE FROM oauth_grants WHERE id = ?;

-- name: ListOAuthGrantsOfSameApp :many
-- Cloud assistants register again each time they connect. A member's
-- earlier connections of a self-registered application with the same name
-- and redirect URIs are the same application connecting again.
SELECT g.id FROM oauth_grants g JOIN oauth_clients c ON c.id = g.client_id
WHERE g.member_id = sqlc.arg(member_id) AND g.resource = sqlc.arg(resource) AND c.kind = 'dynamic'
  AND c.name = sqlc.arg(name) AND c.redirect_uris = sqlc.arg(redirect_uris);

-- name: ListOAuthGrants :many
-- The applications a member has connected: grants that still have a token
-- that works.
SELECT g.id, g.scope, g.resource, g.created_at, g.last_used_at,
       c.id AS client_id, c.kind AS client_kind, c.name AS client_name, c.uri AS client_uri, c.redirect_uris
FROM oauth_grants g
JOIN oauth_clients c ON c.id = g.client_id
JOIN members m ON m.id = g.member_id
WHERE g.member_id = sqlc.arg(member_id) AND g.session_version = m.session_version
  AND EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.grant_id = g.id AND t.used_at IS NULL AND t.expires_at > sqlc.arg(now))
ORDER BY g.created_at DESC;

-- name: CreateOAuthToken :exec
INSERT INTO oauth_tokens (token_hash, grant_id, kind, created_at, expires_at) VALUES (?, ?, ?, ?, ?);

-- name: GetOAuthToken :one
SELECT * FROM oauth_tokens WHERE token_hash = ?;

-- name: UseOAuthToken :execrows
-- Rotation: only the first caller changes a row.
UPDATE oauth_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL;

-- name: DeleteOAuthToken :exec
DELETE FROM oauth_tokens WHERE token_hash = ?;

-- name: GetOAuthAccess :one
-- Everything needed to accept an access token, in one read.
SELECT t.expires_at, g.id AS grant_id, g.member_id, g.client_id, g.scope, g.resource,
       g.session_version AS grant_version, m.session_version AS member_version, m.household_id,
       c.name AS client_name
FROM oauth_tokens t
JOIN oauth_grants g ON g.id = t.grant_id
JOIN oauth_clients c ON c.id = g.client_id
JOIN members m ON m.id = g.member_id
WHERE t.token_hash = ? AND t.kind = 'access';

-- name: DeleteExpiredOAuthTokens :exec
DELETE FROM oauth_tokens WHERE expires_at <= ?;

-- name: DeleteExpiredOAuthCodes :exec
DELETE FROM oauth_codes WHERE expires_at <= ?;

-- name: DeleteDeadOAuthGrants :exec
-- Grants with no token left, and grants ended by a password change.
DELETE FROM oauth_grants
WHERE id IN (
    SELECT g.id FROM oauth_grants g JOIN members m ON m.id = g.member_id
    WHERE g.session_version <> m.session_version
       OR NOT EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.grant_id = g.id));

-- name: DeleteIdleOAuthClients :exec
-- Clients without grants: registrations nobody approved, registrations not
-- used for a long time, and cached metadata documents.
DELETE FROM oauth_clients
WHERE id IN (
    SELECT c.id FROM oauth_clients c
    WHERE NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id = c.id)
      AND NOT EXISTS (SELECT 1 FROM oauth_codes o WHERE o.client_id = c.id)
      AND ((c.last_used_at IS NULL AND c.created_at <= sqlc.arg(unused_before))
        OR (c.last_used_at IS NOT NULL AND c.last_used_at <= sqlc.arg(idle_before))));
