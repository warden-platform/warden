ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS sync_generation VARCHAR(100) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_resources_last_seen_at ON resources(last_seen_at);
CREATE INDEX IF NOT EXISTS idx_resources_sync_generation ON resources(connector_id, sync_generation);
