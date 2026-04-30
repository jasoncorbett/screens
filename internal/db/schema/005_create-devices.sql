CREATE TABLE IF NOT EXISTS devices (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    created_by   TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at TEXT,
    revoked_at   TEXT
);

CREATE INDEX idx_devices_token_hash ON devices(token_hash);
CREATE INDEX idx_devices_revoked_at ON devices(revoked_at);
