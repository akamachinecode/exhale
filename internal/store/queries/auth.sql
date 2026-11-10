-- name: FindSession :one
SELECT data FROM sessions WHERE token = ? AND expiry > ?;

-- name: SaveSession :exec
INSERT INTO sessions (token, data, expiry) VALUES (?, ?, ?)
ON CONFLICT (token) DO UPDATE SET data = excluded.data, expiry = excluded.expiry;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expiry <= ?;

-- name: MemberByEmail :one
SELECT * FROM members WHERE email = ? AND email <> '';

-- name: CountMembersWithLogin :one
SELECT count(*) FROM members WHERE password_hash <> '' OR oidc_subject <> '';

-- name: MemberByIdentity :one
SELECT * FROM members WHERE oidc_issuer = ? AND oidc_subject = ? AND oidc_subject <> '';

-- name: LinkMemberIdentity :execrows
-- Links a member that has no identity, or one of another provider (a dead
-- link after the instance moved providers). Never replaces a link to the
-- same provider.
UPDATE members SET oidc_issuer = sqlc.arg(issuer), oidc_subject = sqlc.arg(subject)
WHERE id = sqlc.arg(id) AND (oidc_subject = '' OR oidc_issuer <> sqlc.arg(issuer));

-- name: SetMemberProfile :exec
UPDATE members SET name = ?, email = ? WHERE id = ?;

-- name: SetMemberCredentials :exec
UPDATE members SET name = ?, email = ?, password_hash = ? WHERE id = ?;

-- name: SetMemberPassword :exec
UPDATE members SET password_hash = ?, session_version = session_version + 1 WHERE id = ?;

-- name: CreateInvite :exec
INSERT INTO invites (code_hash, household_id, role, created_by, expires_at) VALUES (?, ?, ?, ?, ?);

-- name: GetInvite :one
SELECT * FROM invites WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?;

-- name: UseInvite :execrows
UPDATE invites SET used_at = ? WHERE code_hash = ? AND used_at IS NULL;

-- name: ListAPITokens :many
SELECT * FROM api_tokens WHERE member_id = ? AND revoked_at IS NULL ORDER BY created_at DESC;

-- name: RevokeAPIToken :execrows
UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND member_id = ? AND revoked_at IS NULL;

-- name: DeleteChannelLink :execrows
DELETE FROM channel_links WHERE id = ? AND member_id = ?;
