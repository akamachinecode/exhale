-- +goose Up
-- ADR 0018, safety rule 6: what each question to the built-in assistant
-- cost. Counts only: never the words. member_id has no foreign key, so a
-- month's total survives someone leaving.
CREATE TABLE assistant_usage (
    id            TEXT PRIMARY KEY,
    household_id  TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    member_id     TEXT NOT NULL,
    at            TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('question', 'import')),
    surface       TEXT NOT NULL,      -- web, telegram, slack, import
    provider      TEXT NOT NULL,
    model         TEXT NOT NULL,
    rounds        INTEGER NOT NULL,
    input_tokens  INTEGER NOT NULL,
    output_tokens INTEGER NOT NULL,
    cost_micros   INTEGER,            -- estimated, in millionths of a US dollar; NULL when the price is unknown
    failed        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX assistant_usage_at ON assistant_usage (household_id, at);

-- +goose Down
DROP TABLE assistant_usage;
