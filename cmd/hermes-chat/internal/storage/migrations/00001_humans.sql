-- +goose Up
CREATE TABLE humans (
    id         TEXT    NOT NULL PRIMARY KEY,
    username   TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE humans;
