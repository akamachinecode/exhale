-- name: CreateSubscription :exec
INSERT INTO subscriptions (
    id, household_id, name, price_minor, currency, schedule, category_id,
    payment_method_id, payer_id, visibility, status, url, notes, auto_confirm,
    reminders, created_by, created_at, updated_at, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1);

-- name: GetSubscription :one
SELECT * FROM subscriptions WHERE id = ? AND household_id = ?;

-- name: ListSubscriptions :many
SELECT * FROM subscriptions WHERE household_id = ? ORDER BY name COLLATE NOCASE;

-- name: ListActiveSubscriptions :many
SELECT * FROM subscriptions WHERE status = 'active';

-- name: UpdateSubscription :execrows
UPDATE subscriptions SET
    name = ?, price_minor = ?, currency = ?, schedule = ?, category_id = ?,
    payment_method_id = ?, payer_id = ?, visibility = ?, status = ?, url = ?,
    notes = ?, auto_confirm = ?, reminders = ?, updated_at = ?, version = version + 1
WHERE id = ? AND household_id = ? AND version = ?;

-- name: DeleteSubscription :exec
DELETE FROM subscriptions WHERE id = ? AND household_id = ?;
