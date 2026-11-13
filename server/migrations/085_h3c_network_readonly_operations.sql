-- Additional H3C read-only operations. These actions deliberately expose
-- derived summaries only; the Connector never returns raw CLI/configuration.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level,
     execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('network.device.health', 1, '检查网络设备健康状态',
     '汇总设备、接口链路和 VLAN 的只读健康指标；不修改设备。',
     'network_device', 'low', 'SYNCHRONOUS',
     '{"type":"object","additionalProperties":false}'::jsonb, 60),
    ('network.topology.lldp', 1, '查看 LLDP 拓扑邻居',
     '读取 H3C LLDP 邻居摘要并返回邻居数量和观测时间；不返回原始配置。',
     'network_device', 'low', 'SYNCHRONOUS',
     '{"type":"object","additionalProperties":false}'::jsonb, 60),
    ('network.link_aggregation.status', 1, '查看链路聚合状态',
     '读取 H3C BAGG/LACP 状态并返回聚合与成员状态摘要；不修改聚合。',
     'network_device', 'low', 'SYNCHRONOUS',
     '{"type":"object","additionalProperties":false}'::jsonb, 60),
    ('network.stp.status', 1, '查看 STP 状态',
     '读取 H3C STP 端口状态摘要；不修改生成树或保护策略。',
     'network_device', 'low', 'SYNCHRONOUS',
     '{"type":"object","additionalProperties":false}'::jsonb, 60),
    ('network.port.health', 1, '检查端口健康状态',
     '基于接口只读信息汇总 Up、Down、管理关闭和速率分布；不修改端口。',
     'network_device', 'low', 'SYNCHRONOUS',
     '{"type":"object","additionalProperties":false}'::jsonb, 60)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds,
    updated_at=now();
