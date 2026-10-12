-- name: InsertAttachment :exec
INSERT INTO attachments (id, room_id, uploader_human_id, uploader_agent_id, name, content_type, size, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAttachment :one
SELECT id, room_id, message_id, uploader_human_id, uploader_agent_id, name, content_type, size, created_at FROM attachments WHERE id = ?;

-- name: AttachToMessage :execrows
UPDATE attachments SET message_id = ? WHERE id = ? AND message_id IS NULL;

-- name: ListAttachmentsInRange :many
SELECT id, room_id, message_id, uploader_human_id, uploader_agent_id, name, content_type, size, created_at FROM attachments
WHERE room_id = ? AND message_id >= ? AND message_id <= ?
ORDER BY message_id, id;