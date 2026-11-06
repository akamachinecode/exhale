-- +goose Up
CREATE TABLE households (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    currency   TEXT NOT NULL,
    timezone   TEXT NOT NULL,
    locale     TEXT NOT NULL DEFAULT 'en',
    created_at TEXT NOT NULL
);

CREATE TABLE members (
    id            TEXT PRIMARY KEY,
    household_id  TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    email         TEXT NOT NULL DEFAULT '',
    role          TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    password_hash TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);

CREATE TABLE payment_methods (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('card', 'bank', 'paypal', 'other')),
    last4        TEXT NOT NULL DEFAULT '',
    expires_on   TEXT,
    owner_id     TEXT REFERENCES members (id) ON DELETE SET NULL
);

CREATE TABLE categories (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    color        TEXT NOT NULL DEFAULT '',
    UNIQUE (household_id, name)
);

CREATE TABLE subscriptions (
    id                TEXT PRIMARY KEY,
    household_id      TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    price_minor       INTEGER NOT NULL,
    currency          TEXT NOT NULL,
    schedule          TEXT NOT NULL, -- engine.Schedule as JSON
    category_id       TEXT REFERENCES categories (id) ON DELETE SET NULL,
    payment_method_id TEXT REFERENCES payment_methods (id) ON DELETE SET NULL,
    payer_id          TEXT REFERENCES members (id) ON DELETE SET NULL,
    visibility        TEXT NOT NULL CHECK (visibility IN ('shared', 'private')),
    status            TEXT NOT NULL CHECK (status IN ('active', 'paused', 'cancelled')),
    url               TEXT NOT NULL DEFAULT '',
    notes             TEXT NOT NULL DEFAULT '',
    auto_confirm      INTEGER NOT NULL DEFAULT 0,
    reminders         TEXT NOT NULL, -- domain.ReminderPolicy as JSON
    created_by        TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    version           INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX subscriptions_household ON subscriptions (household_id, status);

CREATE TABLE charges (
    id               TEXT PRIMARY KEY,
    subscription_id  TEXT NOT NULL REFERENCES subscriptions (id) ON DELETE CASCADE,
    original_date    TEXT NOT NULL,
    due_date         TEXT NOT NULL,
    amount_minor     INTEGER NOT NULL,
    currency         TEXT NOT NULL,
    charged_minor    INTEGER,
    charged_currency TEXT,
    status           TEXT NOT NULL CHECK (status IN ('pending', 'paid', 'failed', 'skipped')),
    resolved_at      TEXT,
    note             TEXT NOT NULL DEFAULT '',
    updated_at       TEXT NOT NULL,
    version          INTEGER NOT NULL DEFAULT 1,
    UNIQUE (subscription_id, original_date)
);
CREATE INDEX charges_due ON charges (due_date, status);

-- Outbox of everything the scheduler must deliver (reminders, confirmations,
-- alerts). dedupe_key makes every delivery happen at most once.
CREATE TABLE outbox (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    dedupe_key TEXT NOT NULL UNIQUE,
    charge_id  TEXT REFERENCES charges (id) ON DELETE CASCADE,
    member_id  TEXT REFERENCES members (id) ON DELETE CASCADE,
    due_at     TEXT NOT NULL, -- RFC 3339, UTC
    status     TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed', 'cancelled')),
    attempts   INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    sent_at    TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX outbox_due ON outbox (status, due_at);

-- Messages already delivered to chat channels, so they can be re-edited.
CREATE TABLE messages (
    id         TEXT PRIMARY KEY,
    channel    TEXT NOT NULL,
    chat_id    TEXT NOT NULL,
    message_id TEXT NOT NULL,
    charge_id  TEXT REFERENCES charges (id) ON DELETE CASCADE,
    member_id  TEXT REFERENCES members (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    UNIQUE (channel, chat_id, message_id)
);
CREATE INDEX messages_charge ON messages (charge_id);

-- Multi-step action flows (ADR 0003: chat buttons carry only a short id).
CREATE TABLE flows (
    id         TEXT PRIMARY KEY,
    member_id  TEXT NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    state      TEXT NOT NULL, -- JSON
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE channel_links (
    id          TEXT PRIMARY KEY,
    member_id   TEXT NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    channel     TEXT NOT NULL,
    external_id TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    UNIQUE (channel, external_id)
);

CREATE TABLE link_codes (
    code_hash  TEXT PRIMARY KEY,
    member_id  TEXT NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    channel    TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    member_id    TEXT NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    scope        TEXT NOT NULL CHECK (scope IN ('read', 'write', 'trusted')),
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
);

-- Every change, with its actor and the inverse needed to undo it.
CREATE TABLE audit_log (
    id          TEXT PRIMARY KEY,
    at          TEXT NOT NULL,
    actor_kind  TEXT NOT NULL CHECK (actor_kind IN ('member', 'agent', 'system')),
    actor_id    TEXT NOT NULL DEFAULT '',
    actor_label TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    entity      TEXT NOT NULL,
    entity_id   TEXT NOT NULL,
    before      TEXT NOT NULL DEFAULT '', -- JSON snapshot
    after       TEXT NOT NULL DEFAULT '', -- JSON snapshot
    undone_by   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_entity ON audit_log (entity, entity_id);

CREATE TABLE fx_rates (
    day      TEXT NOT NULL,
    currency TEXT NOT NULL,
    rate     TEXT NOT NULL, -- units of currency per 1 EUR (ECB reference)
    source   TEXT NOT NULL DEFAULT 'ecb',
    PRIMARY KEY (day, currency)
);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- +goose Down
DROP TABLE settings;
DROP TABLE fx_rates;
DROP TABLE audit_log;
DROP TABLE api_tokens;
DROP TABLE link_codes;
DROP TABLE channel_links;
DROP TABLE flows;
DROP TABLE messages;
DROP TABLE outbox;
DROP TABLE charges;
DROP TABLE subscriptions;
DROP TABLE categories;
DROP TABLE payment_methods;
DROP TABLE members;
DROP TABLE households;
