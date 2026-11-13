-- Purpose-bound credentials for automation that registers PAM resources.
-- Registration credentials are distinct from execution Agent keys, carry an
-- explicit API audience, and never receive the global pam:admin wildcard.
ALTER TABLE principal_api_keys
    ADD COLUMN IF NOT EXISTS purpose VARCHAR(32) NOT NULL DEFAULT 'AGENT',
    ADD COLUMN IF NOT EXISTS audience VARCHAR(512);

ALTER TABLE principal_api_keys
    DROP CONSTRAINT IF EXISTS principal_api_keys_purpose_check,
    DROP CONSTRAINT IF EXISTS principal_api_keys_audience_check;

ALTER TABLE principal_api_keys
    ADD CONSTRAINT principal_api_keys_purpose_check
        CHECK (purpose IN ('AGENT', 'REGISTRATION')),
    ADD CONSTRAINT principal_api_keys_audience_check
        CHECK ((purpose = 'AGENT' AND audience IS NULL) OR
               (purpose = 'REGISTRATION' AND audience IS NOT NULL AND length(audience) > 0));

CREATE INDEX IF NOT EXISTS idx_principal_api_keys_registration
    ON principal_api_keys(principal_id, created_at DESC)
    WHERE purpose = 'REGISTRATION';

ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'authorization.denied';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'integration.project_scope.updated';
