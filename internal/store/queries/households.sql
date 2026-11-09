-- name: CreateHousehold :exec
INSERT INTO households (id, name, currency, timezone, locale, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetHousehold :one
SELECT * FROM households WHERE id = ?;

-- name: FirstHousehold :one
SELECT * FROM households ORDER BY created_at LIMIT 1;

-- name: UpdateHousehold :exec
UPDATE households SET name = ?, currency = ?, timezone = ?, locale = ?, reminder_hour = ?, reminder_minute = ?
WHERE id = ?;

-- name: CreateMember :exec
INSERT INTO members (id, household_id, name, email, role, password_hash, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetMember :one
SELECT * FROM members WHERE id = ?;

-- name: ListMembers :many
SELECT * FROM members WHERE household_id = ? ORDER BY created_at;

-- name: CreateCategory :exec
INSERT INTO categories (id, household_id, name, color) VALUES (?, ?, ?, ?);

-- name: ListCategories :many
SELECT * FROM categories WHERE household_id = ? ORDER BY name;

-- name: CreatePaymentMethod :exec
INSERT INTO payment_methods (id, household_id, name, kind, last4, expires_on, owner_id)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: UpdatePaymentMethod :exec
UPDATE payment_methods SET name = ?, kind = ?, last4 = ?, expires_on = ?, owner_id = ?
WHERE id = ? AND household_id = ?;

-- name: GetPaymentMethod :one
SELECT * FROM payment_methods WHERE id = ? AND household_id = ?;

-- name: ListPaymentMethods :many
SELECT * FROM payment_methods WHERE household_id = ? ORDER BY name;

-- name: GetSetting :one
SELECT value FROM settings WHERE key = ?;

-- name: PutSetting :exec
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value;
