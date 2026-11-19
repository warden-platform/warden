ALTER TABLE integrations
    ADD COLUMN IF NOT EXISTS environment VARCHAR(20);

ALTER TABLE integrations
    DROP CONSTRAINT IF EXISTS integrations_environment_check;
ALTER TABLE integrations
    ADD CONSTRAINT integrations_environment_check
    CHECK (environment IS NULL OR environment IN ('development', 'testing', 'production'));

CREATE TABLE IF NOT EXISTS resource_environment_overrides (
    resource_id UUID PRIMARY KEY REFERENCES resources(id) ON DELETE CASCADE,
    environment VARCHAR(20) NOT NULL,
    created_by UUID NULL REFERENCES principals(id) ON DELETE SET NULL,
    reason VARCHAR(500) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT resource_environment_overrides_environment_check
        CHECK (environment IN ('development', 'testing', 'production'))
);

CREATE INDEX IF NOT EXISTS idx_resource_environment_overrides_environment
    ON resource_environment_overrides(environment);
CREATE INDEX IF NOT EXISTS idx_resource_environment_overrides_created_by
    ON resource_environment_overrides(created_by);

ALTER TABLE policy_matrix_rules
    DROP CONSTRAINT IF EXISTS policy_matrix_rule_environment;
ALTER TABLE policy_matrix_rules
    ADD CONSTRAINT policy_matrix_rule_environment
    CHECK (environment IN ('*', 'development', 'testing', 'production'));
