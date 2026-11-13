-- Explicit DingTalk-to-PAM identity bindings for Cool App passwordless login.
-- Application credentials remain file-backed and are never persisted here.
CREATE TABLE IF NOT EXISTS dingtalk_principal_bindings (
    user_id text PRIMARY KEY,
    principal_id uuid NOT NULL UNIQUE REFERENCES principals(id) ON DELETE CASCADE,
    display_name text NOT NULL DEFAULT '',
    tenant_id text NOT NULL DEFAULT 'default',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT dingtalk_binding_user_id_length CHECK (char_length(user_id) BETWEEN 1 AND 128)
);
