-- Immutable, versioned query templates (§5). Published templates are the
-- preferred entry point for production complex queries: the canonical AST is
-- fixed at publish time, cost-verified, and immutable — a new release is a new
-- version, never an in-place edit. DRAFT → PUBLISHED → DEPRECATED/DISABLED.
--
-- The canonical_ast is the Warden Query AST (JSON, §4.3); the control plane
-- injects it into database.query_template.execute dispatch so the Connector
-- never resolves templates from Agent input. bindings are the runtime
-- parameters an Agent may supply; identifiers, table/JOIN structure, SELECT
-- fields, aggregates, operators and sort are fixed and cannot be substituted.

CREATE TABLE IF NOT EXISTS database_query_templates (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id     UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    schema_resource_id VARCHAR(512) NOT NULL,           -- database_schema external id
    name               VARCHAR(128) NOT NULL,
    version            INTEGER NOT NULL,
    status             VARCHAR(16) NOT NULL DEFAULT 'DRAFT'
                       CHECK (status IN ('DRAFT','PUBLISHED','DEPRECATED','DISABLED')),
    canonical_ast      JSONB NOT NULL,
    parameter_schema   JSONB NOT NULL DEFAULT '{}',
    resource_bindings  JSONB NOT NULL DEFAULT '[]',     -- related database_table external ids
    result_schema      JSONB,
    max_rows           INTEGER NOT NULL DEFAULT 100,
    max_result_bytes   INTEGER NOT NULL DEFAULT 1048576,
    timeout_seconds    INTEGER NOT NULL DEFAULT 30,
    cost_policy        JSONB NOT NULL DEFAULT '{}',     -- per-template EXPLAIN gate overrides
    sensitivity        VARCHAR(16) NOT NULL DEFAULT 'INTERNAL'
                       CHECK (sensitivity IN ('PUBLIC','INTERNAL','CONFIDENTIAL','PII','SECRET')),
    owner              VARCHAR(255),
    created_by         VARCHAR(255),
    approved_by        VARCHAR(255),
    published_at       TIMESTAMPTZ,
    template_hash      VARCHAR(64) NOT NULL,            -- sha256 hex of canonical_ast
    tenant_id          VARCHAR(255) NOT NULL DEFAULT 'default',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(integration_id, name, version)
);

CREATE INDEX IF NOT EXISTS idx_database_query_templates_integration
    ON database_query_templates(integration_id, name, status);

DROP TRIGGER IF EXISTS trg_database_query_templates_updated_at ON database_query_templates;
CREATE TRIGGER trg_database_query_templates_updated_at BEFORE UPDATE ON database_query_templates
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
