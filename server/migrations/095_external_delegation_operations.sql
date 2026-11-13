-- Query and lifecycle support for external delegation operations.
-- These indexes keep the common tenant-scoped investigation paths bounded
-- without duplicating external platform identifiers into audit columns.

CREATE INDEX IF NOT EXISTS idx_audit_external_workspace
    ON audit_events (tenant_id, ((metadata->'external_context'->>'workspaceId')), created_at DESC)
    WHERE metadata ? 'external_context';

CREATE INDEX IF NOT EXISTS idx_audit_external_agent
    ON audit_events (tenant_id, ((metadata->'external_context'->>'agentId')), created_at DESC)
    WHERE metadata ? 'external_context';

CREATE INDEX IF NOT EXISTS idx_audit_external_task
    ON audit_events (tenant_id, ((metadata->'external_context'->>'taskId')), created_at DESC)
    WHERE metadata ? 'external_context';

CREATE INDEX IF NOT EXISTS idx_audit_delegation_token
    ON audit_events (tenant_id, ((metadata->>'delegation_token_id')), created_at DESC)
    WHERE metadata ? 'delegation_token_id';

CREATE INDEX IF NOT EXISTS idx_external_bindings_principal
    ON external_identity_bindings (tenant_id, principal_id, status);

CREATE INDEX IF NOT EXISTS idx_delegation_tokens_user_attribution
    ON delegation_tokens (tenant_id, on_behalf_of_principal_id, accountable_principal_id)
    WHERE revoked_at IS NULL;

ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'external_binding.workspace_revoked';
