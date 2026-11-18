-- Low-destructive ECS lifecycle and governance metadata operations. The
-- Connector fixes the target to the authorized resource and Region; metadata
-- updates receive trusted Warden ownership context from warden-control.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('huawei.ecs.start', 1, '启动 ECS', '启动所选 ECS，并回读实例直到最终状态为 ACTIVE。', 'huawei_ecs_instance', 'high', 'ASYNCHRONOUS', '{"type":"object","additionalProperties":false}', 180),
    ('huawei.ecs.stop', 1, '关闭 ECS', '以 SOFT 模式关闭所选 ECS，并回读实例直到最终状态为 SHUTOFF。', 'huawei_ecs_instance', 'high', 'ASYNCHRONOUS', '{"type":"object","additionalProperties":false}', 180),
    ('huawei.ecs.reboot', 1, '重启 ECS', '以 SOFT 模式重启所选 ECS，并回读实例直到最终状态恢复为 ACTIVE。', 'huawei_ecs_instance', 'high', 'ASYNCHRONOUS', '{"type":"object","additionalProperties":false}', 180),
    ('huawei.ecs.update_metadata', 1, '更新 ECS 治理标签与备注', '更新业务标签和云服务器备注；warden-* 保留键仅从资源当前 Warden 项目归属生成，调用方不可覆盖。', 'huawei_ecs_instance', 'high', 'SYNCHRONOUS',
     '{"type":"object","properties":{"description":{"type":"string","maxLength":85,"description":"业务备注，可传空字符串清除"},"tags":{"type":"object","maxProperties":3,"additionalProperties":{"type":"string","maxLength":43},"description":"最多 3 个业务标签；warden-* 前缀保留"}},"additionalProperties":false,"minProperties":1}', 60),
    ('huawei.ecs.delete', 1, '删除 ECS', '永久删除所选 ECS。审批通过后仍须输入完整 ECS 名称二次确认；可明确选择是否同时删除弹性公网 IP 和数据盘。', 'huawei_ecs_instance', 'critical', 'ASYNCHRONOUS',
     '{"type":"object","required":["confirmServerName"],"properties":{"confirmServerName":{"type":"string","minLength":1,"maxLength":64,"description":"输入完整 ECS 名称确认永久删除"},"deletePublicIP":{"type":"boolean","default":false,"description":"同时删除弹性公网 IP"},"deleteVolumes":{"type":"boolean","default":false,"description":"同时删除数据盘"}},"additionalProperties":false}', 300)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
