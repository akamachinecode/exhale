-- +goose Up
-- Reminders that end in an answer (feature scan: snooze, repeat until
-- handled, duplicates, card expiry).
--
-- An outbox item that is not about a charge (a duplicate found, a card about
-- to expire) says what it is about in ref: a payment method id or the key of
-- a duplicate group.
ALTER TABLE outbox ADD COLUMN ref TEXT NOT NULL DEFAULT '';
CREATE INDEX outbox_charge ON outbox (charge_id, kind);

-- How "was it charged?" repeats while nobody answers: asked this many times
-- in all, every so many days. New subscriptions copy it, like the reminder
-- time.
ALTER TABLE households ADD COLUMN confirm_times INTEGER NOT NULL DEFAULT 3;
ALTER TABLE households ADD COLUMN confirm_every_days INTEGER NOT NULL DEFAULT 1;

-- Findings someone said are fine ("we pay Spotify twice on purpose"), so
-- they are neither reported nor messaged again.
CREATE TABLE dismissed_findings (
    household_id TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL, -- duplicate
    key          TEXT NOT NULL,
    member_id    TEXT REFERENCES members (id) ON DELETE SET NULL,
    created_at   TEXT NOT NULL,
    PRIMARY KEY (household_id, kind, key)
);

-- +goose Down
DROP TABLE dismissed_findings;
ALTER TABLE households DROP COLUMN confirm_every_days;
ALTER TABLE households DROP COLUMN confirm_times;
DROP INDEX outbox_charge;
ALTER TABLE outbox DROP COLUMN ref;
