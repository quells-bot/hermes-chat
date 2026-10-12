-- +goose Up
CREATE TABLE attachments (
    id                TEXT    NOT NULL PRIMARY KEY,
    room_id           TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    message_id        TEXT    REFERENCES messages (id) ON DELETE CASCADE,
    uploader_human_id TEXT    REFERENCES humans (id) ON DELETE SET NULL,
    uploader_agent_id TEXT    REFERENCES agents (id),
    name              TEXT    NOT NULL,
    content_type      TEXT    NOT NULL,
    size              INTEGER NOT NULL CHECK (size >= 0),
    created_at        INTEGER NOT NULL,
    CHECK (uploader_human_id IS NULL OR uploader_agent_id IS NULL)
) STRICT;

CREATE INDEX attachments_message ON attachments (message_id, id) WHERE message_id IS NOT NULL;

-- +goose Down
DROP TABLE attachments;