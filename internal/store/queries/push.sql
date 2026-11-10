-- ADR 0026: Web Push devices.

-- name: SavePushSubscription :exec
INSERT INTO push_subscriptions (link_id, p256dh, auth, device, created_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (link_id) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth, device = excluded.device;

-- name: GetPushSubscription :one
SELECT l.id AS link_id, l.member_id, p.p256dh, p.auth, p.device
FROM channel_links l JOIN push_subscriptions p ON p.link_id = l.id
WHERE l.channel = 'webpush' AND l.external_id = ?;

-- name: DeletePushEndpoint :exec
DELETE FROM channel_links WHERE channel = 'webpush' AND external_id = ?;

-- name: PushLink :one
SELECT * FROM channel_links WHERE channel = 'webpush' AND external_id = ?;
