-- Prometheus/Loki datasource governance. Secret-bearing secureJsonData is
-- deliberately absent from the public action contract.
INSERT INTO action_definitions (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds) VALUES
  ('grafana.datasource.create', 1, '创建 Grafana Datasource', '创建不携带凭据的 Prometheus 或 Loki Datasource', 'grafana_instance', 'high', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"datasourceName":{"type":"string","minLength":1,"maxLength":190},"datasourceUid":{"type":"string","pattern":"^[a-zA-Z0-9_-]{1,40}$"},"type":{"type":"string","enum":["prometheus","loki"]},"url":{"type":"string","maxLength":2048},"access":{"type":"string","enum":["proxy","direct"]},"isDefault":{"type":"boolean"},"jsonData":{"type":"object","maxProperties":50}},"required":["name","datasourceName","type","url"],"additionalProperties":false}', 60),
  ('grafana.datasource.update', 1, '更新 Grafana Datasource', '更新已授权的 Prometheus 或 Loki Datasource，不接受 Secret 字段', 'grafana_datasource', 'high', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"datasourceName":{"type":"string","minLength":1,"maxLength":190},"type":{"type":"string","enum":["prometheus","loki"]},"url":{"type":"string","maxLength":2048},"access":{"type":"string","enum":["proxy","direct"]},"isDefault":{"type":"boolean"},"jsonData":{"type":"object","maxProperties":50}},"required":["name","datasourceName","type","url"],"additionalProperties":false}', 60),
  ('grafana.datasource.delete', 1, '删除 Grafana Datasource', '删除已授权的 Grafana Datasource', 'grafana_datasource', 'high', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name"],"additionalProperties":false}', 30)
ON CONFLICT (name, version) DO UPDATE SET display_name=EXCLUDED.display_name, description=EXCLUDED.description,
  resource_type=EXCLUDED.resource_type, risk_level=EXCLUDED.risk_level, execution_mode=EXCLUDED.execution_mode,
  parameter_schema=EXCLUDED.parameter_schema, timeout_seconds=EXCLUDED.timeout_seconds;
