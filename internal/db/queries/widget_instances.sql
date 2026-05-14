-- name: CreateWidgetInstance :exec
INSERT INTO widget_instances (id, page_id, type, config, position)
VALUES (?, ?, ?, ?, ?);

-- name: GetWidgetInstanceByID :one
SELECT id, page_id, type, config, position, created_at, updated_at
FROM widget_instances
WHERE id = ? AND page_id = ?;

-- name: ListWidgetInstancesByPage :many
SELECT id, page_id, type, config, position, created_at, updated_at
FROM widget_instances
WHERE page_id = ?
ORDER BY position;

-- name: ListWidgetInstancesByPageIDs :many
SELECT id, page_id, type, config, position, created_at, updated_at
FROM widget_instances
WHERE page_id IN (sqlc.slice('page_ids'))
ORDER BY page_id, position;

-- name: MaxWidgetPosition :one
SELECT COALESCE(MAX(position), 0) FROM widget_instances WHERE page_id = ?;

-- name: DeleteWidgetInstance :execresult
DELETE FROM widget_instances WHERE id = ? AND page_id = ?;

-- name: GetWidgetNeighbor :one
SELECT id, page_id, type, config, position, created_at, updated_at
FROM widget_instances
WHERE page_id = ? AND position = ?;

-- name: SetWidgetPosition :exec
UPDATE widget_instances SET position = ?, updated_at = datetime('now')
WHERE id = ? AND page_id = ?;
