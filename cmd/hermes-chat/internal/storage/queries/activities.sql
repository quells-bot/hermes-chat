-- name: InsertActivity :exec
INSERT INTO activities (id, room_id, agent_id, message_id, kind, text, tool, preview, arguments, duration_ms, succeeded, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetActivity :one
SELECT id, room_id, agent_id, message_id, kind, text, tool, preview, arguments, duration_ms, succeeded, created_at FROM activities WHERE id = ?;

-- name: SetActivityResult :execrows
UPDATE activities SET duration_ms = ?, succeeded = ? WHERE id = ? AND kind = 'tool';

-- name: ListActivities :many
SELECT id, room_id, agent_id, message_id, kind, text, tool, preview, arguments, duration_ms, succeeded, created_at FROM activities
WHERE room_id = ? AND message_id = ?
ORDER BY id;