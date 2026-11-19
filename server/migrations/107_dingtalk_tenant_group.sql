-- 107: 钉钉审批租户级群隔离
-- 每个租户在 notification_channels 绑定自己的审批群（open_conversation_id）；
-- 钉钉应用凭据保持部署级共享（与 OIDC 单 IdP 同构）。
-- env WARDEN_DINGTALK_OPEN_CONVERSATION_ID 语义收敛为 platform 租户的群，
-- 由代码层兜底，不落库。部分唯一索引保证一个群只能绑定一个租户。
-- 幂等：可重放。

BEGIN;

ALTER TABLE notification_channels ADD COLUMN IF NOT EXISTS open_conversation_id VARCHAR(512);

CREATE UNIQUE INDEX IF NOT EXISTS notification_channels_type_conversation_key
    ON notification_channels(type, open_conversation_id)
    WHERE open_conversation_id IS NOT NULL;

COMMIT;