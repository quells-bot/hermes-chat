-- +goose Up
CREATE TABLE prompts (
    message_id  TEXT    NOT NULL PRIMARY KEY REFERENCES messages (id) ON DELETE CASCADE,
    room_id     TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    agent_id    TEXT    NOT NULL REFERENCES agents (id),
    kind        TEXT    NOT NULL CHECK (kind IN ('approval', 'clarify')),
    choices     TEXT    NOT NULL,
    state       TEXT    NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'answered', 'expired')),
    answer      TEXT    NOT NULL DEFAULT '',
    answered_by TEXT    REFERENCES humans (id) ON DELETE SET NULL,
    closed_at   INTEGER,
    CHECK ((state = 'open') = (closed_at IS NULL))
) STRICT;

CREATE INDEX prompts_open ON prompts (room_id, message_id) WHERE state = 'open';

-- +goose Down
DROP TABLE prompts;