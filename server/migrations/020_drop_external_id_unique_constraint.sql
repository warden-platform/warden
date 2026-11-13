-- Drop the (external_id, connector_id) unique constraint.
-- Different resource types can share the same name in the same namespace
-- (e.g. Service "monitoring/prometheus-grafana" and Endpoints "monitoring/prometheus-grafana"),
-- so external_id alone is not unique per connector. The existing
-- (connector_id, resource_type, namespace, name) constraint is sufficient.
ALTER TABLE resources DROP CONSTRAINT IF EXISTS resources_external_id_connector_id_key;
