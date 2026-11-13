-- Column-level classification for governed database results (§13.2). The
-- control plane owns classifications; the Connector applies the resulting
-- output policy (allow/mask/hash/deny) to each returned cell so business data
-- never leaves the Connector unmasked. SECRET defaults to deny, PII to mask;
-- unclassified columns are fail-closed in production.

CREATE TABLE IF NOT EXISTS database_column_classifications (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    schema_name    VARCHAR(64) NOT NULL,
    table_name     VARCHAR(64) NOT NULL,
    column_name    VARCHAR(64) NOT NULL,
    classification VARCHAR(16) NOT NULL CHECK (classification IN ('PUBLIC','INTERNAL','CONFIDENTIAL','PII','SECRET')),
    output_policy  VARCHAR(16) NOT NULL CHECK (output_policy IN ('allow','mask','hash','deny')),
    tenant_id      VARCHAR(255) NOT NULL DEFAULT 'default',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(integration_id, schema_name, table_name, column_name)
);

CREATE INDEX IF NOT EXISTS idx_database_column_classifications_integration
    ON database_column_classifications(integration_id, schema_name, table_name);

DROP TRIGGER IF EXISTS trg_database_column_classifications_updated_at ON database_column_classifications;
CREATE TRIGGER trg_database_column_classifications_updated_at BEFORE UPDATE ON database_column_classifications
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
