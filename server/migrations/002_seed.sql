-- 种子数据：Warden 演示数据

-- 1. 种子主体
INSERT INTO principals (id, kind, name, status, metadata) VALUES
    ('00000000-0000-0000-0000-000000000001', 'agent_type', 'deploy-agent', 'ACTIVE', '{"description": "标准部署 Agent"}'),
    ('00000000-0000-0000-0000-000000000002', 'agent_instance', 'demo-agent-001', 'ACTIVE', '{"agent_type": "deploy-agent"}'),
    ('00000000-0000-0000-0000-000000000003', 'user', 'admin', 'ACTIVE', '{"email": "admin@warden.local"}');

-- 2. 种子动作定义 (Kubernetes)
INSERT INTO action_definitions (id, name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds) VALUES
    ('a0000000-0000-0000-0000-000000000001', 'deployment.get', 1, '查询 Deployment', '获取 Deployment 详情', 'deployment', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"]}', 30),
    ('a0000000-0000-0000-0000-000000000002', 'deployment.update_image', 1, '更新 Deployment 镜像', '更新指定容器的镜像版本', 'deployment', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"container":{"type":"string"},"image":{"type":"string"}},"required":["name","namespace","container","image"]}', 120),
    ('a0000000-0000-0000-0000-000000000003', 'deployment.rollout_status', 1, '查询发布状态', '查询 Deployment 发布进度', 'deployment', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"]}', 30),
    ('a0000000-0000-0000-0000-000000000004', 'pod.logs.read', 1, '读取 Pod 日志', '读取指定 Pod 的日志', 'pod', 'low', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"container":{"type":"string"},"tailLines":{"type":"integer","default":100,"maximum":1000}},"required":["name","namespace"]}', 60);

-- 3. 种子策略
INSERT INTO policies (id, name, description, status) VALUES
    ('f0000000-0000-0000-0000-000000000001', 'kubernetes-default', 'Kubernetes 默认策略', 'PUBLISHED');

INSERT INTO policy_versions (id, policy_id, version, rego_content, status, published_at) VALUES
    ('f1000000-0000-0000-0000-000000000001', 'f0000000-0000-0000-0000-000000000001', 1,
     'package warden.kubernetes

default allow = false

# 测试环境自动允许只读和更新镜像
allow {
    input.environment == "testing"
    input.action in ["deployment.get", "deployment.update_image", "deployment.rollout_status", "pod.logs.read"]
}

# 生产环境只允许只读操作自动执行
allow {
    input.environment == "production"
    input.action in ["deployment.get", "deployment.rollout_status", "pod.logs.read"]
}
',
     'PUBLISHED', now());

INSERT INTO policy_bindings (id, policy_id, resource_type, environment, priority) VALUES
    ('f2000000-0000-0000-0000-000000000001', 'f0000000-0000-0000-0000-000000000001', 'deployment', 'testing', 10),
    ('f2000000-0000-0000-0000-000000000002', 'f0000000-0000-0000-0000-000000000001', 'deployment', 'production', 10),
    ('f2000000-0000-0000-0000-000000000003', 'f0000000-0000-0000-0000-000000000001', 'pod', 'testing', 10),
    ('f2000000-0000-0000-0000-000000000004', 'f0000000-0000-0000-0000-000000000001', 'pod', 'production', 10);
