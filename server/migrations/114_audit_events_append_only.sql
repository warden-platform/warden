-- 114: 审计事件 append-only 的数据库层强制。
--
-- 应用代码从不 UPDATE/DELETE audit_events，但迁移 109 的租户 RLS 只限制
-- "哪个租户"，并不禁止同租户内改写审计历史。此前 deploy/db/roles.sql 给
-- warden_app / warden_privileged 授予了全部表的 UPDATE/DELETE，审计的
-- append-only 仅是应用约定。这里撤销两个应用角色的 UPDATE/DELETE 权限；
-- 迁移角色 warden（owner/superuser）保留权限，历史幂等迁移仍可重放。
--
-- 幂等：角色不存在（纯 env 开发环境）时跳过，可重复执行并纳入发布清单。

DO $$
BEGIN
  IF to_regclass('public.audit_events') IS NOT NULL THEN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'warden_app') THEN
      EXECUTE 'REVOKE UPDATE, DELETE ON TABLE public.audit_events FROM warden_app';
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'warden_privileged') THEN
      EXECUTE 'REVOKE UPDATE, DELETE ON TABLE public.audit_events FROM warden_privileged';
    END IF;
  END IF;
END
$$;
