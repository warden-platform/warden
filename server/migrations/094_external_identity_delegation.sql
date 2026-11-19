-- Trusted external identity bindings and short-lived delegation credentials.
-- Long-lived Warden API Keys remain server-side Broker credentials; Agents use
-- delegation tokens issued only after these bindings are verified.

CREATE TABLE IF NOT EXISTS external_identity_bindings (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             VARCHAR(255) NOT NULL REFERENCES tenants(slug) ON DELETE CASCADE,
    source                VARCHAR(64) NOT NULL,
    workspace_id          VARCHAR(255) NOT NULL,
    external_project_id   VARCHAR(255),
    identity_type         VARCHAR(16) NOT NULL,
    external_id           VARCHAR(255) NOT NULL,
    principal_id          UUID NOT NULL,
    project_id            UUID,
    status                VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    created_by            UUID,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at            TIMESTAMPTZ,
    CONSTRAINT external_identity_bindings_type_check
        CHECK (identity_type IN ('AGENT', 'USER')),
    CONSTRAINT external_identity_bindings_status_check
        CHECK (status IN ('ACTIVE', 'REVOKED')),
    CONSTRAINT external_identity_bindings_source_check
        CHECK (source ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT external_identity_bindings_principal_fk
        FOREIGN KEY (principal_id, tenant_id) REFERENCES principals(id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT external_identity_bindings_project_fk
        FOREIGN KEY (project_id, tenant_id) REFERENCES projects(id, tenant_id) ON DELETE RESTRICT,
    CONSTRAINT external_identity_bindings_created_by_fk
        FOREIGN KEY (created_by, tenant_id) REFERENCES principals(id, tenant_id),
    CONSTRAINT external_identity_bindings_user_project_check
        CHECK (identity_type = 'AGENT' OR project_id IS NULL)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_external_identity_bindings_active
    ON external_identity_bindings
       (tenant_id, source, workspace_id, COALESCE(external_project_id, ''), identity_type, external_id)
    WHERE status = 'ACTIVE';
CREATE INDEX IF NOT EXISTS idx_external_identity_bindings_lookup
    ON external_identity_bindings (tenant_id, source, workspace_id, identity_type, external_id)
    WHERE status = 'ACTIVE';

CREATE TABLE IF NOT EXISTS delegation_tokens (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             VARCHAR(255) NOT NULL REFERENCES tenants(slug) ON DELETE CASCADE,
    parent_credential_id  UUID NOT NULL REFERENCES principal_api_keys(id) ON DELETE CASCADE,
    principal_id          UUID NOT NULL,
    on_behalf_of_principal_id UUID,
    accountable_principal_id UUID,
    token_hash            BYTEA NOT NULL,
    scopes                JSONB NOT NULL DEFAULT '[]',
    external_context      JSONB NOT NULL,
    acting_project_id     UUID,
    issued_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at            TIMESTAMPTZ NOT NULL,
    last_used_at          TIMESTAMPTZ,
    revoked_at            TIMESTAMPTZ,
    CONSTRAINT delegation_tokens_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT delegation_tokens_scopes_array CHECK (jsonb_typeof(scopes) = 'array'),
    CONSTRAINT delegation_tokens_context_object CHECK (jsonb_typeof(external_context) = 'object'),
    CONSTRAINT delegation_tokens_principal_fk
        FOREIGN KEY (principal_id, tenant_id) REFERENCES principals(id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT delegation_tokens_on_behalf_of_fk
        FOREIGN KEY (on_behalf_of_principal_id, tenant_id) REFERENCES principals(id, tenant_id) ON DELETE RESTRICT,
    CONSTRAINT delegation_tokens_accountable_fk
        FOREIGN KEY (accountable_principal_id, tenant_id) REFERENCES principals(id, tenant_id) ON DELETE RESTRICT,
    CONSTRAINT delegation_tokens_project_fk
        FOREIGN KEY (acting_project_id, tenant_id) REFERENCES projects(id, tenant_id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_delegation_tokens_hash ON delegation_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_delegation_tokens_active
    ON delegation_tokens(id, tenant_id, expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_delegation_tokens_principal
    ON delegation_tokens(tenant_id, principal_id, issued_at DESC);

ALTER TABLE executions ADD COLUMN IF NOT EXISTS delegation_token_id UUID;
ALTER TABLE executions ADD COLUMN IF NOT EXISTS on_behalf_of_principal_id UUID;
ALTER TABLE executions ADD COLUMN IF NOT EXISTS accountable_principal_id UUID;
ALTER TABLE access_requests ADD COLUMN IF NOT EXISTS delegation_token_id UUID;
ALTER TABLE access_requests ADD COLUMN IF NOT EXISTS on_behalf_of_principal_id UUID;
ALTER TABLE access_requests ADD COLUMN IF NOT EXISTS accountable_principal_id UUID;

DO $do$
DECLARE table_name text;
DECLARE constraint_name text;
DECLARE column_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY['executions','access_requests'] LOOP
        constraint_name := table_name || '_delegation_token_fk';
        IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname=constraint_name AND conrelid=table_name::regclass) THEN
            EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY (delegation_token_id) REFERENCES delegation_tokens(id)', table_name, constraint_name);
        END IF;
        FOREACH column_name IN ARRAY ARRAY['on_behalf_of_principal_id','accountable_principal_id'] LOOP
            constraint_name := table_name || '_' || column_name || '_tenant_fk';
            IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname=constraint_name AND conrelid=table_name::regclass) THEN
                EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY (%I, tenant_id) REFERENCES principals(id, tenant_id)', table_name, constraint_name, column_name);
            END IF;
        END LOOP;
    END LOOP;
END $do$;

CREATE INDEX IF NOT EXISTS idx_executions_delegation ON executions(tenant_id, delegation_token_id);
CREATE INDEX IF NOT EXISTS idx_executions_on_behalf_of ON executions(tenant_id, on_behalf_of_principal_id, created_at DESC);

ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'external_binding.created';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'external_binding.revoked';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'delegation.issued';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'delegation.revoked';
