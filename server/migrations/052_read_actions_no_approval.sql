-- Bounded read actions are audited but do not require interactive approval.
-- Mutation actions retain their existing medium/high/critical risk and policy.
UPDATE action_definitions
SET risk_level = 'low', updated_at = now()
WHERE name IN (
  'pod.logs.read',
  'docker.container.logs',
  'database.table.rows.query',
  'grafana.datasource.query'
);
