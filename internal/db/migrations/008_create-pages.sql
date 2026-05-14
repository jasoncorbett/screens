-- +up
CREATE TABLE IF NOT EXISTS pages (
    id          TEXT PRIMARY KEY,
    screen_id   TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    name        TEXT NOT NULL DEFAULT '',
    position    INTEGER NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX idx_pages_screen_position ON pages(screen_id, position);
CREATE INDEX idx_pages_screen_id ON pages(screen_id);

-- +down
DROP INDEX IF EXISTS idx_pages_screen_id;
DROP INDEX IF EXISTS idx_pages_screen_position;
DROP TABLE IF EXISTS pages;
