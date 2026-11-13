-- Grafana folder delete action
INSERT INTO action_definitions (id, name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds, tenant_id)
VALUES (
  gen_random_uuid(),
  'grafana.folder.delete',
  1,
  '删除 Grafana Folder',
  '删除指定文件夹，文件夹必须为空',
  'grafana_folder',
  'high',
  'SYNCHRONOUS',
  '{
    "type": "object",
    "properties": {
      "name": {"type": "string"},
      "namespace": {"type": "string"}
    },
    "required": ["name"],
    "additionalProperties": false
  }'::jsonb,
  60,
  'default'
)
ON CONFLICT (name, version) DO NOTHING;
