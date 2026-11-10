-- +goose Up
-- ADR 0023: each person may choose their own language; empty means the
-- household's.
ALTER TABLE members ADD COLUMN locale TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE members DROP COLUMN locale;
