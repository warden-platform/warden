-- AI conversations are user-owned and tenant-bound. Model credentials are
-- never stored here; tool calls are correlated with an AI run, not Execution.
CREATE TABLE IF NOT EXISTS ai_conversations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    VARCHAR(255) NOT NULL,
    principal_id UUID NOT NULL,
    title        VARCHAR(200) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_ai_conversations_owner
    ON ai_conversations(tenant_id, principal_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS ai_runs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
    status          VARCHAR(32) NOT NULL,
    error_code      VARCHAR(100),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    CHECK (status IN ('RUNNING', 'COMPLETED', 'FAILED'))
);

CREATE INDEX IF NOT EXISTS idx_ai_runs_conversation
    ON ai_runs(conversation_id, created_at DESC);

CREATE TABLE IF NOT EXISTS ai_messages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
    run_id          UUID REFERENCES ai_runs(id) ON DELETE SET NULL,
    role            VARCHAR(20) NOT NULL,
    content         TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (role IN ('user', 'assistant'))
);

CREATE INDEX IF NOT EXISTS idx_ai_messages_conversation
    ON ai_messages(conversation_id, created_at, id);

CREATE TABLE IF NOT EXISTS ai_tool_calls (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          UUID NOT NULL REFERENCES ai_runs(id) ON DELETE CASCADE,
    provider_call_id VARCHAR(255),
    tool_name       VARCHAR(100) NOT NULL,
    status          VARCHAR(32) NOT NULL,
    output          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('COMPLETED', 'FAILED'))
);

CREATE INDEX IF NOT EXISTS idx_ai_tool_calls_run ON ai_tool_calls(run_id, created_at);
