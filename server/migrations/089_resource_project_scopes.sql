-- Tenant-owned resources can be exposed to zero, one, many, or all projects.
-- INHERITED preserves the existing connector/integration/routing behavior.

BEGIN;

ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS project_scope_mode VARCHAR(32) NOT NULL DEFAULT 'INHERITED';

DO $do$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'resources_project_scope_mode_check'
    ) THEN
        ALTER TABLE resources ADD CONSTRAINT resources_project_scope_mode_check
            CHECK (project_scope_mode IN ('INHERITED', 'SELECTED_PROJECTS', 'ALL_PROJECTS', 'UNASSIGNED'));
    END IF;
END $do$;

-- Composite keys let project-scope relationships enforce tenant equality at
-- the database boundary instead of relying only on handler validation.
CREATE UNIQUE INDEX IF NOT EXISTS uq_resources_id_tenant ON resources(id, tenant_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_projects_id_tenant ON projects(id, tenant_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_principals_id_tenant ON principals(id, tenant_id);

CREATE TABLE IF NOT EXISTS resource_project_bindings (
    tenant_id      VARCHAR(255) NOT NULL,
    resource_id    UUID NOT NULL,
    project_id     UUID NOT NULL,
    permission_set VARCHAR(32) NOT NULL DEFAULT 'USE',
    source         VARCHAR(32) NOT NULL DEFAULT 'MANUAL',
    created_by     UUID,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (resource_id, project_id),
    CONSTRAINT resource_project_bindings_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES tenants(slug) ON DELETE CASCADE,
    CONSTRAINT resource_project_bindings_resource_tenant_fk
        FOREIGN KEY (resource_id, tenant_id) REFERENCES resources(id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT resource_project_bindings_project_tenant_fk
        FOREIGN KEY (project_id, tenant_id) REFERENCES projects(id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT resource_project_bindings_created_by_tenant_fk
        FOREIGN KEY (created_by, tenant_id) REFERENCES principals(id, tenant_id),
    CONSTRAINT resource_project_bindings_permission_check
        CHECK (permission_set IN ('USE')),
    CONSTRAINT resource_project_bindings_source_check
        CHECK (source IN ('MANUAL', 'LEGACY_CONSUMER'))
);

CREATE INDEX IF NOT EXISTS idx_resource_project_bindings_tenant_project
    ON resource_project_bindings(tenant_id, project_id, resource_id);

-- Preserve the Jenkins-specific consumer configuration as general project
-- bindings. Resources remain INHERITED until an administrator explicitly
-- chooses a scope, so their existing primary-project routing is unchanged.
INSERT INTO resource_project_bindings (tenant_id, resource_id, project_id, source)
SELECT res.tenant_id, rpc.resource_id, rpc.project_id, 'LEGACY_CONSUMER'
FROM resource_project_consumers rpc
JOIN resources res ON res.id=rpc.resource_id
JOIN projects project ON project.id=rpc.project_id AND project.tenant_id=res.tenant_id
ON CONFLICT (resource_id, project_id) DO NOTHING;

ALTER TABLE policy_decisions ADD COLUMN IF NOT EXISTS acting_project_id UUID;
ALTER TABLE access_requests ADD COLUMN IF NOT EXISTS acting_project_id UUID;
ALTER TABLE access_grants ADD COLUMN IF NOT EXISTS acting_project_id UUID;
ALTER TABLE executions ADD COLUMN IF NOT EXISTS acting_project_id UUID;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS acting_project_id UUID;

DO $do$
DECLARE table_name text;
DECLARE constraint_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY['policy_decisions','access_requests','access_grants','executions','audit_events'] LOOP
        constraint_name := table_name || '_acting_project_tenant_fk';
        IF NOT EXISTS (
            SELECT 1 FROM pg_constraint
            WHERE conname=constraint_name AND conrelid=table_name::regclass
        ) THEN
            EXECUTE format(
                'ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY (acting_project_id, tenant_id) REFERENCES projects(id, tenant_id)',
                table_name, constraint_name
            );
        END IF;
        EXECUTE format(
            'CREATE INDEX IF NOT EXISTS %I ON %I(tenant_id, acting_project_id)',
            'idx_' || table_name || '_acting_project', table_name
        );
    END LOOP;
END $do$;

COMMIT;
