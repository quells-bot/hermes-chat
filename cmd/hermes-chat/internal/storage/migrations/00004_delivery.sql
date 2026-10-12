-- +goose Up
ALTER TABLE events ADD COLUMN agent_id TEXT REFERENCES agents (id);
CREATE INDEX events_agent ON events (agent_id, id) WHERE agent_id IS NOT NULL;

CREATE TABLE deliveries (
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL REFERENCES agents (id),
    message_id TEXT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    state      TEXT NOT NULL,
    PRIMARY KEY (room_id, agent_id, message_id),
    CHECK (state IN ('waiting','delivered','working','done','cancelled','failed'))
) STRICT;

CREATE INDEX deliveries_agent_message ON deliveries (agent_id, message_id);

CREATE TABLE activities (
    id          TEXT    NOT NULL PRIMARY KEY,
    room_id     TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    agent_id    TEXT    NOT NULL REFERENCES agents (id),
    message_id  TEXT    NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    kind        TEXT    NOT NULL,
    text        TEXT    NOT NULL DEFAULT '',
    tool        TEXT    NOT NULL DEFAULT '',
    preview     TEXT    NOT NULL DEFAULT '',
    arguments   TEXT    NOT NULL DEFAULT '',
    duration_ms INTEGER,
    succeeded   INTEGER,
    created_at  INTEGER NOT NULL,
    CHECK (kind IN ('remark','tool')),
    CHECK (succeeded IS NULL OR succeeded IN (0,1)),
    CHECK ((kind = 'remark' AND text <> '' AND tool = '' AND preview = '' AND arguments = '')
        OR (kind = 'tool' AND tool <> ''))
) STRICT;

CREATE INDEX activities_message ON activities (message_id, id);

-- +goose Down
DROP TABLE activities;
DROP TABLE deliveries;
DROP INDEX events_agent;
ALTER TABLE events DROP COLUMN agent_id;