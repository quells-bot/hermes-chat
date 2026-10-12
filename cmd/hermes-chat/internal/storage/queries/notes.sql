-- name: GetNoteText :one
SELECT text FROM notes WHERE human_id = ?;

-- name: UpsertNoteText :exec
INSERT INTO notes (human_id, text, updated_at) VALUES (?, ?, ?)
ON CONFLICT (human_id) DO UPDATE SET text = excluded.text, updated_at = excluded.updated_at;

-- name: InsertNoteEntry :exec
INSERT INTO note_entries (id, human_id, agent_id, text, approved_at, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetNoteEntry :one
SELECT id, human_id, agent_id, text, approved_at, created_at FROM note_entries WHERE id = ?;

-- name: ListNoteEntries :many
SELECT id, human_id, agent_id, text, approved_at, created_at FROM note_entries WHERE human_id = ? ORDER BY id;

-- name: SetNoteEntryText :execrows
UPDATE note_entries SET text = ? WHERE id = ?;

-- name: SetNoteEntryApprovedAt :execrows
UPDATE note_entries SET approved_at = ? WHERE id = ?;

-- name: DeleteNoteEntry :execrows
DELETE FROM note_entries WHERE id = ?;