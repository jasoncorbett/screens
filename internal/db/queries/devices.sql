-- name: CreateDevice :exec
INSERT INTO devices (id, name, token_hash, created_by)
VALUES (?, ?, ?, ?);

-- name: GetDeviceByTokenHash :one
SELECT id, name, token_hash, created_by, created_at, last_seen_at, revoked_at
FROM devices
WHERE token_hash = ?;

-- name: GetDeviceByID :one
SELECT id, name, token_hash, created_by, created_at, last_seen_at, revoked_at
FROM devices
WHERE id = ?;

-- name: ListDevices :many
SELECT id, name, token_hash, created_by, created_at, last_seen_at, revoked_at
FROM devices
ORDER BY created_at;

-- name: RevokeDevice :exec
UPDATE devices SET revoked_at = datetime('now') WHERE id = ? AND revoked_at IS NULL;

-- name: TouchDeviceSeen :execresult
UPDATE devices
   SET last_seen_at = datetime('now')
 WHERE id = ?
   AND (last_seen_at IS NULL OR last_seen_at < datetime('now', ?));

-- name: RotateDeviceToken :execresult
UPDATE devices
   SET token_hash = ?
 WHERE id = ?
   AND revoked_at IS NULL;
