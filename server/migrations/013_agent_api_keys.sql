-- Per-Agent API credentials and immutable credential attribution.

CREATE TYPE api_key_status AS ENUM ('ACTIVE', 'REVOKED');

CREATE TABLE principal_api_keys (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    principal_id        UUID NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
    name                VARCHAR(255) NOT NULL,
    key_prefix          VARCHAR(32) NOT NULL,
    key_hash            BYTEA NOT NULL,
    status              api_key_status NOT NULL DEFAULT 'ACTIVE',
    scopes              JSONB NOT NULL DEFAULT '[]',
    expires_at          TIMESTAMPTZ,
    last_used_at        TIMESTAMPTZ,
    last_used_ip        INET,
    created_by          UUID REFERENCES principals(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at          TIMESTAMPTZ,
    revoked_by          UUID REFERENCES principals(id),
    rotation_parent_id  UUID REFERENCES principal_api_keys(id),
    CONSTRAINT principal_api_keys_hash_length CHECK (octet_length(key_hash) = 32),
    CONSTRAINT principal_api_keys_scopes_array CHECK (jsonb_typeof(scopes) = 'array')
);

CREATE INDEX idx_principal_api_keys_principal ON principal_api_keys(principal_id, created_at DESC);
CREATE INDEX idx_principal_api_keys_active ON principal_api_keys(id) WHERE status = 'ACTIVE';

ALTER TABLE executions
    ADD COLUMN credential_id UUID REFERENCES principal_api_keys(id);
CREATE INDEX idx_executions_credential ON executions(credential_id);

-- Idempotency keys are owned by a Principal, not globally shared between Agents.
DROP INDEX idx_executions_idempotency;
CREATE UNIQUE INDEX idx_executions_principal_idempotency
    ON executions(principal_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

ALTER TABLE audit_events
    ADD COLUMN credential_id UUID REFERENCES principal_api_keys(id);
CREATE INDEX idx_audit_events_credential ON audit_events(credential_id);

ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'principal.created';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.created';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.revoked';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.rotated';
