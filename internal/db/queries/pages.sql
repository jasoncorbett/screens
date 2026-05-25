-- name: CreatePage :exec
INSERT INTO pages (id, screen_id, name, position)
VALUES (?, ?, ?, ?);

-- name: GetPageByID :one
SELECT id, screen_id, name, position, created_at, updated_at
FROM pages
WHERE id = ? AND screen_id = ?;

-- name: ListPagesByScreen :many
SELECT id, screen_id, name, position, created_at, updated_at
FROM pages
WHERE screen_id = ?
ORDER BY position;

-- name: MaxPagePosition :one
SELECT CAST(COALESCE(MAX(position), 0) AS INTEGER) FROM pages WHERE screen_id = ?;

-- name: UpdatePage :exec
UPDATE pages SET name = ?, updated_at = datetime('now')
WHERE id = ? AND screen_id = ?;

-- name: DeletePage :execresult
DELETE FROM pages WHERE id = ? AND screen_id = ?;

-- name: GetPageNeighbor :one
SELECT id, screen_id, name, position, created_at, updated_at
FROM pages
WHERE screen_id = ? AND position = ?;

-- name: SetPagePosition :exec
UPDATE pages SET position = ?, updated_at = datetime('now')
WHERE id = ? AND screen_id = ?;
