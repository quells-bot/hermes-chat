-- name: InsertMessage :exec
INSERT INTO messages (id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetMessage :one
SELECT id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at FROM messages WHERE id = ?;

-- name: ListNewestMessages :many
SELECT id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at FROM messages
WHERE room_id = ?
ORDER BY id DESC LIMIT ?;

-- name: ListMessagesBefore :many
SELECT id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at FROM messages
WHERE room_id = ? AND id < ?
ORDER BY id DESC LIMIT ?;

-- name: GetLastMessageID :one
SELECT id FROM messages WHERE room_id = ? ORDER BY id DESC LIMIT 1;

-- name: SetMessageBody :execrows
UPDATE messages SET body = ? WHERE id = ?;
