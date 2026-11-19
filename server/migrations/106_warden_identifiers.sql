-- 106: pam -> warden 标识符迁移（数据层，配合代码全量切换）
-- 幂等：全新安装经 002-105 已直接产出 warden 标识符，本迁移为空操作；
-- 存量库在此完成 scope/前缀/capability/标签/线格式/凭据引用的转换。
-- 旧格式明文 Key（pam_agk_/pam_rgk_/pam_dtk_）解析层不再接受，统一吊销待重发；
-- 全部会话撤销，重登即携带新 scope。

BEGIN;

-- 1. 权限 scope（JSONB 数组元素）
UPDATE principal_api_keys
SET scopes = replace(scopes::text, 'pam:', 'warden:')::jsonb
WHERE scopes::text LIKE '%pam:%';

UPDATE delegation_tokens
SET scopes = replace(scopes::text, 'pam:', 'warden:')::jsonb
WHERE scopes::text LIKE '%pam:%';

-- 2. 旧前缀 Key 吊销（明文以 pam_ 开头，新解析层不认）+ 展示前缀统一
UPDATE principal_api_keys
SET status = 'REVOKED', revoked_at = now()
WHERE key_prefix LIKE 'pam_%' AND status = 'ACTIVE';

UPDATE principal_api_keys
SET key_prefix = replace(key_prefix, 'pam_', 'warden_')
WHERE key_prefix LIKE 'pam_%';

-- 3. Connector capability
UPDATE connectors
SET capabilities = (
  SELECT jsonb_agg(replace(c, 'pam.change-safety', 'warden.change-safety'))
  FROM jsonb_array_elements_text(capabilities) AS c
)
WHERE capabilities::text LIKE '%pam.change-safety%';

-- 4. 资源标签/元数据 key（pam-* -> warden-*）
UPDATE resources
SET labels = (
  SELECT jsonb_object_agg(replace(k, 'pam-', 'warden-'), v)
  FROM jsonb_each(labels) AS e(k, v)
)
WHERE labels::text LIKE '%pam-%';

UPDATE resources
SET metadata = (
  SELECT jsonb_object_agg(replace(k, 'pam-', 'warden-'), v)
  FROM jsonb_each(metadata) AS e(k, v)
)
WHERE metadata::text LIKE '%pam-%';

-- 5. 执行结果线格式 key
UPDATE executions
SET result = (result - '_pam_safety') || jsonb_build_object('_warden_safety', result->'_pam_safety')
WHERE result ? '_pam_safety';

-- 6. 集成凭据引用前缀
UPDATE integrations
SET credential_ref = 'warden:' || substr(credential_ref, 5)
WHERE credential_ref LIKE 'pam:%';

-- 7. 动作定义描述文案
UPDATE action_definitions
SET description = replace(description, 'PAM', 'Warden')
WHERE description LIKE '%PAM%';

-- 8. 撤销全部活跃会话（重登换取新 scope JWT）
UPDATE admin_sessions
SET revoked_at = now()
WHERE revoked_at IS NULL;

COMMIT;