-- Integration lifecycle changes are security-relevant configuration changes.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'integration.created';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'integration.updated';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'integration.deleted';
