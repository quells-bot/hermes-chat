-- +goose Up
ALTER TABLE messages ADD COLUMN client_id TEXT;
ALTER TABLE messages ADD COLUMN edited_at INTEGER;
CREATE UNIQUE INDEX messages_client_id ON messages (room_id, author_human_id, client_id) WHERE client_id IS NOT NULL;
CREATE INDEX messages_streaming ON messages (room_id, author_agent_id, id) WHERE edited_at IS NOT NULL AND finalized_at IS NULL;
CREATE INDEX deliveries_working ON deliveries (room_id, agent_id) WHERE state = 'working';

-- +goose Down
DROP INDEX deliveries_working;
DROP INDEX messages_streaming;
DROP INDEX messages_client_id;
ALTER TABLE messages DROP COLUMN edited_at;
ALTER TABLE messages DROP COLUMN client_id;
