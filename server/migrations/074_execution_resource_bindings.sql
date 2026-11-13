-- Related-resource bindings for executions that touch more than one resource
-- (governed cross-table queries span multiple database_table resources). An
-- execution keeps its single primary resource_id in executions; every
-- additional participating resource is recorded here so policy, approval and
-- audit can evaluate the full blast radius, and so ORPHANED/DISABLED members
-- can be detected at dispatch time.

CREATE TABLE IF NOT EXISTS execution_resource_bindings (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id UUID NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
    resource_id  UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    role         VARCHAR(16) NOT NULL DEFAULT 'joined',  -- 'primary' | 'joined'
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(execution_id, resource_id)
);

CREATE INDEX IF NOT EXISTS idx_execution_resource_bindings_execution
    ON execution_resource_bindings(execution_id);
CREATE INDEX IF NOT EXISTS idx_execution_resource_bindings_resource
    ON execution_resource_bindings(resource_id);
