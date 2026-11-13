-- A schema/database is an executable resource in the catalog. Give it a
-- dedicated action because action_definitions intentionally bind one action
-- version to exactly one resource type.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES (
    'database.schema.get', 1, '查看 Schema / Database',
    '读取选中 Schema（MySQL 中为 Database）的基本信息及其 Table / View 清单，不读取业务数据。',
    'database_schema', 'low', 'SYNCHRONOUS',
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name"],"additionalProperties":false}',
    60
)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
