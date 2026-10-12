-- name: InsertEvent :one
INSERT INTO events (room_id, kind, subject_id, agent_id, created_at) VALUES (?, ?, ?, ?, ?) RETURNING id;

-- name: ListEventsForHuman :many
SELECT e.id, e.room_id, e.kind, e.subject_id, e.agent_id, e.created_at FROM events e
WHERE e.id > ? AND e.agent_id IS NULL
  AND e.room_id IN (SELECT m.room_id FROM room_humans m WHERE m.human_id = ?)
ORDER BY e.id LIMIT ?;

-- name: ListEventsForAgent :many
SELECT e.id, e.room_id, e.kind, e.subject_id, e.agent_id, e.created_at FROM events e
WHERE e.id > ? AND e.agent_id = ?
ORDER BY e.id LIMIT ?;

-- name: GetLastEventID :one
SELECT id FROM events ORDER BY id DESC LIMIT 1;

-- name: ListUnacknowledgedDeliveryEvents :many
SELECT e.id, e.room_id, e.kind, e.subject_id, e.agent_id, e.created_at FROM events e
JOIN deliveries d ON d.room_id = e.room_id AND d.agent_id = e.agent_id AND d.message_id = e.subject_id
WHERE e.agent_id = ? AND e.kind = 'delivery' AND e.id <= ? AND d.state = 'delivered'
ORDER BY e.id;
