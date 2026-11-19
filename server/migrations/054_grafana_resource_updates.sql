-- Grafana resource update UX. Datasource detail reads expose only non-secret
-- configuration; mutation targets remain fixed to the selected resource UID.
INSERT INTO action_definitions
  (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
  ('grafana.folder.update', 1, '更新 Grafana Folder', '重命名已授权的 Grafana Folder，UID 固定为当前资源', 'grafana_folder', 'high', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"title":{"type":"string","minLength":1,"maxLength":255},"version":{"type":"integer","minimum":0}},"required":["name","title","version"],"additionalProperties":false}', 60),
  ('grafana.datasource.get', 1, '查看 Grafana Datasource 配置', '读取 Datasource 非敏感配置，不返回密码、Token 或 secureJsonData', 'grafana_datasource', 'low', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name"],"additionalProperties":false}', 30)
ON CONFLICT (name, version) DO UPDATE SET
  display_name=EXCLUDED.display_name,
  description=EXCLUDED.description,
  resource_type=EXCLUDED.resource_type,
  risk_level=EXCLUDED.risk_level,
  execution_mode=EXCLUDED.execution_mode,
  parameter_schema=EXCLUDED.parameter_schema,
  timeout_seconds=EXCLUDED.timeout_seconds;
