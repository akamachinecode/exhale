-- Two-factor sign-in (ADR 0025).

-- name: GetTOTP :one
SELECT * FROM member_totp WHERE member_id = ?;

-- name: PutPendingTOTP :execrows
-- Starts or restarts an enrolment. Never replaces a secret that is on.
INSERT INTO member_totp (member_id, secret, created_at) VALUES (?, ?, ?)
ON CONFLICT (member_id) DO UPDATE SET secret = excluded.secret, created_at = excluded.created_at, last_step = 0
WHERE member_totp.enabled_at IS NULL;

-- name: EnableTOTP :execrows
UPDATE member_totp SET enabled_at = sqlc.arg(enabled_at), last_step = sqlc.arg(step)
WHERE member_id = sqlc.arg(member_id) AND secret = sqlc.arg(secret) AND enabled_at IS NULL;

-- name: UseTOTPStep :execrows
-- Accepts a time step only once and never one older than the last.
UPDATE member_totp SET last_step = sqlc.arg(step)
WHERE member_id = sqlc.arg(member_id) AND enabled_at IS NOT NULL AND last_step < sqlc.arg(step);

-- name: DeleteTOTP :execrows
DELETE FROM member_totp WHERE member_id = ?;

-- name: DeleteBackupCodes :exec
DELETE FROM member_backup_codes WHERE member_id = ?;

-- name: AddBackupCode :exec
INSERT INTO member_backup_codes (member_id, code_hash) VALUES (?, ?);

-- name: UseBackupCode :execrows
UPDATE member_backup_codes SET used_at = ? WHERE member_id = ? AND code_hash = ? AND used_at IS NULL;

-- name: CountBackupCodesLeft :one
SELECT count(*) FROM member_backup_codes WHERE member_id = ? AND used_at IS NULL;

-- name: ListTwoFactorMembers :many
SELECT t.member_id FROM member_totp t JOIN members m ON m.id = t.member_id
WHERE m.household_id = ? AND t.enabled_at IS NOT NULL;

-- name: BumpSessionVersion :exec
-- Signs a member out of every session (turning two-factor on).
UPDATE members SET session_version = session_version + 1 WHERE id = ?;
