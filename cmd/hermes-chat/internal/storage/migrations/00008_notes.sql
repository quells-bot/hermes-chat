-- +goose Up
CREATE TABLE notes (
    human_id   TEXT    NOT NULL PRIMARY KEY REFERENCES humans (id) ON DELETE CASCADE,
    text       TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE note_entries (
    id          TEXT    NOT NULL PRIMARY KEY,
    human_id    TEXT    NOT NULL REFERENCES humans (id) ON DELETE CASCADE,
    agent_id    TEXT    NOT NULL REFERENCES agents (id),
    text        TEXT    NOT NULL,
    approved_at INTEGER,
    created_at  INTEGER NOT NULL
) STRICT;

CREATE INDEX note_entries_human ON note_entries (human_id, id);

-- +goose Down
DROP TABLE note_entries;
DROP TABLE notes;