-- name: CreateTheme :exec
INSERT INTO themes (
    id, name, is_default,
    color_bg, color_surface, color_border, color_text, color_text_muted, color_accent,
    font_family, font_family_mono, radius
) VALUES (
    ?, ?, ?,
    ?, ?, ?, ?, ?, ?,
    ?, ?, ?
);

-- name: GetThemeByID :one
SELECT id, name, is_default, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, font_family_mono, radius, created_at, updated_at
FROM themes
WHERE id = ?;

-- name: GetThemeByName :one
SELECT id, name, is_default, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, font_family_mono, radius, created_at, updated_at
FROM themes
WHERE name = ?;

-- name: GetDefaultTheme :one
SELECT id, name, is_default, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, font_family_mono, radius, created_at, updated_at
FROM themes
WHERE is_default = 1
LIMIT 1;

-- name: ListThemes :many
SELECT id, name, is_default, color_bg, color_surface, color_border, color_text, color_text_muted, color_accent, font_family, font_family_mono, radius, created_at, updated_at
FROM themes
ORDER BY name;

-- name: UpdateTheme :exec
UPDATE themes
   SET name = ?,
       color_bg = ?, color_surface = ?, color_border = ?,
       color_text = ?, color_text_muted = ?, color_accent = ?,
       font_family = ?, font_family_mono = ?, radius = ?,
       updated_at = datetime('now')
 WHERE id = ?;

-- name: DeleteTheme :execresult
DELETE FROM themes WHERE id = ? AND is_default = 0;

-- name: ClearDefaultTheme :exec
UPDATE themes SET is_default = 0 WHERE is_default = 1;

-- name: SetDefaultTheme :execresult
UPDATE themes SET is_default = 1 WHERE id = ?;

-- name: CountDefaultThemes :one
SELECT COUNT(*) FROM themes WHERE is_default = 1;
