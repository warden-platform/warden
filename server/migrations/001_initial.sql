-- HighTower - 初始数据库迁移
-- 遵循架构文档中定义的领域模型和约束

-- 扩展：UUID 生成
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ============================================================
-- 1. 主体 (Principal)
-- ============================================================
CREATE TYPE principal_kind AS ENUM (
    'user',           -- 自然人用户
    'agent_type',     -- Agent 类型（如 deploy-agent）
    'agent_instance', -- Agent 具体实例
    'workload'        -- Kubernetes Workload Identity
);

CREATE TYPE principal_status AS ENUM (
    'ACTIVE',
    'DISABLED'
);

CREATE TABLE principals (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        principal_kind NOT NULL,
    name        VARCHAR(255) NOT NULL,
    status      principal_status NOT NULL DEFAULT 'ACTIVE',
    tenant_id   VARCHAR(255) NOT NULL DEFAULT 'default',
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(kind, name, tenant_id)
);

CREATE INDEX idx_principals_kind ON principals(kind);
CREATE INDEX idx_principals_status ON principals(status);

-- ============================================================
-- 2. 主体绑定 (Principal Bindings)
-- ============================================================
CREATE TABLE principal_bindings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    principal_id    UUID NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
    agent_type_id   UUID NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
    description     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(principal_id, agent_type_id)
);

-- ============================================================
-- 3. Connector
-- ============================================================
CREATE TYPE connector_status AS ENUM (
    'DRAFT',
    'TESTING',
    'ACTIVE',
    'DEGRADED',
    'QUARANTINED',
    'DISABLED'
);

CREATE TABLE connectors (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    connector_type  VARCHAR(100) NOT NULL, -- 'kubernetes', 'jenkins'
    status          connector_status NOT NULL DEFAULT 'DRAFT',
    environment     VARCHAR(50) NOT NULL DEFAULT 'testing', -- 'production', 'testing', 'staging'
    target_host     VARCHAR(512),
    version         VARCHAR(50) NOT NULL DEFAULT '0.1.0',
    capabilities    JSONB NOT NULL DEFAULT '[]',
    permissions     JSONB NOT NULL DEFAULT '{}',
    config          JSONB NOT NULL DEFAULT '{}',
    last_heartbeat  TIMESTAMPTZ,
    tenant_id       VARCHAR(255) NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(name, tenant_id)
);

CREATE INDEX idx_connectors_status ON connectors(status);
CREATE INDEX idx_connectors_type ON connectors(connector_type);

-- ============================================================
-- 4. 资源 (Resources)
-- ============================================================
CREATE TYPE resource_status AS ENUM (
    'ACTIVE',
    'DISCOVERED',
    'ORPHANED',
    'DISABLED'
);

CREATE TABLE resources (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_type   VARCHAR(100) NOT NULL, -- 'deployment', 'statefulset', 'pod', 'job'
    external_id     VARCHAR(512) NOT NULL, -- 目标系统中的原始 ID
    name            VARCHAR(255) NOT NULL,
    namespace       VARCHAR(255),
    connector_id    UUID NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    owner_team      VARCHAR(255),
    environment     VARCHAR(50) NOT NULL DEFAULT 'testing',
    status          resource_status NOT NULL DEFAULT 'DISCOVERED',
    labels          JSONB NOT NULL DEFAULT '{}',
    annotations     JSONB NOT NULL DEFAULT '{}',
    metadata        JSONB NOT NULL DEFAULT '{}',
    tenant_id       VARCHAR(255) NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(external_id, connector_id),
    UNIQUE(connector_id, resource_type, namespace, name)
);

CREATE INDEX idx_resources_type ON resources(resource_type);
CREATE INDEX idx_resources_connector ON resources(connector_id);
CREATE INDEX idx_resources_environment ON resources(environment);

-- ============================================================
-- 5. 动作定义 (Action Definitions)
-- ============================================================
CREATE TYPE risk_level AS ENUM (
    'low',
    'medium',
    'high',
    'critical'
);

CREATE TYPE execution_mode AS ENUM (
    'SYNCHRONOUS',
    'ASYNCHRONOUS'
);

CREATE TABLE action_definitions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,  -- 'deployment.update_image'
    version         INTEGER NOT NULL DEFAULT 1,
    display_name    VARCHAR(255),
    description     TEXT,
    resource_type   VARCHAR(100) NOT NULL,
    risk_level      risk_level NOT NULL DEFAULT 'low',
    execution_mode  execution_mode NOT NULL DEFAULT 'SYNCHRONOUS',
    parameter_schema JSONB NOT NULL DEFAULT '{}',
    timeout_seconds INTEGER NOT NULL DEFAULT 120,
    tenant_id       VARCHAR(255) NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(name, version)
);

-- ============================================================
-- 6. 资源-动作关联 (Resource Actions)
-- ============================================================
CREATE TABLE resource_actions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id     UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    action_id       UUID NOT NULL REFERENCES action_definitions(id) ON DELETE CASCADE,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(resource_id, action_id)
);

