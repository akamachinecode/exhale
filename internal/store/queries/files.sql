-- ADR 0027: logos and documents. Listing queries never read the data column.

-- name: CreateFile :exec
INSERT INTO files (
    id, household_id, subscription_id, charge_id, kind, name, media_type,
    size, sha256, created_at, created_by, data
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetFileMeta :one
SELECT id, household_id, subscription_id, charge_id, kind, name, media_type,
       size, sha256, created_at, created_by, deleted_at
FROM files WHERE id = ? AND household_id = ?;

-- name: GetFileData :one
SELECT data FROM files WHERE id = ? AND household_id = ? AND deleted_at = '';

-- name: ListDocuments :many
SELECT id, household_id, subscription_id, charge_id, kind, name, media_type,
       size, sha256, created_at, created_by, deleted_at
FROM files
WHERE subscription_id = ? AND household_id = ? AND kind <> 'logo' AND deleted_at = ''
ORDER BY created_at DESC, id;

-- name: GetLogo :one
SELECT id, household_id, subscription_id, charge_id, kind, name, media_type,
       size, sha256, created_at, created_by, deleted_at
FROM files
WHERE subscription_id = ? AND household_id = ? AND kind = 'logo' AND deleted_at = '';

-- name: HouseholdFilesSize :one
SELECT CAST(COALESCE(SUM(size), 0) AS INTEGER) FROM files WHERE household_id = ?;

-- name: TrashFile :exec
UPDATE files SET deleted_at = ? WHERE id = ? AND household_id = ?;

-- name: RestoreFile :exec
UPDATE files SET deleted_at = '' WHERE id = ? AND household_id = ?;

-- name: TrashSubscriptionFiles :many
UPDATE files SET deleted_at = sqlc.arg(deleted_at)
WHERE subscription_id = sqlc.arg(subscription_id) AND household_id = sqlc.arg(household_id) AND deleted_at = ''
RETURNING id;

-- name: PurgeFiles :execrows
DELETE FROM files WHERE deleted_at <> '' AND deleted_at < ?;

-- name: ListFilesForExport :many
SELECT id, household_id, subscription_id, charge_id, kind, name, media_type,
       size, sha256, created_at, created_by, deleted_at
FROM files WHERE household_id = ? AND deleted_at = ''
ORDER BY subscription_id, created_at, id;
