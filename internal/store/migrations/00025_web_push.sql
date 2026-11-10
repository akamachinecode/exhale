-- +goose Up
-- ADR 0026: Web Push. A device is a channel link (channel 'webpush', external
-- id = the push endpoint); its browser keys live here and go with the link.
CREATE TABLE push_subscriptions (
    link_id    TEXT PRIMARY KEY REFERENCES channel_links (id) ON DELETE CASCADE,
    p256dh     TEXT NOT NULL, -- browser public key, base64url
    auth       TEXT NOT NULL, -- browser auth secret, base64url
    device     TEXT NOT NULL DEFAULT '', -- shown in Settings, e.g. "Chrome on Android"
    created_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE push_subscriptions;
