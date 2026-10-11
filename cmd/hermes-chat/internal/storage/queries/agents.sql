-- name: InsertAgent :exec
INSERT INTO agents (id, handle, display_name, token_hash, revoked_at, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetAgentByID :one
SELECT id, handle, display_name, token_hash, revoked_at, created_at FROM agents WHERE id = ?;

-- name: GetAgentByHandle :one
SELECT id, handle, display_name, token_hash, revoked_at, created_at FROM agents WHERE handle = ?;

-- name: GetAgentByTokenHash :one
SELECT id, handle, display_name, token_hash, revoked_at, created_at FROM agents WHERE token_hash = ?;

-- name: ListAgents :many
SELECT id, handle, display_name, token_hash, revoked_at, created_at FROM agents ORDER BY handle;

-- name: SetAgentTokenHash :execrows
UPDATE agents SET token_hash = ? WHERE id = ?;

-- name: SetAgentRevokedAt :execrows
UPDATE agents SET revoked_at = ? WHERE id = ?;
