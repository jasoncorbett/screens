CREATE TABLE IF NOT EXISTS widget_instances (
    id          TEXT PRIMARY KEY,
    page_id     TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,
    config      TEXT NOT NULL,
    position    INTEGER NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX idx_widget_instances_page_position ON widget_instances(page_id, position);
CREATE INDEX idx_widget_instances_page_id ON widget_instances(page_id);
