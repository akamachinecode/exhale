-- name: InsertChargeIfMissing :exec
INSERT INTO charges (
    id, subscription_id, original_date, due_date, amount_minor, currency,
    status, updated_at, version
) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, 1)
ON CONFLICT (subscription_id, original_date) DO NOTHING;

-- name: GetCharge :one
SELECT * FROM charges WHERE id = ?;

-- name: GetChargeByOriginalDate :one
SELECT * FROM charges WHERE subscription_id = ? AND original_date = ?;

-- name: ListChargesForSubscription :many
SELECT * FROM charges WHERE subscription_id = ? ORDER BY original_date DESC LIMIT ?;

-- name: ListChargesBetween :many
SELECT c.* FROM charges c
JOIN subscriptions s ON s.id = c.subscription_id
WHERE s.household_id = sqlc.arg(household_id)
  AND c.due_date >= sqlc.arg(from_date) AND c.due_date <= sqlc.arg(to_date)
ORDER BY c.due_date, s.name;

-- name: ListOpenChargesDueBefore :many
SELECT c.* FROM charges c
JOIN subscriptions s ON s.id = c.subscription_id
WHERE s.household_id = sqlc.arg(household_id)
  AND c.status IN ('pending', 'failed') AND c.due_date < sqlc.arg(before)
ORDER BY c.due_date;

-- name: UpdateCharge :execrows
UPDATE charges SET
    due_date = ?, amount_minor = ?, currency = ?, charged_minor = ?,
    charged_currency = ?, status = ?, resolved_at = ?, note = ?, decision = ?,
    updated_at = ?, version = version + 1
WHERE id = ? AND version = ?;

-- name: DeleteFuturePendingCharges :exec
DELETE FROM charges
WHERE subscription_id = ? AND status = 'pending' AND original_date >= ? AND due_date = original_date;

-- name: CreateAudit :exec
INSERT INTO audit_log (id, at, actor_kind, actor_id, actor_label, action, entity, entity_id, before, after)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAudit :one
SELECT * FROM audit_log WHERE id = ?;

-- name: MarkAuditUndone :exec
UPDATE audit_log SET undone_by = ? WHERE id = ?;

-- name: ListAudit :many
SELECT * FROM audit_log ORDER BY at DESC LIMIT ?;

-- name: ListPaidChargesBetween :many
SELECT c.* FROM charges c
JOIN subscriptions s ON s.id = c.subscription_id
WHERE s.household_id = sqlc.arg(household_id) AND c.status = 'paid'
  AND c.due_date >= sqlc.arg(from_date) AND c.due_date <= sqlc.arg(to_date)
ORDER BY c.due_date;

-- name: ListChargesForHousehold :many
SELECT c.* FROM charges c
JOIN subscriptions s ON s.id = c.subscription_id
WHERE s.household_id = ?
ORDER BY c.subscription_id, c.original_date;

-- name: RestoreCharge :exec
INSERT INTO charges (
    id, subscription_id, original_date, due_date, amount_minor, currency,
    charged_minor, charged_currency, status, resolved_at, note, decision, updated_at, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (subscription_id, original_date) DO NOTHING;
