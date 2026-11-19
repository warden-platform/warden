-- Visual policy matrix. Rego remains the deny-by-default lower bound; these
-- rules can tighten ALLOW to approval/deny or relax REQUIRE_APPROVAL to ALLOW,
-- but can never override a Rego DENY.
CREATE TABLE IF NOT EXISTS policy_matrix_rules (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    varchar(120) NOT NULL,
    project_id              uuid REFERENCES projects(id) ON DELETE CASCADE,
    resource_type           varchar(100) NOT NULL DEFAULT '*',
    action_name             varchar(150) NOT NULL DEFAULT '*',
    environment             varchar(50) NOT NULL DEFAULT '*',
    risk_level              risk_level,
    principal_kind          principal_kind,
    decision                decision_result NOT NULL,
    priority                integer NOT NULL DEFAULT 100 CHECK (priority BETWEEN 0 AND 10000),
    enabled                 boolean NOT NULL DEFAULT true,
    description             text,
    hit_count               bigint NOT NULL DEFAULT 0,
    last_hit_at             timestamptz,
    legacy_approval_rule_id uuid UNIQUE REFERENCES approval_rules(id) ON DELETE SET NULL,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT policy_matrix_rule_name_nonempty CHECK (length(btrim(name)) > 0),
    CONSTRAINT policy_matrix_rule_environment CHECK (environment IN ('*', 'testing', 'production'))
);

CREATE INDEX IF NOT EXISTS idx_policy_matrix_rules_match
    ON policy_matrix_rules(enabled, priority DESC, action_name, resource_type, environment);
CREATE INDEX IF NOT EXISTS idx_policy_matrix_rules_project ON policy_matrix_rules(project_id) WHERE project_id IS NOT NULL;

ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'policy.rule.created';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'policy.rule.updated';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'policy.rule.deleted';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'policy.rule.simulated';

-- Preserve the existing approval switches as matrix rules. The source id makes
-- this migration idempotent while retaining any administrator changes.
INSERT INTO policy_matrix_rules
    (name, resource_type, action_name, environment, risk_level, decision,
     priority, enabled, description, legacy_approval_rule_id)
SELECT
    concat('兼容规则 · ', risk_level::text, ' · ', environment),
    '*', '*', environment, risk_level,
    CASE WHEN requires_approval THEN 'REQUIRE_APPROVAL'::decision_result ELSE 'ALLOW'::decision_result END,
    priority, true, description, id
FROM approval_rules
ON CONFLICT (legacy_approval_rule_id) DO NOTHING;
