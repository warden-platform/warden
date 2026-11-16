-- 099: Single super administrator model.
-- The built-in break-glass admin (fixed ID) is the only super administrator.
-- The platform_admin role can no longer be assigned to other accounts; any
-- previously promoted account becomes a tenant sub-administrator (tenant_admin)
-- of its own tenant. The built-in admin is untouched (its super-admin status is
-- derived from its fixed ID, not from metadata.role).
BEGIN;

UPDATE principals
SET metadata = metadata || '{"role":"tenant_admin"}'::jsonb,
    updated_at = now()
WHERE kind = 'user'
  AND metadata->>'role' = 'platform_admin'
  AND id::text <> '00000000-0000-0000-0000-000000000003';

COMMIT;