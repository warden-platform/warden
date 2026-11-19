-- Configurable provider capability catalog (OPE-7516 Phase 1).
--
-- Replaces the hard-coded BuiltinProviderCapabilities slice with a
-- database-backed registry. Administrators can register/update/disable read
-- capabilities at runtime without a code change; every entry still passes
-- ValidateProviderCapability before it becomes active, so the executor is
-- never widened into an arbitrary URL proxy or generic write.

-- 1) provider_capabilities table. Columns mirror domain.ResourceCapability;
--    array/map fields are stored as JSONB and scanned back directly by pgx.
--    发布流水线每次发布都会重跑 migrations.txt 中的全部迁移，故必须幂等。
CREATE TABLE IF NOT EXISTS provider_capabilities (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               VARCHAR(255) NOT NULL DEFAULT 'default',
    name                    VARCHAR(253) NOT NULL,
    version                 INTEGER      NOT NULL DEFAULT 1,
    resource_kinds          JSONB        NOT NULL DEFAULT '[]',
    protocols               JSONB        NOT NULL DEFAULT '[]',
    methods                 JSONB        NOT NULL DEFAULT '[]',
    path_patterns           JSONB        NOT NULL DEFAULT '[]',
    query_parameters        JSONB        NOT NULL DEFAULT '{}',
    allowed_headers         JSONB        NOT NULL DEFAULT '[]',
    max_items               INTEGER      NOT NULL DEFAULT 0,
    max_response_bytes      INTEGER      NOT NULL DEFAULT 0,
    timeout_seconds         INTEGER      NOT NULL DEFAULT 0,
    max_time_range_seconds  BIGINT       NOT NULL DEFAULT 0,
    status                  VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE', -- ACTIVE/DISABLED
    description             TEXT         NOT NULL DEFAULT '',
    created_by              UUID REFERENCES principals(id),
    created_at              TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, name, version)
);

CREATE INDEX IF NOT EXISTS idx_provider_capabilities_status ON provider_capabilities(status);

-- 1b) audit_action 新值（pattern 同 migration 055）
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'provider_capability.created';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'provider_capability.updated';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'provider_capability.deleted';

-- 2) Seed from the v1 built-in catalog (domain.BuiltinProviderCapabilities).
INSERT INTO provider_capabilities
    (tenant_id, name, version, resource_kinds, protocols, methods, path_patterns,
     query_parameters, allowed_headers, max_items, max_response_bytes, timeout_seconds, description)
VALUES
    ('default', 'grafana.dashboard.read', 1,
     '["grafana_dashboard"]'::jsonb, '["grafana"]'::jsonb, '["GET"]'::jsonb,
     '["/api/dashboards/uid/{uid}"]'::jsonb, '{}'::jsonb, '[]'::jsonb, 1, 1048576, 10,
     '读取 Grafana 仪表板（按 UID）'),
    ('default', 'jenkins.job.read', 1,
     '["jenkins_job"]'::jsonb, '["jenkins"]'::jsonb, '["GET"]'::jsonb,
     '["/job/{job}/api/json"]'::jsonb,
     '{"tree":{"maxValues":1,"maxLength":512},"depth":{"maxValues":1,"maxLength":2,"pattern":"^[0-2]$"}}'::jsonb,
     '[]'::jsonb, 100, 1048576, 10,
     '读取 Jenkins 作业状态'),
    ('default', 'kubernetes.resource.read', 1,
     '["deployment","statefulset","daemonset","replicaset","job","cronjob","service","endpoints","ingress","configmap","pod","node","namespace","kubernetes_resource"]'::jsonb,
     '["kubernetes"]'::jsonb, '["GET"]'::jsonb,
     '["/api/{version}/namespaces/{namespace}/{plural}/{name}","/apis/{group}/{version}/namespaces/{namespace}/{plural}/{name}"]'::jsonb,
     '{"labelSelector":{"maxValues":1,"maxLength":512},"fieldSelector":{"maxValues":1,"maxLength":512}}'::jsonb,
     '[]'::jsonb, 500, 4194304, 20,
     '读取 Kubernetes 资源（namespaced 与 cluster-scoped）'),
    ('default', 'huawei.ecs.read', 1,
     '["huawei_ecs_instance"]'::jsonb, '["huaweicloud"]'::jsonb, '["GET"]'::jsonb,
     '["/servers/{server_id}"]'::jsonb, '{}'::jsonb, '[]'::jsonb, 1, 1048576, 20,
     '读取华为云 ECS 实例详情'),
    ('default', 'huawei.obs.object.metadata.read', 1,
     '["huawei_obs_bucket"]'::jsonb, '["huaweicloud"]'::jsonb, '["HEAD"]'::jsonb,
     '["/{bucket}/**"]'::jsonb, '{}'::jsonb, '[]'::jsonb, 1, 65536, 15,
     '读取华为云 OBS 对象元数据（HEAD）')
ON CONFLICT (tenant_id, name, version) DO NOTHING;
