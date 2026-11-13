-- Federated authorization and provisioning foundation.
--
-- An external enterprise is represented by its verified enterprise key and is
-- mapped to exactly one PAM tenant.  The external workspace and project IDs are
-- retained as external context; they never replace PAM's own tenant/project
-- authorization checks.

CREATE TABLE IF NOT EXISTS external_tenant_bindings (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             VARCHAR(255) NOT NULL REFERENCES tenants(slug) ON DELETE CASCADE,
    source                VARCHAR(64) NOT NULL,
    external_enterprise_id VARCHAR(255) NOT NULL,
    external_workspace_id  VARCHAR(255) NOT NULL,
    status                VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    created_by            UUID,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at            TIMESTAMPTZ,
    CONSTRAINT external_tenant_bindings_status_check
        CHECK (status IN ('ACTIVE', 'REVOKED')),
    CONSTRAINT external_tenant_bindings_source_check
        CHECK (source ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT external_tenant_bindings_created_by_fk
        FOREIGN KEY (created_by, tenant_id) REFERENCES principals(id, tenant_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_external_tenant_bindings_enterprise
    ON external_tenant_bindings(source, external_enterprise_id)
    WHERE status = 'ACTIVE';
CREATE UNIQUE INDEX IF NOT EXISTS uq_external_tenant_bindings_workspace
    ON external_tenant_bindings(source, external_workspace_id)
    WHERE status = 'ACTIVE';

CREATE TABLE IF NOT EXISTS federated_identities (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      VARCHAR(255) NOT NULL REFERENCES tenants(slug) ON DELETE CASCADE,
    issuer         VARCHAR(255) NOT NULL,
    subject        VARCHAR(255) NOT NULL,
    principal_id   UUID NOT NULL,
    email          VARCHAR(320),
    display_name   VARCHAR(255),
    status         VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    last_login_at  TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT federated_identities_status_check
        CHECK (status IN ('ACTIVE', 'REVOKED')),
    CONSTRAINT federated_identities_principal_fk
        FOREIGN KEY (principal_id, tenant_id) REFERENCES principals(id, tenant_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_federated_identities_issuer_subject
    ON federated_identities(issuer, subject);
CREATE INDEX IF NOT EXISTS idx_federated_identities_principal
    ON federated_identities(tenant_id, principal_id);

CREATE TABLE IF NOT EXISTS external_project_bindings (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             VARCHAR(255) NOT NULL REFERENCES tenants(slug) ON DELETE CASCADE,
    source                VARCHAR(64) NOT NULL,
    external_workspace_id VARCHAR(255) NOT NULL,
    external_project_id   VARCHAR(255) NOT NULL,
    external_project_name VARCHAR(255),
    project_id            UUID NOT NULL,
    status                VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    created_by            UUID,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at            TIMESTAMPTZ,
    CONSTRAINT external_project_bindings_status_check
        CHECK (status IN ('ACTIVE', 'REVOKED')),
    CONSTRAINT external_project_bindings_source_check
        CHECK (source ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT external_project_bindings_project_fk
        FOREIGN KEY (project_id, tenant_id) REFERENCES projects(id, tenant_id) ON DELETE RESTRICT,
    CONSTRAINT external_project_bindings_created_by_fk
        FOREIGN KEY (created_by, tenant_id) REFERENCES principals(id, tenant_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_external_project_bindings_active
    ON external_project_bindings(tenant_id, source, external_workspace_id, external_project_id)
    WHERE status = 'ACTIVE';
CREATE INDEX IF NOT EXISTS idx_external_project_bindings_lookup
    ON external_project_bindings(tenant_id, source, external_workspace_id, status);

-- One-time assertion replay protection.  Expired rows may be pruned by a
-- future maintenance job; the unique key is the security boundary today.
CREATE TABLE IF NOT EXISTS federated_assertion_replays (
    issuer      VARCHAR(255) NOT NULL,
    assertion_id VARCHAR(255) NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (issuer, assertion_id)
);

CREATE INDEX IF NOT EXISTS idx_federated_assertion_replays_expiry
    ON federated_assertion_replays(expires_at);

ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'federated.tenant_bound';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'federated.project_bound';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'federated.user_provisioned';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'federated.login';