-- ============================================================
-- 7. 策略 (Policies)
-- ============================================================
CREATE TYPE policy_status AS ENUM (
    'DRAFT',
    'PUBLISHED',
    'DEPRECATED',
    'ARCHIVED'
);

CREATE TABLE policies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    status          policy_status NOT NULL DEFAULT 'DRAFT',
    tenant_id       VARCHAR(255) NOT NULL DEFAULT 'default',
    created_by      UUID REFERENCES principals(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(name, tenant_id)
);

-- ============================================================
-- 8. 策略版本 (Policy Versions)
-- ============================================================
CREATE TABLE policy_versions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id       UUID NOT NULL REFERENCES policies(id) ON DELETE CASCADE,
    version         INTEGER NOT NULL,
    rego_content    TEXT NOT NULL,
    bundle_hash     VARCHAR(64),
    status          VARCHAR(50) NOT NULL DEFAULT 'DRAFT',
    published_at    TIMESTAMPTZ,
    published_by    UUID REFERENCES principals(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(policy_id, version)
);

-- ============================================================
-- 9. 策略绑定 (Policy Bindings)
-- ============================================================
CREATE TABLE policy_bindings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id       UUID NOT NULL REFERENCES policies(id) ON DELETE CASCADE,
    principal_kind  principal_kind,
    resource_type   VARCHAR(100),
    environment     VARCHAR(50),
    priority        INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_policy_bindings_lookup ON policy_bindings(principal_kind, resource_type, environment);

-- ============================================================
-- 10. 策略决策 (Policy Decisions)
-- ============================================================
CREATE TYPE decision_result AS ENUM (
    'ALLOW',
    'DENY',
    'REQUIRE_APPROVAL'
);

CREATE TABLE policy_decisions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id    UUID,
    principal_id    UUID REFERENCES principals(id),
    resource_id     UUID REFERENCES resources(id),
    action_id       UUID REFERENCES action_definitions(id),
    opa_input       JSONB NOT NULL,
    opa_output      JSONB NOT NULL,
    result          decision_result NOT NULL,
    bundle_hash     VARCHAR(64),
    decision_seconds INTEGER,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- 11. 访问请求 (Access Requests)
-- ============================================================
CREATE TYPE request_status AS ENUM (
    'PENDING',
    'APPROVED',
    'REJECTED',
    'EXPIRED',
    'CANCELLED'
);

CREATE TABLE access_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    principal_id    UUID NOT NULL REFERENCES principals(id),
    resource_id     UUID NOT NULL REFERENCES resources(id),
    action_id       UUID NOT NULL REFERENCES action_definitions(id),
    parameters      JSONB NOT NULL,
    parameter_hash  VARCHAR(64) NOT NULL,
    reason          TEXT,
    status          request_status NOT NULL DEFAULT 'PENDING',
    decision_id     UUID REFERENCES policy_decisions(id),
    approved_by     UUID REFERENCES principals(id),
    approved_at     TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_access_requests_status ON access_requests(status);
CREATE INDEX idx_access_requests_principal ON access_requests(principal_id);

-- ============================================================
-- 12. 访问授权 (Access Grants)
-- ============================================================
CREATE TYPE grant_status AS ENUM (
    'ACTIVE',
    'USED',
    'REVOKED',
    'EXPIRED'
);

CREATE TABLE access_grants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id      UUID REFERENCES access_requests(id),
    principal_id    UUID NOT NULL REFERENCES principals(id),
    resource_scope  JSONB NOT NULL,
    action_scope    JSONB NOT NULL,
    parameter_hash  VARCHAR(64) NOT NULL,
    scope_hash      VARCHAR(64) NOT NULL,
    max_uses        INTEGER NOT NULL DEFAULT 1,
    use_count       INTEGER NOT NULL DEFAULT 0,
    ttl_seconds     INTEGER NOT NULL DEFAULT 900,
    expires_at      TIMESTAMPTZ NOT NULL,
    status          grant_status NOT NULL DEFAULT 'ACTIVE',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(request_id)
);

CREATE INDEX idx_access_grants_status ON access_grants(status);
CREATE INDEX idx_access_grants_principal ON access_grants(principal_id);
CREATE INDEX idx_access_grants_expires ON access_grants(expires_at) WHERE status = 'ACTIVE';

-- ============================================================
-- 13. 执行记录 (Executions)
-- ============================================================
CREATE TYPE execution_status AS ENUM (
    'WAITING_APPROVAL',
    'QUEUED',
    'DISPATCHED',
    'RUNNING',
    'SUCCEEDED',
    'FAILED',
    'UNKNOWN',
    'CANCELLING',
    'CANCELLED',
    'EXPIRED'
);

