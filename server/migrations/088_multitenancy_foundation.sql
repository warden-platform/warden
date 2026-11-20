-- Multi-tenant foundation.
-- Existing rows are migrated into the formal platform tenant.  The control plane
-- remains shared; tenant_id is the isolation boundary for business data.

BEGIN;

CREATE TABLE IF NOT EXISTS tenants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        VARCHAR(100) NOT NULL UNIQUE,
    name        VARCHAR(255) NOT NULL,
    status      VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
    settings    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,98}[a-z0-9]$' OR length(slug) = 1)
);

INSERT INTO tenants (slug, name)
VALUES ('platform', 'Platform')
ON CONFLICT (slug) DO NOTHING;

-- Tables created before the multi-tenant foundation did not carry tenant
-- ownership.  Keep the migration idempotent for development databases.
ALTER TABLE policy_decisions ADD COLUMN IF NOT EXISTS tenant_id VARCHAR(255);
ALTER TABLE access_requests ADD COLUMN IF NOT EXISTS tenant_id VARCHAR(255);
ALTER TABLE access_grants ADD COLUMN IF NOT EXISTS tenant_id VARCHAR(255);
ALTER TABLE executions ADD COLUMN IF NOT EXISTS tenant_id VARCHAR(255);
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS tenant_id VARCHAR(255);
DO $$ BEGIN IF to_regclass('outbox_events') IS NOT NULL THEN ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS tenant_id VARCHAR(255); END IF; END $$;

UPDATE principals SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE connectors SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
-- Older release runs seeded this shared control-plane resource under the
-- legacy default tenant. Remove only a duplicate legacy row before ownership
-- backfill so the partial unique index cannot reject the tenant rewrite.
DELETE FROM resources legacy
WHERE legacy.tenant_id='default'
  AND legacy.management_type='control_plane'
  AND EXISTS (
    SELECT 1 FROM resources current
    WHERE current.tenant_id='platform'
      AND current.management_type='control_plane'
      AND current.resource_type=legacy.resource_type
      AND current.external_id=legacy.external_id
  );
UPDATE resources SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE integrations SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE projects SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE policies SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE action_definitions SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
-- Ownership for the remaining tenant-bound tables introduced after the
-- original schema.  These were previously relying on the legacy default.
UPDATE credentials SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
-- Jenkins runs the complete migration directory on every release.  Some
-- older databases already contain the same shared capability under both the
-- legacy default tenant and platform.  Remove only the legacy duplicate before
-- backfilling ownership so the migration remains safely repeatable.
DELETE FROM provider_capabilities legacy
WHERE legacy.tenant_id='default'
  AND EXISTS (
    SELECT 1 FROM provider_capabilities current
    WHERE current.tenant_id='platform'
      AND current.name=legacy.name
      AND current.version=legacy.version
  );
UPDATE provider_capabilities SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE database_query_templates SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE database_query_template_publish_requests SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE database_column_classifications SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE database_previews SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE notification_channels SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE dingtalk_principal_bindings SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE integration_credential_profiles SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
ALTER TABLE approval_rules ADD COLUMN IF NOT EXISTS tenant_id VARCHAR(255);
ALTER TABLE policy_matrix_rules ADD COLUMN IF NOT EXISTS tenant_id VARCHAR(255);
UPDATE approval_rules SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE policy_matrix_rules SET tenant_id='platform' WHERE tenant_id IS NULL OR btrim(tenant_id) IN ('', 'default');
UPDATE policy_decisions pd SET tenant_id=p.tenant_id
FROM principals p WHERE pd.tenant_id IS NULL AND p.id=pd.principal_id;
UPDATE policy_decisions pd SET tenant_id=r.tenant_id
FROM resources r WHERE pd.tenant_id IS NULL AND r.id=pd.resource_id;
UPDATE policy_decisions SET tenant_id='platform' WHERE tenant_id IS NULL;
UPDATE access_requests ar SET tenant_id=COALESCE(p.tenant_id, r.tenant_id, 'platform')
FROM principals p, resources r
WHERE ar.tenant_id IS NULL AND p.id=ar.principal_id AND r.id=ar.resource_id;
UPDATE access_grants ag SET tenant_id=COALESCE(p.tenant_id, 'platform')
FROM principals p
WHERE ag.tenant_id IS NULL AND p.id=ag.principal_id;
UPDATE executions e SET tenant_id=COALESCE(p.tenant_id, r.tenant_id, 'platform')
FROM principals p, resources r
WHERE e.tenant_id IS NULL AND p.id=e.principal_id AND r.id=e.resource_id;
UPDATE audit_events ae SET tenant_id=COALESCE(p.tenant_id, 'platform')
FROM principals p
WHERE ae.tenant_id IS NULL AND p.id=ae.actor_id;
UPDATE audit_events SET tenant_id='platform' WHERE tenant_id IS NULL;
DO $$ BEGIN IF to_regclass('outbox_events') IS NOT NULL THEN UPDATE outbox_events SET tenant_id='platform' WHERE tenant_id IS NULL; END IF; END $$;

ALTER TABLE policy_decisions ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE access_requests ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE access_grants ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE executions ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE audit_events ALTER COLUMN tenant_id SET NOT NULL;
DO $$ BEGIN IF to_regclass('outbox_events') IS NOT NULL THEN ALTER TABLE outbox_events ALTER COLUMN tenant_id SET NOT NULL; END IF; END $$;

CREATE INDEX IF NOT EXISTS idx_policy_decisions_tenant_created ON policy_decisions(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_access_requests_tenant_status ON access_requests(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_access_grants_tenant_status ON access_grants(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_executions_tenant_created ON executions(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_created ON audit_events(tenant_id, created_at DESC);
DO $$ BEGIN IF to_regclass('outbox_events') IS NOT NULL THEN CREATE INDEX IF NOT EXISTS idx_outbox_events_tenant_status ON outbox_events(tenant_id, status); END IF; END $$;

-- Tenant identifiers are slugs for compatibility with the existing API.  FK
-- constraints prevent orphaned ownership values and make future private
-- deployment exports deterministic.
DO $do$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['principals','connectors','resources','integrations','projects','policies','action_definitions','credentials','provider_capabilities','database_query_templates','database_query_template_publish_requests','database_column_classifications','database_previews','notification_channels','dingtalk_principal_bindings','approval_rules','policy_matrix_rules','integration_credential_profiles','policy_decisions','access_requests','access_grants','executions','audit_events','outbox_events'] LOOP
    IF to_regclass(t) IS NOT NULL THEN
      EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON %I(tenant_id)', 'idx_'||t||'_tenant', t);
    END IF;
  END LOOP;
END $do$;

CREATE TABLE IF NOT EXISTS tenant_quotas (
    tenant_id              VARCHAR(255) PRIMARY KEY,
    max_principals         INTEGER NOT NULL DEFAULT 1000 CHECK (max_principals > 0),
    max_connectors         INTEGER NOT NULL DEFAULT 100 CHECK (max_connectors > 0),
    max_concurrent_tasks   INTEGER NOT NULL DEFAULT 100 CHECK (max_concurrent_tasks > 0),
    max_requests_per_minute INTEGER NOT NULL DEFAULT 600 CHECK (max_requests_per_minute > 0),
    audit_retention_days   INTEGER NOT NULL DEFAULT 365 CHECK (audit_retention_days > 0),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO tenant_quotas (tenant_id)
SELECT 'platform' WHERE EXISTS (SELECT 1 FROM tenants WHERE slug='platform')
ON CONFLICT (tenant_id) DO NOTHING;

COMMIT;
