-- Huawei Cloud (protocol=huaweicloud) P1: read-only actions for the account
-- overview and ECS/RDS/ELB/OBS resources discovered through an IAM user's
-- AK/SK. The IAM policy on the cloud side is the hard permission ceiling;
-- discovery only surfaces services the account can list, so actions attach
-- only to resources the account can actually reach. Change actions (start/
-- stop/reboot) arrive in P2 behind the approval flow.

INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('huawei.account.get', 1, '查看华为云账号服务权限', '对账号在范围内的服务逐一做只读探测，返回每个服务的可用状态（ACTIVE/UNAVAILABLE/OUT_OF_SCOPE）', 'huawei_cloud_account', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 60),
    ('huawei.ecs.get', 1, '查看 ECS 实例详情', '读取弹性云服务器的完整详情（规格、状态、网络、磁盘挂载等）', 'huawei_ecs_instance', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('huawei.ecs.metrics', 1, '查看 ECS 监控指标', '通过 CES 云监控读取实例指标，默认最近 60 分钟的 cpu_util 平均值', 'huawei_ecs_instance', 'low', 'SYNCHRONOUS', '{"type":"object","properties":{"metricName":{"type":"string","pattern":"^[a-z0-9_]{1,64}$","description":"CES 指标名，默认 cpu_util"},"minutes":{"type":"integer","minimum":5,"maximum":1440,"description":"回看窗口分钟数，默认 60"}},"additionalProperties":false}', 30),
    ('huawei.rds.get', 1, '查看 RDS 实例详情', '读取云数据库实例的详情（引擎版本、状态、规格、端口等）', 'huawei_rds_instance', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('huawei.elb.get', 1, '查看 ELB 负载均衡详情', '读取弹性负载均衡实例的详情（状态、VIP、监听器关联等）', 'huawei_elb_loadbalancer', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('huawei.obs.bucket.get', 1, '查看 OBS 桶用量', '读取对象存储桶的容量与对象数量统计', 'huawei_obs_bucket', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
