-- name: InsertDelivery :exec
INSERT INTO deliveries (room_id, agent_id, message_id, state) VALUES (?, ?, ?, ?);

-- name: GetDelivery :one
SELECT room_id, agent_id, message_id, state FROM deliveries WHERE agent_id = ? AND message_id = ?;

-- name: ListDeliveriesForMessage :many
SELECT d.room_id, d.agent_id, d.message_id, d.state
FROM deliveries d JOIN agents a ON a.id = d.agent_id
WHERE d.room_id = ? AND d.message_id = ?
ORDER BY a.handle;

-- name: ListDeliveriesInRange :many
SELECT d.room_id, d.agent_id, d.message_id, d.state
FROM deliveries d JOIN agents a ON a.id = d.agent_id
WHERE d.room_id = ? AND d.message_id >= ? AND d.message_id <= ?
ORDER BY d.message_id, a.handle;

-- name: HeadWaiting :one
SELECT d.room_id, d.agent_id, d.message_id, d.state FROM deliveries d
WHERE d.room_id = ? AND d.agent_id = ? AND d.state = 'waiting'
  AND NOT EXISTS (SELECT 1 FROM deliveries x
                  WHERE x.room_id = d.room_id AND x.agent_id = d.agent_id
                    AND x.state IN ('delivered','working'))
ORDER BY d.message_id LIMIT 1;

-- name: ReleasableDeliveries :many
SELECT d.room_id, d.agent_id, d.message_id, d.state FROM deliveries d
WHERE d.agent_id = ? AND d.state = 'waiting'
  AND NOT EXISTS (SELECT 1 FROM deliveries x
                  WHERE x.room_id = d.room_id AND x.agent_id = d.agent_id
                    AND x.state IN ('delivered','working'))
  AND NOT EXISTS (SELECT 1 FROM deliveries w
                  WHERE w.room_id = d.room_id AND w.agent_id = d.agent_id
                    AND w.state = 'waiting' AND w.message_id < d.message_id)
ORDER BY d.room_id;

-- name: SetDeliveryState :execrows
UPDATE deliveries SET state = ? WHERE room_id = ? AND agent_id = ? AND message_id = ?;

-- name: CancelDeliveries :execrows
UPDATE deliveries SET state = 'cancelled' WHERE room_id = ? AND agent_id = ? AND state = 'waiting';

-- name: ListAgentRoomIDs :many
SELECT room_id FROM room_agents WHERE agent_id = ? ORDER BY room_id;

-- name: GetWorkingDelivery :one
SELECT room_id, agent_id, message_id, state FROM deliveries
WHERE room_id = ? AND agent_id = ? AND state = 'working'
ORDER BY message_id LIMIT 1;
-- name: GetDeliveredDelivery :one
SELECT room_id, agent_id, message_id, state FROM deliveries
WHERE room_id = ? AND agent_id = ? AND state = 'delivered'
ORDER BY message_id LIMIT 1;
