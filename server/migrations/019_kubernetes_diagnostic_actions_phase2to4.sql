-- K8s diagnostic actions Phase 2-4: workload, network/config, cluster health
-- All read-only actions for comprehensive cluster troubleshooting.

-- Phase 2: Workload resources
INSERT INTO action_definitions (id, name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds) VALUES
    ('a0000000-0000-0000-0000-000000000013', 'statefulset.get', 1, '查询 StatefulSet', '获取 StatefulSet 详情：副本数、更新策略、容器', 'statefulset', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30),
    ('a0000000-0000-0000-0000-000000000014', 'daemonset.get', 1, '查询 DaemonSet', '获取 DaemonSet 详情：调度数量、就绪数量、更新策略', 'daemonset', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30),
    ('a0000000-0000-0000-0000-000000000015', 'replicaset.get', 1, '查询 ReplicaSet', '获取 ReplicaSet 详情：副本数、Owner、容器', 'replicaset', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30),
    ('a0000000-0000-0000-0000-000000000016', 'job.get', 1, '查询 Job', '获取 Job 详情：完成数、并行度、状态条件、时间', 'job', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30),
    ('a0000000-0000-0000-0000-000000000017', 'cronjob.get', 1, '查询 CronJob', '获取 CronJob 详情：调度规则、并发策略、上次调度时间', 'cronjob', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30);

-- Phase 3: Network & config
INSERT INTO action_definitions (id, name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds) VALUES
    ('a0000000-0000-0000-0000-000000000018', 'service.get', 1, '查询 Service', '获取 Service 详情：类型、ClusterIP、端口、选择器', 'service', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30),
    ('a0000000-0000-0000-0000-000000000019', 'endpoints.get', 1, '查询 Endpoints', '获取 Endpoints 详情：就绪/未就绪地址、端口', 'endpoints', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30),
    ('a0000000-0000-0000-0000-000000000020', 'ingress.get', 1, '查询 Ingress', '获取 Ingress 详情：路由规则、TLS、负载均衡状态', 'ingress', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30),
    ('a0000000-0000-0000-0000-000000000021', 'configmap.get', 1, '查询 ConfigMap', '获取 ConfigMap 详情：数据键值', 'configmap', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30),
    ('a0000000-0000-0000-0000-000000000022', 'secret.list', 1, '查询 Secret 列表', '列出命名空间内所有 Secret 的元数据（名称、类型，不含值）', 'secret', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 30);

-- Phase 4: Cluster health (cluster-scoped, namespace parameter unused)
INSERT INTO action_definitions (id, name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds) VALUES
    ('a0000000-0000-0000-0000-000000000023', 'node.get', 1, '查询 Node', '获取节点详情：条件、容量、地址、污点、Kubelet 版本', 'node', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name"],"additionalProperties":false}', 30),
    ('a0000000-0000-0000-0000-000000000024', 'namespace.list', 1, '查询命名空间列表', '列出集群内所有命名空间及状态', 'namespace', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name"],"additionalProperties":false}', 30);
