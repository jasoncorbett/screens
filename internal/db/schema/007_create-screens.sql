CREATE TABLE IF NOT EXISTS screens (
    id                         TEXT PRIMARY KEY,
    name                       TEXT NOT NULL UNIQUE,
    theme_id                   TEXT NOT NULL REFERENCES themes(id) ON DELETE RESTRICT,
    rotation_interval_seconds  INTEGER NOT NULL DEFAULT 30,
    created_at                 TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at                 TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_screens_theme_id ON screens(theme_id);
