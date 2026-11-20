-- 109: RLS 首批——高风险纯租户表（executions / access_requests /
-- access_grants / audit_events）。这些表无 platform 共享语义，策略为 strict：
-- 读/写都只允许当前会话租户（app.tenant_id，由连接池 BeforeAcquire 从 ctx 注入）。
--
-- 生效前提（P0 已落地）：应用运行时连 warden_app（非 owner/非 superuser/
-- NOBYPASSRLS），FORCE 使 owner 亦受约束；合法跨租户旁路（超管全局
-- ListAllTenants、后台 janitor 扫描、钉钉 GetByIDAnyTenant）走 warden_privileged
-- （BYPASSRLS）特权池，已在仓储层路由。superuser 迁移角色不受 RLS 限制。
--
-- 空/未设租户 → current_setting(...,true) 为 NULL 或 ''，匹配不到任何行
-- （fail-closed）。幂等：DROP POLICY IF EXISTS 后重建，可纳入 migrate-release。

DO $$
DECLARE
  t TEXT;
BEGIN
  FOREACH t IN ARRAY ARRAY['executions','access_requests','access_grants','audit_events']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
    EXECUTE format(
      'CREATE POLICY tenant_isolation ON %I
         USING (tenant_id = current_setting(''app.tenant_id'', true))
         WITH CHECK (tenant_id = current_setting(''app.tenant_id'', true))', t);
  END LOOP;
END $$;