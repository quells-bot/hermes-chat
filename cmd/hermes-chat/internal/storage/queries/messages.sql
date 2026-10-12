-- name: InsertMessage :exec
INSERT INTO messages (id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at, client_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetMessage :one
SELECT id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at, finalized_at, client_id, edited_at FROM messages WHERE id = ?;

-- name: ListNewestMessages :many
SELECT id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at, finalized_at, client_id, edited_at FROM messages
WHERE room_id = ?
ORDER BY id DESC LIMIT ?;

-- name: ListMessagesBefore :many
SELECT id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at, finalized_at, client_id, edited_at FROM messages
WHERE room_id = ? AND id < ?
ORDER BY id DESC LIMIT ?;

-- name: GetLastMessageID :one
SELECT id FROM messages WHERE room_id = ? ORDER BY id DESC LIMIT 1;

-- name: SetMessageBody :execrows
UPDATE messages SET body = ?, edited_at = ? WHERE id = ?;

-- name: FinalizeMessage :execrows
UPDATE messages SET body = ?, finalized_at = ? WHERE id = ?;

-- name: GetMessageByClientID :one
SELECT id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at, finalized_at, client_id, edited_at FROM messages
WHERE room_id = ? AND author_human_id = ? AND client_id = ?;

-- name: ListStreamingMessages :many
SELECT id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at, finalized_at, client_id, edited_at FROM messages
WHERE room_id = ? AND author_agent_id = ? AND id > ? AND edited_at IS NOT NULL AND finalized_at IS NULL
ORDER BY id;
