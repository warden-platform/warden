ALTER TABLE connectors
    ADD COLUMN IF NOT EXISTS task_key_ciphertext BYTEA;

CREATE TABLE IF NOT EXISTS cluster_pairings (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code_hash           VARCHAR(64) NOT NULL UNIQUE,
    bootstrap_token_hash VARCHAR(64) NOT NULL,
    operator_id         VARCHAR(255) NOT NULL,
    cluster_uid         VARCHAR(255) NOT NULL,
    cluster_version     VARCHAR(100) NOT NULL DEFAULT '',
    suggested_audience  VARCHAR(512) NOT NULL DEFAULT '',
    status              VARCHAR(30) NOT NULL DEFAULT 'PENDING',
    connector_id        UUID REFERENCES connectors(id) ON DELETE SET NULL,
    task_key_ciphertext BYTEA NOT NULL,
    config              JSONB NOT NULL DEFAULT '{}',
    expires_at          TIMESTAMPTZ NOT NULL,
    confirmed_at        TIMESTAMPTZ,
    ready_at            TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cluster_pairings_status ON cluster_pairings(status);
CREATE INDEX IF NOT EXISTS idx_cluster_pairings_expires ON cluster_pairings(expires_at);
