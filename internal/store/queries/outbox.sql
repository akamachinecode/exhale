-- name: EnqueueOutbox :exec
INSERT INTO outbox (id, kind, dedupe_key, charge_id, member_id, due_at, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, 'pending', ?)
-- A pending reminder follows changes of its time (reminder settings); one
-- already retried keeps its backoff.
ON CONFLICT (dedupe_key) DO UPDATE SET due_at = excluded.due_at, member_id = excluded.member_id
WHERE outbox.status = 'pending' AND outbox.attempts = 0;

-- name: NextOutboxDue :one
SELECT due_at FROM outbox WHERE status = 'pending' ORDER BY due_at LIMIT 1;

-- name: ListOutboxDue :many
SELECT * FROM outbox WHERE status = 'pending' AND due_at <= ? ORDER BY due_at LIMIT ?;

-- name: MarkOutboxSent :exec
UPDATE outbox SET status = 'sent', sent_at = ?, attempts = attempts + 1 WHERE id = ?;

-- name: MarkOutboxAttemptFailed :exec
UPDATE outbox SET attempts = attempts + 1, last_error = ?, due_at = ?,
    status = CASE WHEN attempts + 1 >= ? THEN 'failed' ELSE 'pending' END
WHERE id = ?;

-- name: CancelOutboxForCharge :exec
UPDATE outbox SET status = 'cancelled' WHERE charge_id = ? AND status = 'pending';

-- name: CancelOutboxKindForCharge :exec
UPDATE outbox SET status = 'cancelled' WHERE charge_id = ? AND kind = ? AND status = 'pending';

-- name: ListOutbox :many
SELECT * FROM outbox ORDER BY due_at DESC LIMIT ?;

-- name: SaveMessage :exec
INSERT INTO messages (id, channel, chat_id, message_id, charge_id, member_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (channel, chat_id, message_id) DO NOTHING;

-- name: ListMessagesForCharge :many
SELECT * FROM messages WHERE charge_id = ? AND kind = 'reminder';

-- name: SaveAppMessage :exec
INSERT INTO messages (id, channel, chat_id, message_id, member_id, kind, route, created_at)
VALUES (?, ?, ?, ?, ?, 'app', ?, ?)
ON CONFLICT (channel, chat_id, message_id) DO UPDATE SET kind = 'app', route = excluded.route;

-- name: GetMessage :one
SELECT * FROM messages WHERE channel = ? AND chat_id = ? AND message_id = ?;

-- name: SetMessageRoute :exec
UPDATE messages SET route = ? WHERE channel = ? AND chat_id = ? AND message_id = ? AND kind = 'app';

-- name: ListAppMessagesForChat :many
SELECT * FROM messages WHERE kind = 'app' AND channel = ? AND chat_id = ?;

-- name: RetireAppMessage :exec
UPDATE messages SET kind = 'app_old', route = '' WHERE id = ?;

-- name: ListLiveAppMessages :many
SELECT * FROM messages WHERE kind = 'app' AND route <> '';

-- name: SaveFlow :exec
INSERT INTO flows (id, member_id, state, expires_at, created_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET state = excluded.state, expires_at = excluded.expires_at;

-- name: GetFlow :one
SELECT * FROM flows WHERE id = ?;

-- name: DeleteFlow :exec
DELETE FROM flows WHERE id = ?;

-- name: DeleteExpiredFlows :exec
DELETE FROM flows WHERE expires_at < ?;

-- name: LinkChannel :exec
INSERT INTO channel_links (id, member_id, channel, external_id, created_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (channel, external_id) DO UPDATE SET member_id = excluded.member_id;

-- name: MemberForChannel :one
SELECT member_id FROM channel_links WHERE channel = ? AND external_id = ?;

-- name: ChannelLinksForMember :many
SELECT * FROM channel_links WHERE member_id = ?;

-- name: CreateLinkCode :exec
INSERT INTO link_codes (code_hash, member_id, channel, expires_at) VALUES (?, ?, ?, ?);

-- name: TakeLinkCode :one
DELETE FROM link_codes WHERE code_hash = ? AND channel = ? AND expires_at > ? RETURNING member_id;

-- name: CreateAPIToken :exec
INSERT INTO api_tokens (id, member_id, name, token_hash, scope, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetAPITokenByHash :one
SELECT * FROM api_tokens WHERE token_hash = ? AND revoked_at IS NULL;

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = ? WHERE id = ?;

-- name: UpsertFxRate :exec
INSERT INTO fx_rates (day, currency, rate, source) VALUES (?, ?, ?, ?)
ON CONFLICT (day, currency) DO UPDATE SET rate = excluded.rate, source = excluded.source;

-- name: FxRateOnOrBefore :one
SELECT rate, day FROM fx_rates WHERE currency = ? AND day <= ? ORDER BY day DESC LIMIT 1;

-- name: ListOutboxForHousehold :many
SELECT o.id, o.kind, o.status, o.due_at, o.sent_at, o.attempts, o.last_error, o.member_id,
       c.id AS charge_id, c.due_date AS charge_due, s.id AS subscription_id, s.name AS subscription_name
FROM outbox o
JOIN charges c ON c.id = o.charge_id
JOIN subscriptions s ON s.id = c.subscription_id
WHERE s.household_id = ?
ORDER BY o.due_at DESC
LIMIT ?;

-- name: MarkOutboxUndeliverable :exec
UPDATE outbox SET status = 'failed', last_error = ?, attempts = attempts + 1 WHERE id = ?;

-- Reminders that end in an answer: snoozes, repeats, notices that are not
-- about a charge.

-- name: EnqueueNotice :exec
INSERT INTO outbox (id, kind, dedupe_key, member_id, ref, due_at, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, 'pending', ?)
ON CONFLICT (dedupe_key) DO NOTHING;

-- name: ListOutboxForChargeKind :many
SELECT dedupe_key, due_at, status, member_id FROM outbox
WHERE charge_id = ? AND kind = ? AND status IN ('pending', 'sent');

-- name: CancelOutboxKey :exec
UPDATE outbox SET status = 'cancelled' WHERE dedupe_key = ? AND status = 'pending';

-- name: CancelOutboxRef :exec
UPDATE outbox SET status = 'cancelled' WHERE kind = ? AND ref = ? AND status = 'pending';

-- name: DismissFinding :exec
INSERT INTO dismissed_findings (household_id, kind, key, member_id, created_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (household_id, kind, key) DO NOTHING;

-- name: ListDismissedFindings :many
SELECT key FROM dismissed_findings WHERE household_id = ? AND kind = ?;

-- name: MarkOutboxObsolete :exec
UPDATE outbox SET status = 'cancelled', last_error = ? WHERE id = ? AND status = 'pending';
