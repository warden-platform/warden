-- Audit action for removing a member from a business organization (person left
-- / access revoked). SSO members are moved back to the unaffiliated hall; local
-- members are disabled. Both record this action with an "action" metadata of
-- "removed" or "disabled".
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'account.removed';
