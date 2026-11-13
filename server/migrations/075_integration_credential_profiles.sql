-- Per-integration credential profiles for least-privilege database access.
-- A database integration may use distinct MySQL accounts for metadata reads,
-- SELECT-only queries, and DDL, each backed by its own K8s Secret reference.
-- The control plane routes actions to a profile by action type; the Agent can
-- never choose a profile (implementation plan safety invariant #3).

CREATE TABLE IF NOT EXISTS integration_credential_profiles (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    name           VARCHAR(64) NOT NULL,             -- 'query' | 'ddl'
    username       VARCHAR(255),
    credential_ref VARCHAR(253) NOT NULL,            -- K8s Secret name / Linux file name
    tenant_id      VARCHAR(255) NOT NULL DEFAULT 'default',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(integration_id, name)
);

CREATE INDEX IF NOT EXISTS idx_integration_credential_profiles_integration
    ON integration_credential_profiles(integration_id);

DROP TRIGGER IF EXISTS trg_integration_credential_profiles_updated_at ON integration_credential_profiles;
CREATE TRIGGER trg_integration_credential_profiles_updated_at BEFORE UPDATE ON integration_credential_profiles
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
