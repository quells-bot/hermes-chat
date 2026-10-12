-- name: InsertRoom :exec
INSERT INTO rooms (id, topic, dm_human_id, dm_agent_id, created_at, archived_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetRoom :one
SELECT id, topic, dm_human_id, dm_agent_id, created_at, archived_at, paused_at, pause_reason, turn_cap, agent_turns FROM rooms WHERE id = ?;

-- name: GetDMRoom :one
SELECT id, topic, dm_human_id, dm_agent_id, created_at, archived_at, paused_at, pause_reason, turn_cap, agent_turns FROM rooms WHERE dm_human_id = ? AND dm_agent_id = ?;

-- name: ListRoomsByHuman :many
SELECT r.id, r.topic, r.dm_human_id, r.dm_agent_id, r.created_at, r.archived_at, r.paused_at, r.pause_reason, r.turn_cap, r.agent_turns
FROM rooms r JOIN room_humans rh ON rh.room_id = r.id
WHERE rh.human_id = ?
ORDER BY r.id;

-- name: SetRoomTopic :execrows
UPDATE rooms SET topic = ? WHERE id = ?;

-- name: SetRoomArchivedAt :execrows
UPDATE rooms SET archived_at = ? WHERE id = ?;

-- name: InsertRoomHuman :exec
INSERT INTO room_humans (room_id, human_id, joined_at, last_read_message_id) VALUES (?, ?, ?, ?);

-- name: GetRoomHuman :one
SELECT room_id, human_id, joined_at, last_read_message_id FROM room_humans WHERE room_id = ? AND human_id = ?;

-- name: ListRoomHumans :many
SELECT h.id, h.username, h.created_at
FROM humans h JOIN room_humans rh ON rh.human_id = h.id
WHERE rh.room_id = ?
ORDER BY h.username;

-- name: SetRoomHumanLastRead :execrows
UPDATE room_humans SET last_read_message_id = ? WHERE room_id = ? AND human_id = ?;

-- name: InsertRoomAgent :exec
INSERT INTO room_agents (room_id, agent_id, joined_at) VALUES (?, ?, ?);

-- name: DeleteRoomAgent :execrows
DELETE FROM room_agents WHERE room_id = ? AND agent_id = ?;

-- name: ListRoomAgents :many
SELECT a.id, a.handle, a.display_name, a.token_hash, a.revoked_at, a.created_at
FROM agents a JOIN room_agents ra ON ra.agent_id = a.id
WHERE ra.room_id = ?
ORDER BY a.handle;

-- name: SetRoomPause :execrows
UPDATE rooms SET paused_at = ?, pause_reason = ? WHERE id = ?;

-- name: SetRoomTurnCap :execrows
UPDATE rooms SET turn_cap = ? WHERE id = ?;

-- name: SetRoomAgentTurns :execrows
UPDATE rooms SET agent_turns = ? WHERE id = ?;

-- name: ListAllRoomAgents :many
SELECT room_id, agent_id, joined_at FROM room_agents ORDER BY room_id, agent_id;
