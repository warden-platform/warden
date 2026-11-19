-- Generic provider reads reuse the existing policy/grant/execution/audit
-- lifecycle. The control plane applies an additional immutable GET/HEAD
-- capability contract before an execution can be created.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('provider.request', 1, 'Provider 只读请求', '按资源能力目录执行受治理的 GET/HEAD 请求',
     'provider_resource', 'low', 'SYNCHRONOUS',
     '{"type":"object","required":["providerRequest"],"additionalProperties":false,"properties":{"providerRequest":{"type":"object"}}}'::jsonb,
     30)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name, description=EXCLUDED.description,
    risk_level='low', execution_mode='SYNCHRONOUS', parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=30, updated_at=now();
