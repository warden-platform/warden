ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'connector.environment.updated';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'resource.environment.assigned';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'resource.environment.inheritance_restored';
