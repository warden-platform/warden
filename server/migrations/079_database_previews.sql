-- Persistent preview artifacts for governed queries (§17). A preview captures
-- the plan hash, the EXPLAIN summary and the cost verdict so the execute step
-- can prove the plan did not drift between preview and apply. preview_hash is
-- the token the Agent echoes back; the Connector recomputes the plan and
-- compares. Artifacts expire and are pruned so PAM does not retain raw SQL
-- indefinitely.

CREATE TABLE IF NOT EXISTS database_previews (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id  UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    resource_id     UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    plan_hash       VARCHAR(64) NOT NULL,
    preview_hash    VARCHAR(64) NOT NULL,
    rendered_sql    TEXT NOT NULL,
    estimated_rows  BIGINT,
    estimated_cost  DOUBLE PRECISION,
    full_scan       BOOLEAN NOT NULL DEFAULT false,
    using_temp_table BOOLEAN NOT NULL DEFAULT false,
    using_filesort  BOOLEAN NOT NULL DEFAULT false,
    verdict         VARCHAR(16) NOT NULL CHECK (verdict IN ('allow','require_approval','reject')),
    warnings        JSONB NOT NULL DEFAULT '[]',
    governance      JSONB NOT NULL DEFAULT '{}',
    masking         JSONB NOT NULL DEFAULT '{}',
    tenant_id       VARCHAR(255) NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    UNIQUE(preview_hash)
);

CREATE INDEX IF NOT EXISTS idx_database_previews_expiry ON database_previews(expires_at);
CREATE INDEX IF NOT EXISTS idx_database_previews_integration ON database_previews(integration_id, created_at);
