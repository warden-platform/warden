-- First, deliberately read-only H3C Comware actions.  Network changes are
-- introduced separately only after preview/approval/post-check semantics are
-- implemented.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level,
     execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('network.device.get', 1, '获取网络设备信息',
     '读取 H3C Comware 设备型号、软件版本及资源计数。',
     'network_device', 'low', 'SYNCHRONOUS',
     '{"type":"object","additionalProperties":false}'::jsonb, 60),
    ('network.device.audit', 1, '审计网络设备',
     '执行固定只读巡检命令并返回 H3C Comware 设备基本信息、资源计数和已执行检查项；详细指标后续扩展。',
     'network_device', 'low', 'SYNCHRONOUS',
     '{"type":"object","additionalProperties":false}'::jsonb, 120),
    ('network.interface.list', 1, '列出网络接口',
     '读取 H3C Comware 设备接口状态、速率、双工、链路类型、PVID 和描述。',
     'network_device', 'low', 'SYNCHRONOUS',
     '{"type":"object","additionalProperties":false}'::jsonb, 60),
    ('network.vlan.list', 1, '列出网络 VLAN',
     '读取 H3C Comware 设备上的 VLAN 编号。',
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
