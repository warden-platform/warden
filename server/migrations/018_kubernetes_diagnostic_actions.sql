-- K8s diagnostic actions (Phase 1: Pod Diagnostics)
-- Adds read-only actions for cluster troubleshooting:
--   pod.get     - full pod status (container states, restart counts, conditions, QoS)
--   pod.events  - K8s events for a specific pod (image pull errors, OOM kills, etc.)
--   event.list  - all events in a namespace (kubectl get events equivalent)

INSERT INTO action_definitions (id, name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds) VALUES
    ('a0000000-0000-0000-0000-000000000010', 'pod.get', 1, '查询 Pod 状态', '获取 Pod 完整状态：容器状态、重启次数、条件、QoS、节点', 'pod', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30),
    ('a0000000-0000-0000-0000-000000000011', 'pod.events', 1, '查询 Pod 事件', '获取与指定 Pod 相关的 Kubernetes 事件（镜像拉取错误、OOM、调度失败等）', 'pod', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30),
    ('a0000000-0000-0000-0000-000000000012', 'event.list', 1, '查询命名空间事件', '列出指定命名空间内的所有 Kubernetes 事件', 'event', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30);
