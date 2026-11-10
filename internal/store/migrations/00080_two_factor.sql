-- +goose Up
-- ADR 0025: two-factor sign-in with an authenticator app (TOTP) and
-- one-time backup codes. Only password sign-in asks for it.
CREATE TABLE member_totp (
    member_id  TEXT PRIMARY KEY REFERENCES members (id) ON DELETE CASCADE,
    secret     TEXT NOT NULL,          -- base32; never returned after enrolment
    created_at TEXT NOT NULL,
    enabled_at TEXT,                   -- NULL until the first code confirms it
    last_step  INTEGER NOT NULL DEFAULT 0 -- last time step accepted: a code works once
);

CREATE TABLE member_backup_codes (
    member_id TEXT NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,           -- SHA-256
    used_at   TEXT,
    PRIMARY KEY (member_id, code_hash)
);

-- +goose Down
DROP TABLE member_backup_codes;
DROP TABLE member_totp;
