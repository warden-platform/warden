-- Resource names are not globally unique inside a Connector. Cloud and
-- middleware integrations can expose the same type/name pair in different
-- projects, regions, instances, or integrations (for example both production
-- and testing SLS Projects commonly contain Logstores named backend/frontend).
-- external_id carries that provider scope and is the stable execution identity.
BEGIN;

ALTER TABLE resources
  DROP CONSTRAINT IF EXISTS resources_connector_id_resource_type_namespace_name_key;

-- Migration 020 allowed duplicate external identities when a resource was
-- renamed. Merge those rare rows before installing the corrected key. Prefer
-- an ACTIVE/recent row and preserve both live configuration and audit history.
CREATE TEMP TABLE resource_identity_merge ON COMMIT DROP AS
SELECT id AS duplicate_id, keeper_id
FROM (
  SELECT id,
         first_value(id) OVER identity_window AS keeper_id,
         row_number() OVER identity_window AS row_number
  FROM resources
  WINDOW identity_window AS (
    PARTITION BY connector_id, resource_type, external_id
    ORDER BY (status = 'ACTIVE') DESC, updated_at DESC, id
  )
) ranked
WHERE row_number > 1;

INSERT INTO resource_actions (resource_id, action_id, enabled, created_at)
SELECT merge.keeper_id, item.action_id, item.enabled, item.created_at
FROM resource_actions item
JOIN resource_identity_merge merge ON merge.duplicate_id = item.resource_id
ON CONFLICT (resource_id, action_id)
DO UPDATE SET enabled = resource_actions.enabled OR EXCLUDED.enabled;
DELETE FROM resource_actions item
USING resource_identity_merge merge
WHERE item.resource_id = merge.duplicate_id;

INSERT INTO resource_project_overrides (resource_id, project_id, created_by, reason, created_at, updated_at)
SELECT merge.keeper_id, item.project_id, item.created_by, item.reason, item.created_at, item.updated_at
FROM resource_project_overrides item
JOIN resource_identity_merge merge ON merge.duplicate_id = item.resource_id
ON CONFLICT (resource_id) DO NOTHING;
DELETE FROM resource_project_overrides item
USING resource_identity_merge merge
WHERE item.resource_id = merge.duplicate_id;

INSERT INTO resource_environment_overrides (resource_id, environment, created_by, reason, created_at, updated_at)
SELECT merge.keeper_id, item.environment, item.created_by, item.reason, item.created_at, item.updated_at
FROM resource_environment_overrides item
JOIN resource_identity_merge merge ON merge.duplicate_id = item.resource_id
ON CONFLICT (resource_id) DO NOTHING;
DELETE FROM resource_environment_overrides item
USING resource_identity_merge merge
WHERE item.resource_id = merge.duplicate_id;

INSERT INTO resource_project_consumers (resource_id, project_id, created_at)
SELECT merge.keeper_id, item.project_id, item.created_at
FROM resource_project_consumers item
JOIN resource_identity_merge merge ON merge.duplicate_id = item.resource_id
ON CONFLICT (resource_id, project_id) DO NOTHING;
DELETE FROM resource_project_consumers item
USING resource_identity_merge merge
WHERE item.resource_id = merge.duplicate_id;

UPDATE policy_decisions item SET resource_id = merge.keeper_id
FROM resource_identity_merge merge WHERE item.resource_id = merge.duplicate_id;
UPDATE access_requests item SET resource_id = merge.keeper_id
FROM resource_identity_merge merge WHERE item.resource_id = merge.duplicate_id;
UPDATE executions item SET resource_id = merge.keeper_id
FROM resource_identity_merge merge WHERE item.resource_id = merge.duplicate_id;

DELETE FROM resources item
USING resource_identity_merge merge
WHERE item.id = merge.duplicate_id;

-- PostgreSQL does not support ADD CONSTRAINT IF NOT EXISTS. Release jobs may
-- replay migrations, so guard the corrected identity constraint explicitly.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conrelid = 'resources'::regclass
      AND conname = 'resources_connector_id_resource_type_external_id_key'
  ) THEN
    ALTER TABLE resources
      ADD CONSTRAINT resources_connector_id_resource_type_external_id_key
      UNIQUE (connector_id, resource_type, external_id);
  END IF;
END
$$;

COMMIT;
