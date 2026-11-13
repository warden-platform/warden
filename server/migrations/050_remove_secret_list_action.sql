-- Kubernetes List Secret returns Secret objects including data. The runtime,
-- RBAC and policy no longer support this action; remove the stale catalog row
-- when no historical execution still references it.
DELETE FROM action_definitions a
WHERE a.name = 'secret.list'
  AND NOT EXISTS (SELECT 1 FROM executions e WHERE e.action_id = a.id);
