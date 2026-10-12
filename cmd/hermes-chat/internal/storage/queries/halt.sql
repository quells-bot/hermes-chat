-- name: GetHaltedAt :one
SELECT halted_at FROM halt WHERE id = 1;

-- name: SetHaltedAt :execrows
UPDATE halt SET halted_at = ? WHERE id = 1;
