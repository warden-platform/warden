-- Tenant-scoped project membership and project RBAC.
-- The table is intentionally soft-deletable (DISABLED) so historical
-- execution/audit records remain explainable.

ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'project.member.added';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'project.member.updated';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'project.member.removed';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'project.membership.enforcement.updated';

CREATE TABLE IF NOT EXISTS project_memberships (
    tenant_id    VARCHAR(255) NOT NULL,
    project_id   UUID NOT NULL,
    principal_id UUID NOT NULL,
    role         VARCHAR(32) NOT NULL,
    status       VARCHAR(32) NOT NULL DEFAULT 'ACTIVE',
    created_by   UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, principal_id),
    CONSTRAINT project_memberships_tenant_fk FOREIGN KEY (tenant_id) REFERENCES tenants(slug) ON DELETE CASCADE,
    CONSTRAINT project_memberships_project_tenant_fk FOREIGN KEY (project_id, tenant_id) REFERENCES projects(id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT project_memberships_principal_tenant_fk FOREIGN KEY (principal_id, tenant_id) REFERENCES principals(id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT project_memberships_created_by_tenant_fk FOREIGN KEY (created_by, tenant_id) REFERENCES principals(id, tenant_id),
    CONSTRAINT project_memberships_role_check CHECK (role IN ('PROJECT_ADMIN','OPERATOR','VIEWER')),
    CONSTRAINT project_memberships_status_check CHECK (status IN ('ACTIVE','DISABLED'))
);

CREATE INDEX IF NOT EXISTS idx_project_memberships_tenant_principal
    ON project_memberships(tenant_id, principal_id, status, project_id);
CREATE INDEX IF NOT EXISTS idx_project_memberships_tenant_project
    ON project_memberships(tenant_id, project_id, status, role);
