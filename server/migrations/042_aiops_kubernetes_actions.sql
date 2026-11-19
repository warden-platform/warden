-- Structured AIOps additions: metrics-server reads and Grafana-dashboard-only
-- ConfigMap lifecycle. No generic Kubernetes proxy or arbitrary ConfigMap
-- mutation is exposed.
INSERT INTO action_definitions (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds) VALUES
  ('pod.metrics', 1, '查看 Pod 实时指标', '通过 metrics.k8s.io 读取单个 Pod 的 CPU/内存用量', 'pod', 'low', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30),
  ('node.metrics', 1, '查看 Node 实时指标', '通过 metrics.k8s.io 读取单个 Node 的 CPU/内存用量', 'node', 'low', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name"],"additionalProperties":false}', 30),
  ('namespace.dashboard_configmap.apply', 1, '发布 Grafana Dashboard ConfigMap', '仅创建或更新 grafana-dashboard-* ConfigMap，内容固定为单个 Dashboard JSON', 'namespace', 'high', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"configMapName":{"type":"string","pattern":"^grafana-dashboard-[a-z0-9](?:[a-z0-9.-]{0,61}[a-z0-9])?$"},"dashboardKey":{"type":"string","pattern":"^[a-zA-Z0-9_.-]{1,200}\\.json$"},"dashboard":{"type":"object"}},"required":["name","configMapName","dashboardKey","dashboard"],"additionalProperties":false}', 60),
  ('configmap.dashboard.delete', 1, '删除 Grafana Dashboard ConfigMap', '仅删除名称为 grafana-dashboard-* 的 ConfigMap', 'configmap', 'high', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30)
ON CONFLICT (name, version) DO UPDATE SET display_name=EXCLUDED.display_name, description=EXCLUDED.description,
  resource_type=EXCLUDED.resource_type, risk_level=EXCLUDED.risk_level, execution_mode=EXCLUDED.execution_mode,
  parameter_schema=EXCLUDED.parameter_schema, timeout_seconds=EXCLUDED.timeout_seconds;
