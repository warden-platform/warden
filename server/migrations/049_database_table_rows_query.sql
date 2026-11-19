-- Structured, bounded single-table row queries for incident investigation.
-- The Connector validates every identifier against live table metadata and
-- builds parameterized SELECT statements; callers never submit raw SQL.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level,
     execution_mode, parameter_schema, timeout_seconds)
VALUES (
    'database.table.rows.query', 1, '查询表数据',
    '对选中表执行结构化只读查询；仅允许真实字段、AND 条件、有限排序和最多 100 行，不接受原始 SQL、JOIN 或跨表访问。',
    'database_table', 'low', 'SYNCHRONOUS',
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"columns":{"type":"array","minItems":1,"maxItems":20,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":128}},"filters":{"type":"array","maxItems":20,"items":{"type":"object","properties":{"column":{"type":"string","minLength":1,"maxLength":128},"operator":{"type":"string","enum":["eq","ne","gt","gte","lt","lte","like","in","is_null","is_not_null"]},"value":{}},"required":["column","operator"],"additionalProperties":false}},"orderBy":{"type":"array","maxItems":3,"items":{"type":"object","properties":{"column":{"type":"string","minLength":1,"maxLength":128},"direction":{"type":"string","enum":["asc","desc","ASC","DESC"]}},"required":["column","direction"],"additionalProperties":false}},"limit":{"type":"integer","minimum":1,"maximum":100,"default":50},"offset":{"type":"integer","minimum":0,"maximum":10000,"default":0}},"required":["name","namespace","columns"],"additionalProperties":false}',
    30
)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
