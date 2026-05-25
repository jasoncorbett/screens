-- name: CreateScreen :exec
INSERT INTO screens (id, name, theme_id, rotation_interval_seconds)
VALUES (?, ?, ?, ?);

-- name: GetScreenByID :one
SELECT id, name, theme_id, rotation_interval_seconds, created_at, updated_at
FROM screens
WHERE id = ?;

-- name: GetScreenByName :one
SELECT id, name, theme_id, rotation_interval_seconds, created_at, updated_at
FROM screens
WHERE name = ?;

-- name: ListScreens :many
SELECT id, name, theme_id, rotation_interval_seconds, created_at, updated_at
FROM screens
ORDER BY name;

-- name: ListScreenSummaries :many
SELECT
    s.id,
    s.name,
    s.theme_id,
    s.rotation_interval_seconds,
    s.created_at,
    s.updated_at,
    t.name AS theme_name,
    (SELECT COUNT(*) FROM pages p WHERE p.screen_id = s.id) AS page_count
FROM screens s
JOIN themes t ON t.id = s.theme_id
ORDER BY s.name;

-- name: UpdateScreen :exec
UPDATE screens
   SET name = ?,
       theme_id = ?,
       rotation_interval_seconds = ?,
       updated_at = datetime('now')
 WHERE id = ?;

-- name: DeleteScreen :execresult
DELETE FROM screens WHERE id = ?;

-- name: CountScreensUsingTheme :one
SELECT COUNT(*) FROM screens WHERE theme_id = ?;
