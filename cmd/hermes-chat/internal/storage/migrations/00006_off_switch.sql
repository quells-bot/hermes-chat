-- +goose Up
ALTER TABLE rooms ADD COLUMN paused_at INTEGER;
ALTER TABLE rooms ADD COLUMN pause_reason TEXT NOT NULL DEFAULT '' CHECK (pause_reason IN ('', 'human', 'turn_cap'));
ALTER TABLE rooms ADD COLUMN turn_cap INTEGER NOT NULL DEFAULT 6 CHECK (turn_cap BETWEEN 1 AND 100);
ALTER TABLE rooms ADD COLUMN agent_turns INTEGER NOT NULL DEFAULT 0 CHECK (agent_turns >= 0);

CREATE TABLE halt (
    id        INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    halted_at INTEGER
) STRICT;

INSERT INTO halt (id) VALUES (1);

-- +goose Down
DROP TABLE halt;
ALTER TABLE rooms DROP COLUMN agent_turns;
ALTER TABLE rooms DROP COLUMN turn_cap;
ALTER TABLE rooms DROP COLUMN pause_reason;
ALTER TABLE rooms DROP COLUMN paused_at;
