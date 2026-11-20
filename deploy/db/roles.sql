-- 应用数据库角色拆分（RLS 地基）。
--
-- 背景：warden 角色既是表 owner 又是 superuser，RLS 对其完全无效。
-- 要让 RLS 生效，应用运行时必须用「非 owner、非 superuser、无 BYPASSRLS」
-- 的普通角色；合法跨租户旁路（认证引导/超管全局/后台 janitor/tenants 管理）
-- 用独立的 BYPASSRLS 特权角色。
--
-- 本脚本只建角色 + 授权，不含任何密码（密码由部署/测试单独 ALTER ROLE 注入），
-- 因此可安全重放。以 superuser（warden）执行：
--   psql -U warden -d warden -f deploy/db/roles.sql
-- 随后注入密码并启用登录（见 make init-db-roles / scripts/init-prod-secrets.sh）：
--   ALTER ROLE warden_app LOGIN PASSWORD '<app_password>';
--   ALTER ROLE warden_privileged LOGIN PASSWORD '<privileged_password>';
--
-- 角色：
--   warden_app        运行时默认连接，受 FORCE RLS 约束（P1/P2 启用后）
--   warden_privileged 合法旁路，BYPASSRLS

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'warden_app') THEN
    CREATE ROLE warden_app NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'warden_privileged') THEN
    CREATE ROLE warden_privileged NOLOGIN NOSUPERUSER BYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT;
  END IF;
END
$$;

-- schema 使用权
GRANT USAGE ON SCHEMA public TO warden_app, warden_privileged;

-- 现有表/序列/函数授权
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO warden_app, warden_privileged;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO warden_app, warden_privileged;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO warden_app, warden_privileged;

-- 未来由 warden（迁移角色）新建的表/序列/函数自动授权，避免漏授
ALTER DEFAULT PRIVILEGES FOR ROLE warden IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO warden_app, warden_privileged;
ALTER DEFAULT PRIVILEGES FOR ROLE warden IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO warden_app, warden_privileged;
ALTER DEFAULT PRIVILEGES FOR ROLE warden IN SCHEMA public
  GRANT EXECUTE ON FUNCTIONS TO warden_app, warden_privileged;