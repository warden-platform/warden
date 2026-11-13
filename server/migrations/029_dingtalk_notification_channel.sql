-- DingTalk approval notification preferences. Webhook and signing secret are
-- intentionally not stored here; they are injected into pam-control via
-- PAM_DINGTALK_WEBHOOK_FILE and PAM_DINGTALK_SECRET_FILE.

CREATE TABLE IF NOT EXISTS notification_channels (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type        varchar(50) NOT NULL,
    name        varchar(100) NOT NULL,
    enabled     boolean NOT NULL DEFAULT false,
    events      text[] NOT NULL DEFAULT ARRAY[
                    'access_request.created',
                    'access_request.approved',
                    'access_request.rejected',
                    'access_request.expired'
                ]::text[],
    tenant_id   varchar(100) NOT NULL DEFAULT 'default',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notification_channels_type_tenant_key UNIQUE (type, tenant_id),
    CONSTRAINT notification_channels_type_check CHECK (type IN ('dingtalk'))
);

INSERT INTO notification_channels (type, name, enabled)
VALUES ('dingtalk', '钉钉审批通知', false)
ON CONFLICT (type, tenant_id) DO NOTHING;

ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'notification.sent';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'notification.failed';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'notification.tested';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'notification.updated';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'access_request.expired';
