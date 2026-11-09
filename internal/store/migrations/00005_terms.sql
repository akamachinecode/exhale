-- +goose Up
-- Contract facts of a subscription (start date, free trial, notice period,
-- manual renewal, cancel link, cost shares…) as one JSON document: they are
-- read and written together and never filtered in SQL.
ALTER TABLE subscriptions ADD COLUMN details TEXT NOT NULL DEFAULT '{}';
-- What the member decided about this renewal: '' (nothing yet) or 'keep'.
ALTER TABLE charges ADD COLUMN decision TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE charges DROP COLUMN decision;
ALTER TABLE subscriptions DROP COLUMN details;
