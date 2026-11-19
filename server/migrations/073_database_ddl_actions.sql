-- Governed DDL actions (create database / create table). Each has a dry-run
-- .preview (compiles the single DDL statement + fingerprint, never executes)
-- and an idempotent .create (requires the previewHash, replays a definition
-- fingerprint, and refuses when an object exists with a different definition).
-- Rollback is MANUAL: no automatic DROP is ever issued (§14).

INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level,
     execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('database.schema.create.preview', 1, '预览创建 Database',
     '校验 Database 名称、字符集与排序规则，编译单条 CREATE DATABASE 并返回定义指纹，不执行。',
     'database_instance', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":64},"characterSet":{"type":"string","maxLength":64},"collation":{"type":"string","maxLength":64},"previewExecutionId":{"type":"string"}},"required":["name"],"additionalProperties":false}',
     60),
    ('database.schema.create', 1, '创建 Database',
     '执行已预览的 CREATE DATABASE；对象不存在则创建，已存在且定义一致则幂等成功，定义冲突则拒绝。',
     'database_instance', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":64},"characterSet":{"type":"string","maxLength":64},"collation":{"type":"string","maxLength":64},"previewHash":{"type":"string","pattern":"^[a-f0-9]{64}$"},"previewExecutionId":{"type":"string"}},"required":["name","previewHash"],"additionalProperties":false}',
     120),
    ('database.table.create.preview', 1, '预览创建 Table',
     '校验列类型、主键、唯一索引与普通索引，编译单条 CREATE TABLE 并返回定义指纹，不执行。',
     'database_schema', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":64},"columns":{"type":"array","minItems":1,"maxItems":100},"primaryKey":{"type":"array"},"uniqueConstraints":{"type":"array"},"indexes":{"type":"array"},"engine":{"type":"string"},"characterSet":{"type":"string"},"collation":{"type":"string"},"previewExecutionId":{"type":"string"}},"required":["name","columns"],"additionalProperties":false}',
     60),
    ('database.table.create', 1, '创建 Table',
     '执行已预览的 CREATE TABLE；表不存在则创建，已存在且定义一致则幂等成功，定义冲突则拒绝。',
     'database_schema', 'high', 'ASYNCHRONOUS',
     '{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":64},"columns":{"type":"array","minItems":1,"maxItems":100},"primaryKey":{"type":"array"},"uniqueConstraints":{"type":"array"},"indexes":{"type":"array"},"engine":{"type":"string"},"characterSet":{"type":"string"},"collation":{"type":"string"},"previewHash":{"type":"string","pattern":"^[a-f0-9]{64}$"},"previewExecutionId":{"type":"string"}},"required":["name","columns","previewHash"],"additionalProperties":false}',
     120)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
