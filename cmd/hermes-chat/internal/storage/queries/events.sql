-- name: InsertEvent :one
INSERT INTO events (room_id, kind, subject_id, created_at) VALUES (?, ?, ?, ?) RETURNING id;

-- name: ListEventsForHuman :many
SELECT e.id, e.room_id, e.kind, e.subject_id, e.created_at FROM events e
WHERE e.id > ? AND e.room_id IN (SELECT m.room_id FROM room_humans m WHERE m.human_id = ?)
ORDER BY e.id LIMIT ?;

-- name: GetLastEventID :one
SELECT id FROM events ORDER BY id DESC LIMIT 1;
