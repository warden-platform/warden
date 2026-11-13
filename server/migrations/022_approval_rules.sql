-- approval_rules: DB-driven approval threshold that supplements the embedded
-- Rego policy. Rego remains the deny-by-default safety net (it decides ALLOW vs
-- REQUIRE_APPROVAL vs DENY). When Rego says REQUIRE_APPROVAL, the control plane
-- consults this table: the highest-priority rule matching (risk_level, environment)
-- decides whether approval is actually needed. This lets admins toggle approval
-- per risk level / environment without editing Rego code.

CREATE TABLE IF NOT EXISTS approval_rules (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    risk_level        risk_level NOT NULL,
    environment       varchar(50) NOT NULL DEFAULT '*',  -- '*' matches any environment
    requires_approval boolean NOT NULL DEFAULT true,
    priority          integer NOT NULL DEFAULT 0,
    description       text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT approval_rules_risk_env_key UNIQUE (risk_level, environment)
);

-- Seed default rules:
--  low    -> never require approval (Rego already ALLOWs these, this is a safety net)
--  medium -> auto-approve in testing, require approval elsewhere
--  high   -> always require approval
INSERT INTO approval_rules (risk_level, environment, requires_approval, priority, description) VALUES
    ('low',    '*',         false, 10, '低风险操作自动放行'),
    ('medium', 'testing',   false, 20, '测试环境中等风险自动放行'),
    ('medium', '*',         true,  10, '生产环境中等风险需审批'),
    ('high',   '*',         true,  10, '高风险操作始终需要审批')
ON CONFLICT (risk_level, environment) DO NOTHING;
