ALTER TABLE executions
    ADD COLUMN IF NOT EXISTS dispatch_token UUID,
    ADD COLUMN IF NOT EXISTS dispatch_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS dispatched_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS attempt INTEGER NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_executions_dispatch_token
    ON executions(dispatch_token) WHERE dispatch_token IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_executions_dispatch_expiry
    ON executions(dispatch_expires_at) WHERE status = 'DISPATCHED';
