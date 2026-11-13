-- Clarify the product boundary between the bounded single-table shortcut and
-- the governed multi-table AST builder. templateId now means the published
-- template UUID; pam-control still accepts the historical template name for
-- compatibility with older Agent clients.

UPDATE action_definitions SET
    display_name='执行多表查询',
    description='构造并执行已预检的多表只读查询；支持同一数据库集成内的 INNER/LEFT JOIN、跨表返回字段与筛选，执行前重新校验资源范围、previewHash 与 EXPLAIN 成本门禁。'
WHERE name='database.query.execute' AND version=1;

UPDATE action_definitions SET
    display_name='查询表数据（单表）',
    description='对选中表执行快捷的结构化只读查询；支持返回字段、AND 筛选、有限排序和最多 100 行，不支持 JOIN。需要多表关联时使用“执行多表查询”。'
WHERE name='database.table.rows.query' AND version=1;

UPDATE action_definitions SET
    description='执行已发布、不可变的查询模板；从模板目录选择 UUID 和版本，仅填写运行参数，控制面注入固定 AST 与治理上下文。',
    parameter_schema='{"type":"object","properties":{"templateId":{"type":"string","minLength":1,"maxLength":128,"description":"已发布查询模板 UUID（兼容历史模板名称）"},"templateVersion":{"type":"integer","minimum":1},"bindings":{"type":"object"}},"required":["templateId","templateVersion"],"additionalProperties":false}'::jsonb
WHERE name='database.query_template.execute' AND version=1;
