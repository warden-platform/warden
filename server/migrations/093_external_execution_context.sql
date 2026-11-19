-- Structured attribution for calls brokered by an external orchestration
-- platform. This data is audit context only;
-- authorization continues to use the authenticated Warden Principal and project
-- scope checks.

ALTER TABLE executions
    ADD COLUMN IF NOT EXISTS external_context JSONB;

ALTER TABLE access_requests
    ADD COLUMN IF NOT EXISTS external_context JSONB;

DO $do$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'executions_external_context_object_check'
    ) THEN
        ALTER TABLE executions ADD CONSTRAINT executions_external_context_object_check
            CHECK (external_context IS NULL OR jsonb_typeof(external_context) = 'object');
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'access_requests_external_context_object_check'
    ) THEN
        ALTER TABLE access_requests ADD CONSTRAINT access_requests_external_context_object_check
            CHECK (external_context IS NULL OR jsonb_typeof(external_context) = 'object');
    END IF;
END $do$;

CREATE INDEX IF NOT EXISTS idx_executions_external_context_task
    ON executions ((external_context->>'taskId'))
    WHERE external_context IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_executions_external_context_agent
    ON executions ((external_context->>'agentId'))
    WHERE external_context IS NOT NULL;
