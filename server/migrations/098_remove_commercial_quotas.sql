-- 098: Remove commercial-style metering quotas.
-- Warden does not sell capacity tiers: identities (principals) and connectors
-- are unlimited. The only remaining per-tenant limit is the concurrency safety
-- valve (max_concurrent_tasks), which protects the shared control plane from
-- runaway execution floods — an operational guard, not a commercial meter.
-- max_requests_per_minute and audit_retention_days were stored but never
-- enforced; drop them instead of surfacing dead knobs. Re-introduce them as
-- real features (rate limiting / audit retention) when actually implemented.
BEGIN;

ALTER TABLE tenant_quotas
    DROP COLUMN IF EXISTS max_principals,
    DROP COLUMN IF EXISTS max_connectors,
    DROP COLUMN IF EXISTS max_requests_per_minute,
    DROP COLUMN IF EXISTS audit_retention_days;

COMMIT;