-- OBS Object query and lifecycle actions. Object keys are parameter inputs
-- scoped to the authorized bucket resource; the Connector fixes the target to
-- the registered bucket and Region, so Agents cannot address arbitrary OBS
-- endpoints. List and metadata are read-only; delete is a high-risk action
-- requiring an exact objectKey confirmation.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('huawei.obs.object.list', 1, '列出 OBS 对象', '列出所选桶内的对象（前缀、数量限制可选），返回键、大小、最后修改时间和 ETag。', 'huawei_obs_bucket', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"prefix":{"type":"string","maxLength":1024,"description":"对象键前缀筛选"},"maxKeys":{"type":"integer","minimum":1,"maximum":1000,"default":100,"description":"最多返回对象数"}},"additionalProperties":false}', 60),
    ('huawei.obs.object.metadata.get', 1, '查看 OBS 对象元数据', '读取所选桶内指定对象的元数据（大小、内容类型、最后修改时间、ETag），不返回对象内容。', 'huawei_obs_bucket', 'low', 'SYNCHRONOUS',
     '{"type":"object","required":["objectKey"],"properties":{"objectKey":{"type":"string","minLength":1,"maxLength":1024,"description":"对象键"}},"additionalProperties":false}', 30),
    ('huawei.obs.object.delete', 1, '删除 OBS 对象', '永久删除所选桶内指定对象。审批通过后仍须输入完整对象键二次确认。', 'huawei_obs_bucket', 'high', 'SYNCHRONOUS',
     '{"type":"object","required":["objectKey","confirmObjectKey"],"properties":{"objectKey":{"type":"string","minLength":1,"maxLength":1024,"description":"待删除对象键"},"confirmObjectKey":{"type":"string","minLength":1,"maxLength":1024,"description":"输入完整对象键确认永久删除"}},"additionalProperties":false}', 60)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
