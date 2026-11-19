-- Account role administration is a security-relevant configuration change.
-- Keep the audit enum aligned so role updates are never silently dropped by
-- the enum cast in createAudit().
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'account.role_updated';
