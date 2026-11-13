-- Extend the existing H3C device audit with fixed, role-oriented read-only
-- profiles.  The Connector validates the profile and maps it to literal
-- Comware commands; this parameter never becomes an arbitrary CLI channel.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level,
     execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('network.device.audit', 1, '审计网络设备',
     '按设备角色执行固定只读审计。可选 standard、security、core、router、ac、aggregation、switch 或 poe；不开放任意 CLI。',
     'network_device', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"profile":{"type":"string","enum":["standard","security","core","router","ac","aggregation","switch","poe"]}},"additionalProperties":false}'::jsonb,
     180)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds,
    updated_at=now();
