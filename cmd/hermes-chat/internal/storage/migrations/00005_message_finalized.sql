-- +goose Up
ALTER TABLE messages ADD COLUMN finalized_at INTEGER;

-- +goose Down
ALTER TABLE messages DROP COLUMN finalized_at;
