-- Credential governance: Warden-managed encrypted credential ledger with
-- reference, binding, rotation, revocation and expiry management.
--
-- Credentials are governed through the execution pipeline (POST /api/v1/executions)
-- exactly like every other action: policy decision (Rego + matrix + production
-- change floor), approval, audit and timeout apply. The plaintext secret lives
-- only in memory and in AES-GCM ciphertext in this table; executions.parameters
-- and audit_events carry only value_sha256 + value_preview.

-- 1) credentials table (Warden 托管加密存储)
-- 发布流水线每次发布都会重跑 migrations.txt 中的全部迁移，故必须幂等。
CREATE TABLE IF NOT EXISTS credentials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       VARCHAR(255) NOT NULL DEFAULT 'default',
    name            VARCHAR(253) NOT NULL,            -- warden: 引用名，唯一
    kind            VARCHAR(32)  NOT NULL DEFAULT 'token', -- token/password/ssh_key/access_key/api_key
    username        VARCHAR(253) NOT NULL DEFAULT '',
    encrypted_value BYTEA NOT NULL,                   -- credentialCipher AES-GCM
    value_preview   VARCHAR(64)  NOT NULL DEFAULT '', -- 掩码预览（如 "ab****wxyz"），永不全文
    purpose         TEXT         NOT NULL DEFAULT '',
    scope           JSONB        NOT NULL DEFAULT '{}', -- 用途绑定：{"bindings":[{target_type,target_name}...]}
    status          VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE', -- ACTIVE/REVOKED/EXPIRED
    version         INTEGER      NOT NULL DEFAULT 1,
    expires_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    created_by      UUID REFERENCES principals(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_credentials_status ON credentials(status);
CREATE INDEX IF NOT EXISTS idx_credentials_expires_at ON credentials(expires_at) WHERE status = 'ACTIVE';

-- 2) 控制面执行标记 + 审批暂存明文（密文）
ALTER TABLE executions ADD COLUMN IF NOT EXISTS control_plane_execution BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE executions ADD COLUMN IF NOT EXISTS pending_secret BYTEA;

-- 3) audit_action 新值（pattern 同 migration 055）
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.get';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.list';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.created';   -- 013 已有，IF NOT EXISTS 幂等
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.rotated';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.revoked';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.bound';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.unbound';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.expired';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'credential.used';      -- warden: 引用解析

-- 4) credential 治理动作定义。get/list 免审批（low）；create/rotate/bind/unbind
--    需审批（high）；revoke 最高风险（critical）。value 参数在参数校验后由
--    控制面从 parameters 中剥离，只保留 value_sha256。
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('credential.get', 1, '查看凭据', '按身份资源读取 Warden 托管凭据的元数据（名称、种类、状态、版本、掩码预览、绑定），永不返回明文。', 'credential', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string","pattern":"^[a-z0-9][a-z0-9.-]{0,251}[a-z0-9]$"}},"additionalProperties":false}', 30),
    ('credential.list', 1, '列出凭据', '列出凭据库中的全部凭据元数据（含状态、版本、掩码预览、过期时间）。', 'credential_vault', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{},"additionalProperties":false}', 30),
    ('credential.create', 1, '创建凭据', '在 Warden 托管加密存储中登记新凭据。审批通过后密文入库，明文仅存在于内存。', 'credential_vault', 'high', 'SYNCHRONOUS',
     '{"type":"object","required":["name","kind","value"],"properties":{"name":{"type":"string","pattern":"^[a-z0-9][a-z0-9.-]{0,251}[a-z0-9]$"},"kind":{"type":"string","enum":["token","password","ssh_key","access_key","api_key"]},"username":{"type":"string","maxLength":253},"value":{"type":"string","minLength":1},"purpose":{"type":"string","maxLength":2000},"expiresInSeconds":{"type":"integer","minimum":1,"maximum":31536000}},"additionalProperties":false}', 60),
    ('credential.rotate', 1, '轮换凭据', '用新 value 重加密凭据并将版本加一；可选更新过期时间。', 'credential', 'high', 'SYNCHRONOUS',
     '{"type":"object","required":["value"],"properties":{"value":{"type":"string","minLength":1},"expiresInSeconds":{"type":"integer","minimum":1,"maximum":31536000}},"additionalProperties":false}', 60),
    ('credential.revoke', 1, '吊销凭据', '吊销 Warden 托管凭据。审批通过后须输入与凭据名完全一致的 confirmName 二次确认。', 'credential', 'critical', 'SYNCHRONOUS',
     '{"type":"object","required":["confirmName"],"properties":{"confirmName":{"type":"string","minLength":1,"maxLength":253}},"additionalProperties":false}', 60),
    ('credential.bind', 1, '绑定凭据', '将凭据绑定到指定目标（target_type + target_name），限制其用途范围。', 'credential', 'high', 'SYNCHRONOUS',
     '{"type":"object","required":["targetType","targetName"],"properties":{"targetType":{"type":"string","minLength":1,"maxLength":100},"targetName":{"type":"string","minLength":1,"maxLength":253}},"additionalProperties":false}', 60),
    ('credential.unbind', 1, '解除绑定', '解除凭据对指定目标的用途绑定。', 'credential', 'high', 'SYNCHRONOUS',
     '{"type":"object","required":["targetType","targetName"],"properties":{"targetType":{"type":"string","minLength":1,"maxLength":100},"targetName":{"type":"string","minLength":1,"maxLength":253}},"additionalProperties":false}', 60)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
