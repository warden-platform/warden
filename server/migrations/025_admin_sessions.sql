CREATE TABLE IF NOT EXISTS admin_sessions (
    jti         VARCHAR(128) PRIMARY KEY,
    principal_id UUID NOT NULL REFERENCES principals(id),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_admin_sessions_principal
    ON admin_sessions(principal_id, expires_at);
