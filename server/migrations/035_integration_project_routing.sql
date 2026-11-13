-- Shared Integration project scope and deterministic resource routing.
CREATE TABLE IF NOT EXISTS integration_project_bindings (
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (integration_id, project_id)
);

CREATE TABLE IF NOT EXISTS integration_project_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    matcher_type VARCHAR(30) NOT NULL,
    pattern VARCHAR(500) NOT NULL,
    priority INTEGER NOT NULL DEFAULT 100,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT integration_project_rules_matcher CHECK (matcher_type IN ('JENKINS_PATH_PREFIX')),
    CONSTRAINT integration_project_rules_priority CHECK (priority BETWEEN 1 AND 10000),
    UNIQUE (integration_id, matcher_type, pattern)
);

CREATE INDEX IF NOT EXISTS idx_integration_project_bindings_project
    ON integration_project_bindings(project_id);
CREATE INDEX IF NOT EXISTS idx_integration_project_rules_lookup
    ON integration_project_rules(integration_id, matcher_type, priority, pattern);

CREATE TABLE IF NOT EXISTS resource_project_consumers (
    resource_id UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (resource_id, project_id)
);

CREATE INDEX IF NOT EXISTS idx_resource_project_consumers_project
    ON resource_project_consumers(project_id);
