-- name: InsertHuman :exec
INSERT INTO humans (id, username, created_at) VALUES (?, ?, ?);

-- name: GetHumanByUsername :one
SELECT id, username, created_at FROM humans WHERE username = ?;

-- name: ListHumans :many
SELECT id, username, created_at FROM humans ORDER BY username;
