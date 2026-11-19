-- Project-user lifecycle audit vocabulary. Project users are identified by
-- kind='user' and metadata.role='project_user'; no existing identity is changed.

ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'principal.status.updated';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'principal.password.reset';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'user.password.changed';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'user.sessions.revoked';
