-- Executes a published query template. The Agent supplies only
-- templateId/templateVersion/bindings; the control plane resolves the immutable
-- canonical AST, merges bindings, and injects the resolved AST (plus masking
-- and governance context) after parameter validation, so the Connector never
-- trusts a template the Agent chose. Published templates are pre-approved and
-- pre-costed, so unlike database.query.execute this needs no per-execution
-- preview — though the Connector still runs the EXPLAIN cost gate on every run.

INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level,
     execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('database.query_template.execute', 1, '执行查询模板',
     '执行已发布、不可变的查询模板；Agent 仅提供模板 ID/版本与绑定参数，控制面注入模板 AST 与治理上下文，执行仍受 EXPLAIN 成本门禁与结果上限约束。',
     'database_table', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"templateId":{"type":"string","minLength":1,"maxLength":128},"templateVersion":{"type":"integer","minimum":1},"bindings":{"type":"object"}},"required":["templateId","templateVersion"],"additionalProperties":false}',
     30)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
