-- name: GetUserByEmail :one
SELECT id, email, display_name, role, active, created_at, updated_at
FROM users
WHERE email = ?;

-- name: GetUserByID :one
SELECT id, email, display_name, role, active, created_at, updated_at
FROM users
WHERE id = ?;

-- name: CreateUser :one
INSERT INTO users (id, email, display_name, role, active)
VALUES (?, ?, ?, ?, 1)
RETURNING id, email, display_name, role, active, created_at, updated_at;

-- name: ListUsers :many
SELECT id, email, display_name, role, active, created_at, updated_at
FROM users
ORDER BY created_at;

-- name: DeactivateUser :exec
UPDATE users SET active = 0, updated_at = datetime('now')
WHERE id = ?;

-- name: CountActiveAdmins :one
SELECT COUNT(*) FROM users WHERE role = 'admin' AND active = 1;
