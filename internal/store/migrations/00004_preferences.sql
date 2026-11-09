-- +goose Up
-- Default time of day for reminders, chosen by the user (09:00 until then).
ALTER TABLE households ADD COLUMN reminder_hour INTEGER NOT NULL DEFAULT 9;
ALTER TABLE households ADD COLUMN reminder_minute INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE households DROP COLUMN reminder_minute;
ALTER TABLE households DROP COLUMN reminder_hour;
