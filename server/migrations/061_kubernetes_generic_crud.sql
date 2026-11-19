-- Governed generic Kubernetes CRUD. The synthetic kubernetes_resource type is
-- expanded by the control plane onto compatible discovered resources.

INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('kubernetes.resource.get', 1, '读取 Kubernetes 资源', '按固定 GVK、Namespace 和 Name 读取受支持资源；敏感字段会被过滤', 'kubernetes_resource', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"apiVersion":{"type":"string","minLength":1,"maxLength":128},"kind":{"type":"string","minLength":1,"maxLength":128},"name":{"type":"string","minLength":1,"maxLength":253},"namespace":{"type":"string","minLength":1,"maxLength":63}},"required":["apiVersion","kind","name","namespace"],"additionalProperties":false}', 60),
    ('kubernetes.resource.list', 1, '列出 Kubernetes 资源', '在固定 Namespace 中按允许的 GVK 列出资源，最多 200 条', 'kubernetes_resource', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"apiVersion":{"type":"string","minLength":1,"maxLength":128},"kind":{"type":"string","minLength":1,"maxLength":128},"namespace":{"type":"string","minLength":1,"maxLength":63},"labelSelector":{"type":"string","maxLength":1024},"fieldSelector":{"type":"string","maxLength":1024},"limit":{"type":"integer","minimum":1,"maximum":200,"default":100}},"required":["apiVersion","kind","namespace"],"additionalProperties":false}', 60),
    ('kubernetes.resource.preview', 1, '预检 Kubernetes 变更', '执行 server-side dry-run、准入校验并返回净化后的 before/after 与 previewHash', 'kubernetes_resource', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"operation":{"type":"string","enum":["create","update"]},"apiVersion":{"type":"string","minLength":1,"maxLength":128},"kind":{"type":"string","minLength":1,"maxLength":128},"name":{"type":"string","minLength":1,"maxLength":253},"namespace":{"type":"string","minLength":1,"maxLength":63},"expectedResourceVersion":{"type":"string","minLength":1,"maxLength":128},"manifest":{"type":"object"}},"required":["operation","apiVersion","kind","namespace","manifest"],"additionalProperties":false}', 60),
    ('kubernetes.resource.create', 1, '创建 Kubernetes 资源', '在固定 Namespace 内创建允许 GVK；执行前重跑 dry-run 并校验 previewHash', 'kubernetes_resource', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"apiVersion":{"type":"string","minLength":1,"maxLength":128},"kind":{"type":"string","minLength":1,"maxLength":128},"namespace":{"type":"string","minLength":1,"maxLength":63},"manifest":{"type":"object"},"previewHash":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["apiVersion","kind","namespace","manifest","previewHash"],"additionalProperties":false}', 120),
    ('kubernetes.resource.update', 1, '更新 Kubernetes 资源', '更新固定资源；resourceVersion 并发保护，执行前重跑 dry-run 并校验 previewHash', 'kubernetes_resource', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"apiVersion":{"type":"string","minLength":1,"maxLength":128},"kind":{"type":"string","minLength":1,"maxLength":128},"name":{"type":"string","minLength":1,"maxLength":253},"namespace":{"type":"string","minLength":1,"maxLength":63},"expectedResourceVersion":{"type":"string","minLength":1,"maxLength":128},"manifest":{"type":"object"},"previewHash":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["apiVersion","kind","name","namespace","expectedResourceVersion","manifest","previewHash"],"additionalProperties":false}', 120),
    ('kubernetes.resource.delete', 1, '删除 Kubernetes 资源', '删除固定资源；要求 resourceVersion 和 Kind/Namespace/Name 二次确认', 'kubernetes_resource', 'critical', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"apiVersion":{"type":"string","minLength":1,"maxLength":128},"kind":{"type":"string","minLength":1,"maxLength":128},"name":{"type":"string","minLength":1,"maxLength":253},"namespace":{"type":"string","minLength":1,"maxLength":63},"expectedResourceVersion":{"type":"string","minLength":1,"maxLength":128},"confirmResource":{"type":"string","minLength":1,"maxLength":512},"propagationPolicy":{"type":"string","enum":["Background","Foreground","Orphan"],"default":"Background"}},"required":["apiVersion","kind","name","namespace","expectedResourceVersion","confirmResource"],"additionalProperties":false}', 120)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
