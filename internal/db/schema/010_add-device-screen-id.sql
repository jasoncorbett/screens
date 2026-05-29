ALTER TABLE devices ADD COLUMN screen_id TEXT
    REFERENCES screens(id) ON DELETE SET NULL;

CREATE INDEX idx_devices_screen_id ON devices(screen_id);
