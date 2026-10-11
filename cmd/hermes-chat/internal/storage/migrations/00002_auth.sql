-- +goose Up
ALTER TABLE humans ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';

CREATE TABLE agents (
    id           TEXT    NOT NULL PRIMARY KEY,
    handle       TEXT    NOT NULL UNIQUE,
    display_name TEXT    NOT NULL,
    token_hash   TEXT    NOT NULL UNIQUE,
    revoked_at   INTEGER,
    created_at   INTEGER NOT NULL
) STRICT;

CREATE TABLE sessions (
    id         TEXT    NOT NULL PRIMARY KEY,
    token_hash TEXT    NOT NULL UNIQUE,
    human_id   TEXT    NOT NULL REFERENCES humans (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_human_id ON sessions (human_id);

-- +goose Down
DROP TABLE sessions;
DROP TABLE agents;
ALTER TABLE humans DROP COLUMN password_hash;
