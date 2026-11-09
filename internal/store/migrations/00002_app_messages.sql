-- +goose Up
-- ADR 0019: a chat message can be a living app. App messages record the
-- screen they show (route) so changes from anywhere can redraw it.
ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT 'reminder'; -- reminder | app
ALTER TABLE messages ADD COLUMN route TEXT NOT NULL DEFAULT '';         -- app screen; '' while mid-flow
CREATE INDEX messages_app ON messages (kind, member_id);

-- +goose Down
DROP INDEX messages_app;
ALTER TABLE messages DROP COLUMN route;
ALTER TABLE messages DROP COLUMN kind;
