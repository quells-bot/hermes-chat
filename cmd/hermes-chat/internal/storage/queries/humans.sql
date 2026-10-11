-- name: InsertHuman :exec
INSERT INTO humans (id, username, password_hash, created_at) VALUES (?, ?, ?, ?);

-- name: GetHumanByUsername :one
SELECT id, username, created_at, password_hash FROM humans WHERE username = ?;

-- name: GetHumanByID :one
SELECT id, username, created_at, password_hash FROM humans WHERE id = ?;

-- name: ListHumans :many
SELECT id, username, created_at, password_hash FROM humans ORDER BY username;

-- name: SetHumanPasswordHash :execrows
UPDATE humans SET password_hash = ? WHERE id = ?;

-- name: DeleteHuman :execrows
DELETE FROM humans WHERE id = ?;
