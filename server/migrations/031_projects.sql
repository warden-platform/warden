-- ============================================================
-- 031: 资源项目划分
--
-- 资源目录按“项目”组织。归属规则（优先级从高到低）：
--   1. namespace 覆盖规则（project_namespace_rules，可选的细粒度划分）
--   2. 执行通道级项目（connectors.project_id，接入时选定）
--   3. 默认项目（projects.is_default，兜底）
-- 资源本身不落项目字段，归属在查询时实时推导，避免同步链路维护成本。
-- ============================================================

CREATE TABLE projects (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(100) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    is_default  BOOLEAN NOT NULL DEFAULT false,
    tenant_id   VARCHAR(255) NOT NULL DEFAULT 'default',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

-- 每个租户只允许一个默认项目
CREATE UNIQUE INDEX idx_projects_default ON projects (tenant_id) WHERE is_default;

INSERT INTO projects (name, description, is_default)
VALUES ('默认项目', '未划分项目的资源自动归入默认项目', true);

-- 执行通道接入时选定项目；删除项目时通道回落到默认项目
ALTER TABLE connectors ADD COLUMN project_id UUID REFERENCES projects(id) ON DELETE SET NULL;

-- 同一执行通道内按 namespace 覆盖项目归属（可选，粒度更细）
CREATE TABLE project_namespace_rules (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    connector_id UUID NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    namespace    VARCHAR(255) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connector_id, namespace)
);

CREATE INDEX idx_project_ns_rules_project ON project_namespace_rules (project_id);

ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'project.created';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'project.updated';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'project.deleted';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'project.assigned';
