-- +goose Up
-- Day of the month the household gets paid (0: not set). With it Exhale can
-- say how much is due before the next payday.
ALTER TABLE households ADD COLUMN payday INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE households DROP COLUMN payday;
