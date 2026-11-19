-- Governed cross-table query actions. Both carry a structured AST (not raw
-- SQL); the Connector strictly decodes, canonicalizes, scope-checks, renders
-- and EXPLAIN-gates it. execute additionally requires the previewHash emitted
-- by a prior preview so the plan cannot drift between preview and apply.

INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level,
     execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('database.query.preview', 1, '预览复杂查询',
     '解析并校验结构化查询 AST，生成参数化 SQL 并执行 EXPLAIN FORMAT=JSON，返回计划哈希与成本摘要，不读取业务数据。',
     'database_table', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"ast":{"type":"object"},"previewHash":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["ast"],"additionalProperties":false}',
     30),
    ('database.query.execute', 1, '执行复杂查询',
     '执行已预览并通过审批的结构化只读查询；重新校验计划哈希与成本门禁，结果受行数、字节数和单元格上限约束。',
     'database_table', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"ast":{"type":"object"},"previewHash":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["ast","previewHash"],"additionalProperties":false}',
     30)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
