ALTER TABLE connectors
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_connectors_archived_at ON connectors(archived_at);
