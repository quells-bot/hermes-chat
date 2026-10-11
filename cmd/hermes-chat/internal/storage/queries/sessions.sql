-- name: InsertSession :exec
INSERT INTO sessions (id, token_hash, human_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?);

-- name: GetSessionByTokenHash :one
SELECT id, token_hash, human_id, created_at, expires_at FROM sessions WHERE token_hash = ?;

-- name: DeleteSessionByTokenHash :exec
DELETE FROM sessions WHERE token_hash = ?;

-- name: DeleteSessionsByHuman :exec
DELETE FROM sessions WHERE human_id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= ?;
