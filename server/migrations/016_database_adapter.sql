INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('database.instance.get', 1, '查看数据库实例', '读取数据库类型、版本和连接状态', 'database_instance', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('database.schema.list', 1, '列出数据库 Schema', '列出凭证可见且在 Integration Scope 内的 Schema', 'database_instance', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('database.table.list', 1, '列出数据库表', '列出凭证可见且在 Integration Scope 内的 Table 和 View', 'database_instance', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 60),
    ('database.table.describe', 1, '查看表结构', '读取列名、数据类型、可空性和默认值，不读取业务数据', 'database_table', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
