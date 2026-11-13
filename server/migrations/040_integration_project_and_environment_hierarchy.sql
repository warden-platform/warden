-- Middleware ownership is independent from the execution channel. Existing
-- integrations without an explicit project snapshot their current effective
-- project (the connector project, or the tenant default when the connector is
-- unassigned). This preserves visible ownership while removing future coupling.
UPDATE integrations ig
SET project_id = COALESCE(c.project_id, p.id),
    updated_at = now()
FROM connectors c, projects p
WHERE ig.project_id IS NULL
  AND c.id = ig.connector_id
  AND p.tenant_id = ig.tenant_id
  AND p.is_default;

-- Effective environment reads locate the current database instance/Schema for
-- each child resource. Keep the hierarchy join indexed as catalogs grow.
CREATE INDEX IF NOT EXISTS idx_resources_integration_type_name_current
    ON resources(integration_id, resource_type, name)
    WHERE integration_id IS NOT NULL AND status <> 'ORPHANED';
