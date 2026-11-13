-- Purpose-built Kubernetes workload mutations. Every action binds name and
-- namespace to the selected PAM resource and requires an optimistic
-- expectedGeneration obtained from the corresponding read action. No raw YAML,
-- arbitrary patch, Secret, or generic ConfigMap mutation is exposed.

INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('deployment.update_image', 2, '更新 Deployment 镜像', '固定当前 Deployment、Namespace 和容器，校验 expectedGeneration 后更新镜像', 'deployment', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"expectedGeneration":{"type":"integer","minimum":1},"container":{"type":"string","minLength":1,"maxLength":253},"image":{"type":"string","minLength":1,"maxLength":2048}},"required":["name","namespace","expectedGeneration","container","image"],"additionalProperties":false}', 120),
    ('deployment.restart', 1, '重启 Deployment', '固定当前 Deployment 和 Namespace，校验 expectedGeneration 后触发滚动重启', 'deployment', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"expectedGeneration":{"type":"integer","minimum":1}},"required":["name","namespace","expectedGeneration"],"additionalProperties":false}', 120),
    ('deployment.scale', 1, '扩缩容 Deployment', '固定当前 Deployment 和 Namespace，校验 expectedGeneration 后设置期望副本数', 'deployment', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"expectedGeneration":{"type":"integer","minimum":1},"replicas":{"type":"integer","minimum":0,"maximum":1000}},"required":["name","namespace","expectedGeneration","replicas"],"additionalProperties":false}', 120),
    ('statefulset.restart', 1, '重启 StatefulSet', '固定当前 StatefulSet 和 Namespace，校验 expectedGeneration 后触发滚动重启', 'statefulset', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"expectedGeneration":{"type":"integer","minimum":1}},"required":["name","namespace","expectedGeneration"],"additionalProperties":false}', 180),
    ('statefulset.scale', 1, '扩缩容 StatefulSet', '固定当前 StatefulSet 和 Namespace，校验 expectedGeneration 后设置期望副本数', 'statefulset', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"expectedGeneration":{"type":"integer","minimum":1},"replicas":{"type":"integer","minimum":0,"maximum":1000}},"required":["name","namespace","expectedGeneration","replicas"],"additionalProperties":false}', 180),
    ('statefulset.update_image', 1, '更新 StatefulSet 镜像', '固定当前 StatefulSet、Namespace 和容器，校验 expectedGeneration 后更新镜像', 'statefulset', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"expectedGeneration":{"type":"integer","minimum":1},"container":{"type":"string","minLength":1,"maxLength":253},"image":{"type":"string","minLength":1,"maxLength":2048}},"required":["name","namespace","expectedGeneration","container","image"],"additionalProperties":false}', 180),
    ('cronjob.suspend', 1, '暂停 CronJob', '固定当前 CronJob 和 Namespace，校验 expectedGeneration 后暂停调度', 'cronjob', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"expectedGeneration":{"type":"integer","minimum":1}},"required":["name","namespace","expectedGeneration"],"additionalProperties":false}', 60),
    ('cronjob.resume', 1, '恢复 CronJob', '固定当前 CronJob 和 Namespace，校验 expectedGeneration 后恢复调度', 'cronjob', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"expectedGeneration":{"type":"integer","minimum":1}},"required":["name","namespace","expectedGeneration"],"additionalProperties":false}', 60),
    ('cronjob.trigger', 1, '手动触发 CronJob', '从当前 CronJob 模板创建确定性 Job；相同 Execution 重复派发不会重复创建', 'cronjob', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"expectedGeneration":{"type":"integer","minimum":1}},"required":["name","namespace","expectedGeneration"],"additionalProperties":false}', 60)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
