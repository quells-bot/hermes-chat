-- name: InsertPrompt :exec
INSERT INTO prompts (message_id, room_id, agent_id, kind, choices) VALUES (?, ?, ?, ?, ?);

-- name: GetPrompt :one
SELECT message_id, room_id, agent_id, kind, choices, state, answer, answered_by, closed_at FROM prompts WHERE message_id = ?;

-- name: ListPromptsInRange :many
SELECT message_id, room_id, agent_id, kind, choices, state, answer, answered_by, closed_at FROM prompts
WHERE room_id = ? AND message_id >= ? AND message_id <= ?
ORDER BY message_id;

-- name: ListOpenPrompts :many
SELECT message_id, room_id, agent_id, kind, choices, state, answer, answered_by, closed_at FROM prompts
WHERE state = 'open' ORDER BY message_id;

-- name: ListOpenPromptsInRoom :many
SELECT message_id, room_id, agent_id, kind, choices, state, answer, answered_by, closed_at FROM prompts
WHERE room_id = ? AND state = 'open' ORDER BY message_id;

-- name: CountOpenPrompts :one
SELECT COUNT(*) FROM prompts WHERE room_id = ? AND state = 'open';

-- name: ClosePrompt :execrows
UPDATE prompts SET state = ?, answer = ?, answered_by = ?, closed_at = ? WHERE message_id = ? AND state = 'open';