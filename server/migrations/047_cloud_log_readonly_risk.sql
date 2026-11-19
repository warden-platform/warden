-- Cloud log queries are bounded, read-only diagnostics like Pod log reads.
-- Keep them below the global medium-risk approval fallback so AIOps agents can
-- diagnose incidents without a human approval for every query.
UPDATE action_definitions
SET risk_level = 'low'
WHERE name IN ('huawei.lts.logs.query', 'aliyun.sls.logs.query') AND version = 1;