CREATE TABLE executions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    grant_id        UUID REFERENCES access_grants(id),
    principal_id    UUID NOT NULL REFERENCES principals(id),
    resource_id     UUID NOT NULL REFERENCES resources(id),
    action_id       UUID NOT NULL REFERENCES action_definitions(id),
    parameters      JSONB NOT NULL,
    parameter_hash  VARCHAR(64) NOT NULL,
    status          execution_status NOT NULL DEFAULT 'QUEUED',
    decision_id     UUID REFERENCES policy_decisions(id),
    idempotency_key VARCHAR(255),
    task_id         VARCHAR(255),
    trace_id        VARCHAR(64) NOT NULL,
    reason          TEXT,
    timeout_seconds INTEGER NOT NULL DEFAULT 120,
    expires_at      TIMESTAMPTZ,
    connector_id    UUID REFERENCES connectors(id),
    result          JSONB,
    error_code      VARCHAR(100),
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_executions_idempotency ON executions(idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_executions_status ON executions(status);
CREATE INDEX idx_executions_principal ON executions(principal_id);
CREATE INDEX idx_executions_connector ON executions(connector_id);
CREATE INDEX idx_executions_trace ON executions(trace_id);

-- ============================================================
-- 14. 执行尝试 (Execution Attempts)
-- ============================================================
CREATE TABLE execution_attempts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id    UUID NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
    attempt         INTEGER NOT NULL,
    status          execution_status NOT NULL DEFAULT 'QUEUED',
    connector_id    UUID REFERENCES connectors(id),
    task_envelope   JSONB,
    result          JSONB,
    error_code      VARCHAR(100),
    error_message   TEXT,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(execution_id, attempt)
);

-- ============================================================
-- 15. 审计事件 (Audit Events)
-- ============================================================
CREATE TYPE audit_action AS ENUM (
    'execution.created',
    'execution.queued',
    'execution.dispatched',
    'execution.started',
    'execution.completed',
    'execution.failed',
    'execution.cancelled',
    'execution.expired',
    'access_request.created',
    'access_request.approved',
    'access_request.rejected',
    'grant.issued',
    'grant.revoked',
    'grant.expired',
    'connector.registered',
    'connector.heartbeat',
    'connector.quarantined',
    'policy.published',
    'permission_drift.detected'
);

CREATE TABLE audit_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    action          audit_action NOT NULL,
    actor_id        UUID,
    actor_type      VARCHAR(100),
    resource_id     UUID,
    execution_id    UUID REFERENCES executions(id),
    grant_id        UUID REFERENCES access_grants(id),
    decision_id     UUID REFERENCES policy_decisions(id),
    connector_id    UUID REFERENCES connectors(id),
    trace_id        VARCHAR(64),
    target_id       VARCHAR(512),
    summary         TEXT,
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_events_action ON audit_events(action);
CREATE INDEX idx_audit_events_execution ON audit_events(execution_id);
CREATE INDEX idx_audit_events_trace ON audit_events(trace_id);
CREATE INDEX idx_audit_events_created ON audit_events(created_at);
CREATE INDEX idx_audit_events_actor ON audit_events(actor_id);

-- ============================================================
-- 16. Outbox 事件
-- ============================================================
CREATE TYPE outbox_status AS ENUM (
    'PENDING',
    'PUBLISHED',
    'FAILED'
);

CREATE TABLE outbox_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type      VARCHAR(255) NOT NULL,
    aggregate_type  VARCHAR(100) NOT NULL,
    aggregate_id    UUID NOT NULL,
    payload         JSONB NOT NULL,
    status          outbox_status NOT NULL DEFAULT 'PENDING',
    publish_count   INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ
);

CREATE INDEX idx_outbox_status ON outbox_events(status, created_at) WHERE status = 'PENDING';

-- ============================================================
-- 17. Credential Lease 记录
-- ============================================================
CREATE TYPE lease_status AS ENUM (
    'ACTIVE',
    'REVOKED',
    'EXPIRED',
    'LEAKED'
);

CREATE TABLE credential_leases (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id    UUID REFERENCES executions(id),
    attempt_id      UUID REFERENCES execution_attempts(id),
    provider        VARCHAR(100) NOT NULL,
    role_name       VARCHAR(255) NOT NULL,
    lease_id        VARCHAR(512),
    fingerprint     VARCHAR(255),
    status          lease_status NOT NULL DEFAULT 'ACTIVE',
    issued_at       TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_credential_leases_execution ON credential_leases(execution_id);
CREATE INDEX idx_credential_leases_status ON credential_leases(status);

-- ============================================================
-- 触发器：自动更新 updated_at
-- ============================================================
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_principals_updated_at
    BEFORE UPDATE ON principals
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER trg_connectors_updated_at
    BEFORE UPDATE ON connectors
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER trg_resources_updated_at
    BEFORE UPDATE ON resources
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER trg_action_definitions_updated_at
    BEFORE UPDATE ON action_definitions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER trg_policies_updated_at
    BEFORE UPDATE ON policies
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER trg_access_requests_updated_at
    BEFORE UPDATE ON access_requests
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER trg_executions_updated_at
    BEFORE UPDATE ON executions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
