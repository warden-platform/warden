-- Generic governed structured write (OPE-7516 Phase 2).
--
-- provider.write is the high-risk companion to provider.request. Writes are
-- SYNCHRONOUS like reads, but REQUIRE_APPROVAL via Rego (change-safety), and
-- the control plane applies an additional immutable write contract before an
-- execution can be created: the capability must declare write=true, may only
-- declare POST/PUT/PATCH (never DELETE — destructive changes stay on dedicated
-- approved actions), never targets native Kubernetes (dedicated dry-run CRUD),
-- and any request body is validated against the capability's JSON Schema.

-- 1) provider.write action definition.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('provider.write', 1, 'Provider 治理化写请求', '按写能力目录执行受治理的结构化写请求（POST/PUT/PATCH，需审批）',
     'provider_resource', 'high', 'SYNCHRONOUS',
     '{"type":"object","required":["providerRequest"],"additionalProperties":false,"properties":{"providerRequest":{"type":"object"}}}'::jsonb,
     120)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name, description=EXCLUDED.description,
    risk_level='high', execution_mode='SYNCHRONOUS', parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=120, updated_at=now();

-- 2) provider_capabilities gains the write-governance columns. Existing read
--    capabilities keep write=false, max_body_bytes=0, request_body_schema={}.
ALTER TABLE provider_capabilities
    ADD COLUMN IF NOT EXISTS write BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS max_body_bytes INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS request_body_schema JSONB NOT NULL DEFAULT '{}';
