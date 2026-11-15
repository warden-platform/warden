-- Make control-plane managed resources and executions first-class. A
-- Connector represents a real target-side runtime only; Warden-owned credential
-- actions continue through policy/approval/audit but execute inline.

ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS management_type VARCHAR(32) NOT NULL DEFAULT 'connector';
ALTER TABLE action_definitions
    ADD COLUMN IF NOT EXISTS executor_type VARCHAR(32) NOT NULL DEFAULT 'connector';
ALTER TABLE executions
    ADD COLUMN IF NOT EXISTS executor_type VARCHAR(32) NOT NULL DEFAULT 'connector';

ALTER TABLE resources ALTER COLUMN connector_id DROP NOT NULL;

UPDATE action_definitions
SET executor_type='control_plane'
WHERE name LIKE 'credential.%';

UPDATE executions
SET executor_type='control_plane', connector_id=NULL
WHERE control_plane_execution=TRUE;

UPDATE executions execution
SET executor_type='control_plane', connector_id=NULL
FROM action_definitions action
WHERE execution.action_id=action.id AND action.executor_type='control_plane';

UPDATE executions execution
SET connector_id=resource.connector_id
FROM resources resource
WHERE execution.resource_id=resource.id
  AND execution.executor_type='connector'
  AND execution.connector_id IS NULL
  AND resource.connector_id IS NOT NULL;

UPDATE audit_events
SET connector_id=NULL
WHERE connector_id IN (
    SELECT id FROM connectors WHERE name='pam-control-plane' AND connector_type='control_plane'
);

UPDATE resources
SET management_type='control_plane', connector_id=NULL
WHERE metadata->>'managedBy'='control_plane'
   OR connector_id IN (
       SELECT id FROM connectors WHERE name='pam-control-plane' AND connector_type='control_plane'
   );

CREATE UNIQUE INDEX IF NOT EXISTS idx_resources_control_plane_identity
    ON resources(tenant_id, resource_type, external_id)
    WHERE management_type='control_plane';

INSERT INTO resources
    (resource_type, external_id, name, namespace, connector_id, management_type,
     environment, status, metadata, tenant_id)
VALUES
    ('credential_vault', 'pam/control-plane', 'PAM 凭据库', '', NULL,
     'control_plane', 'testing', 'ACTIVE', '{"managedBy":"control_plane"}'::jsonb, 'platform')
ON CONFLICT (tenant_id, resource_type, external_id)
    WHERE management_type='control_plane'
DO UPDATE SET name=EXCLUDED.name, status='ACTIVE', updated_at=now();

DELETE FROM connectors
WHERE name='pam-control-plane' AND connector_type='control_plane';

ALTER TABLE resources DROP CONSTRAINT IF EXISTS resources_management_type_check;
ALTER TABLE resources ADD CONSTRAINT resources_management_type_check CHECK (
    (management_type='connector' AND connector_id IS NOT NULL) OR
    (management_type='control_plane' AND connector_id IS NULL)
);

ALTER TABLE action_definitions DROP CONSTRAINT IF EXISTS action_definitions_executor_type_check;
ALTER TABLE action_definitions ADD CONSTRAINT action_definitions_executor_type_check
    CHECK (executor_type IN ('connector','control_plane'));

ALTER TABLE executions DROP CONSTRAINT IF EXISTS executions_executor_type_check;
ALTER TABLE executions ADD CONSTRAINT executions_executor_type_check CHECK (
    (executor_type='connector' AND connector_id IS NOT NULL) OR
    (executor_type='control_plane' AND connector_id IS NULL)
);

ALTER TABLE executions DROP COLUMN IF EXISTS control_plane_execution;
