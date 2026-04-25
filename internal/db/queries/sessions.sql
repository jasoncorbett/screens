-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at)
VALUES (?, ?, ?, ?);

-- name: GetSessionByTokenHash :one
SELECT token_hash, user_id, csrf_token, created_at, expires_at
FROM sessions
WHERE token_hash = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = ?;

-- name: DeleteSessionsByUserID :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < datetime('now');
