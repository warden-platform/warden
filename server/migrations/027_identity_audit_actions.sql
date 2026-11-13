-- Identity lifecycle events are emitted by the admin authentication and
-- Principal management handlers. Keep the database enum aligned so these
-- security-relevant events are not silently dropped.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'principal.deleted';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'user.login';
