-- 110: RLS 第二批——其余租户表。
--
-- strict：读/写仅当前会话租户（app.tenant_id）。
-- shared-read：action_definitions 是平台共享动作目录，业务租户可读 platform
--   行（运行时查询用 tenant_id='platform' OR tenant_id=$n），但只能写本租户。
-- 豁免：tenant_quotas（超管管理表，tenant_id=slug PK，跨租户配额管理不走 RLS）；
--   outbox_events（无消费者，生命周期阶段将整体移除）。
-- 无 tenant_id 的表（principal_api_keys/admin_sessions/cluster_pairings/
--   ai_runs/ai_messages/ai_tool_calls/federated_assertion_replays/各绑定规则表）
-- 不适用 RLS，靠应用层鉴权与父表隔离。
--
-- 旁路（认证引导/超管跨租户/后台 janitor）已路由 warden_privileged 特权池。
-- 幂等：DROP POLICY IF EXISTS 后重建。仅对确有 tenant_id 列的表启用。

DO $$
DECLARE
  t TEXT;
  strict_tables TEXT[] := ARRAY[
    'principals','connectors','resources','policies','integrations',
    'notification_channels','dingtalk_principal_bindings','projects','credentials',
    'provider_capabilities','integration_credential_profiles','database_query_templates',
    'database_query_template_publish_requests','database_column_classifications',
    'database_previews','policy_decisions','approval_rules','policy_matrix_rules',
    'resource_project_bindings','project_memberships','external_identity_bindings',
    'delegation_tokens','external_tenant_bindings','federated_identities',
    'external_project_bindings','principal_invitations','ai_conversations'
  ];
BEGIN
  FOREACH t IN ARRAY strict_tables LOOP
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema='public' AND table_name=t AND column_name='tenant_id') THEN
      EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
      EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
      EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
      EXECUTE format(
        'CREATE POLICY tenant_isolation ON %I
           USING (tenant_id = current_setting(''app.tenant_id'', true))
           WITH CHECK (tenant_id = current_setting(''app.tenant_id'', true))', t);
    END IF;
  END LOOP;

  -- action_definitions：平台共享读 + 本租户写。
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_schema='public' AND table_name='action_definitions' AND column_name='tenant_id') THEN
    ALTER TABLE action_definitions ENABLE ROW LEVEL SECURITY;
    ALTER TABLE action_definitions FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON action_definitions;
    CREATE POLICY tenant_isolation ON action_definitions
      USING (tenant_id = current_setting('app.tenant_id', true) OR tenant_id = 'platform')
      WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
  END IF;
END $$;