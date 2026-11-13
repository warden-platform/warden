ALTER TABLE integrations
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_integrations_project_id ON integrations(project_id);

CREATE TABLE IF NOT EXISTS resource_project_overrides (
    resource_id UUID PRIMARY KEY REFERENCES resources(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_by UUID NULL REFERENCES principals(id) ON DELETE SET NULL,
    reason VARCHAR(500) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_resource_project_overrides_project_id
    ON resource_project_overrides(project_id);
CREATE INDEX IF NOT EXISTS idx_resource_project_overrides_created_by
    ON resource_project_overrides(created_by);
