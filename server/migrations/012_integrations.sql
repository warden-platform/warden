-- First-class integrations: one cluster connector can host N integrations
-- (protocol=jenkins/postgres/...), each with its own endpoint, credential
-- reference, scope, and lifecycle. Replaces the connectors.config.jenkins JSONB
-- blob approach so "which Jenkins did I wire up?" is answerable by a query.

CREATE TABLE integrations (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connector_id   UUID NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    protocol       VARCHAR(50) NOT NULL,          -- 'jenkins'
    name           VARCHAR(255) NOT NULL,          -- display name, e.g. "prod-jenkins"
    endpoint       VARCHAR(512) NOT NULL,
    username       VARCHAR(255),
    credential_ref VARCHAR(253) NOT NULL,          -- K8s Secret name / Linux file name
    scope          JSONB NOT NULL DEFAULT '{}'::jsonb,    -- {allowedJobs:[...]}
    status         VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',  -- ACTIVE/DISABLED/FAILED
    last_probe_at  TIMESTAMPTZ,
    last_error     TEXT,
    tenant_id      VARCHAR(255) NOT NULL DEFAULT 'default',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(connector_id, protocol, name),
    UNIQUE(connector_id, protocol, endpoint)
);

CREATE INDEX idx_integrations_connector ON integrations(connector_id);
CREATE INDEX idx_integrations_protocol ON integrations(protocol);

CREATE TRIGGER trg_integrations_updated_at BEFORE UPDATE ON integrations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- Link resources to the integration that owns them. Native K8s/Linux resources
-- keep integration_id = NULL and route by resource type.
ALTER TABLE resources ADD COLUMN IF NOT EXISTS integration_id UUID REFERENCES integrations(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_resources_integration ON resources(integration_id) WHERE integration_id IS NOT NULL;

-- One-time migration: expand existing connectors.config.jenkins blobs into
-- integrations rows and associate existing jenkins_* resources with them.
DO $$
DECLARE r RECORD;
BEGIN
    FOR r IN SELECT id, config->'jenkins' AS j FROM connectors WHERE (config->'jenkins'->>'enabled') = 'true' LOOP
        INSERT INTO integrations (connector_id, protocol, name, endpoint, username, credential_ref, scope, status)
        VALUES (r.id, 'jenkins', 'jenkins',
                r.j->>'url', r.j->>'username', r.j->>'credentialRef',
                jsonb_build_object('allowedJobs', r.j->'allowedJobs'),
                'ACTIVE')
        ON CONFLICT DO NOTHING;
    END LOOP;
    -- Associate existing jenkins_* resources with the matching integration.
    UPDATE resources res SET integration_id = ig.id
    FROM integrations ig
    WHERE ig.connector_id = res.connector_id AND ig.protocol = 'jenkins'
      AND res.integration_id IS NULL AND res.resource_type LIKE 'jenkins_%';
END $$;
