-- +goose Up
-- ADR 0027: files people attach to a subscription, stored in the database
-- so that the one data folder, every backup and a restore hold them too.
-- kind 'logo' is the subscription's own logo (at most one live per
-- subscription); the others are documents: a contract or terms, an invoice
-- or receipt (usually of one charge), or anything else.
--
-- subscription_id and charge_id have no foreign key on purpose: deleting a
-- subscription keeps its files for 30 days (deleted_at) so undo brings them
-- back, and charges that are planned again must not take an invoice along.
-- A deleted file is purged for good 30 days after deleted_at.
CREATE TABLE files (
    id              TEXT PRIMARY KEY,
    household_id    TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    subscription_id TEXT NOT NULL,
    charge_id       TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL CHECK (kind IN ('logo', 'contract', 'invoice', 'other')),
    name            TEXT NOT NULL,
    media_type      TEXT NOT NULL,
    size            INTEGER NOT NULL,
    sha256          TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    created_by      TEXT NOT NULL,
    deleted_at      TEXT NOT NULL DEFAULT '',
    data            BLOB NOT NULL
);
CREATE INDEX files_subscription ON files (subscription_id, deleted_at);
CREATE UNIQUE INDEX files_one_logo ON files (subscription_id) WHERE kind = 'logo' AND deleted_at = '';

-- +goose Down
DROP TABLE files;
