-- 104: audit action for super-admin re-enabling a disabled local account.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'account.reenabled';