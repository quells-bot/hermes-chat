-- +goose Up
CREATE TABLE rooms (
    id          TEXT    NOT NULL PRIMARY KEY,
    topic       TEXT    NOT NULL,
    dm_human_id TEXT    REFERENCES humans (id) ON DELETE CASCADE,
    dm_agent_id TEXT    REFERENCES agents (id),
    created_at  INTEGER NOT NULL,
    archived_at INTEGER,
    CHECK ((dm_human_id IS NULL) = (dm_agent_id IS NULL))
) STRICT;

CREATE UNIQUE INDEX rooms_dm ON rooms (dm_human_id, dm_agent_id) WHERE dm_human_id IS NOT NULL;

CREATE TABLE room_humans (
    room_id              TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    human_id             TEXT    NOT NULL REFERENCES humans (id) ON DELETE CASCADE,
    joined_at            INTEGER NOT NULL,
    last_read_message_id TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (room_id, human_id)
) STRICT;

CREATE INDEX room_humans_human_id ON room_humans (human_id);

CREATE TABLE room_agents (
    room_id   TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    agent_id  TEXT    NOT NULL REFERENCES agents (id),
    joined_at INTEGER NOT NULL,
    PRIMARY KEY (room_id, agent_id)
) STRICT;

CREATE TABLE messages (
    id              TEXT    NOT NULL PRIMARY KEY,
    room_id         TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    author_human_id TEXT    REFERENCES humans (id) ON DELETE SET NULL,
    author_agent_id TEXT    REFERENCES agents (id),
    reply_to_id     TEXT    REFERENCES messages (id),
    body            TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    CHECK (author_human_id IS NULL OR author_agent_id IS NULL)
) STRICT;

CREATE INDEX messages_room_id ON messages (room_id, id);

CREATE TABLE events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    room_id    TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL,
    subject_id TEXT    NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX events_room_id ON events (room_id, id);

-- +goose Down
DROP TABLE events;
DROP TABLE messages;
DROP TABLE room_agents;
DROP TABLE room_humans;
DROP TABLE rooms;
