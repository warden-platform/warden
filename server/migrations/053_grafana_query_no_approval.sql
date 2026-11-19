-- Grafana datasource queries are read-only observations. Keep them fully
-- audited while aligning their approval behavior with other query actions.
UPDATE action_definitions
SET risk_level = 'low', updated_at = now()
WHERE name = 'grafana.datasource.query';
