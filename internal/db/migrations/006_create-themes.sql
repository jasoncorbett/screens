-- +up
CREATE TABLE IF NOT EXISTS themes (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL UNIQUE,
    is_default       INTEGER NOT NULL DEFAULT 0,
    color_bg         TEXT NOT NULL,
    color_surface    TEXT NOT NULL,
    color_border     TEXT NOT NULL,
    color_text       TEXT NOT NULL,
    color_text_muted TEXT NOT NULL,
    color_accent     TEXT NOT NULL,
    font_family      TEXT NOT NULL,
    font_family_mono TEXT NOT NULL DEFAULT '',
    radius           TEXT NOT NULL,
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX themes_one_default ON themes(is_default) WHERE is_default = 1;

-- +down
DROP INDEX IF EXISTS themes_one_default;
DROP TABLE IF EXISTS themes;
