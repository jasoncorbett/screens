-- +up
ALTER TABLE devices ADD COLUMN screen_id TEXT
    REFERENCES screens(id) ON DELETE SET NULL;

CREATE INDEX idx_devices_screen_id ON devices(screen_id);

-- +down
DROP INDEX IF EXISTS idx_devices_screen_id;
ALTER TABLE devices DROP COLUMN screen_id;
