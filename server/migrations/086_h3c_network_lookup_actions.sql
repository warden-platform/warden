-- Bounded single-device MAC/ARP lookups. Parameters are validated by the
-- API and again by the Connector; the input is never interpolated into CLI.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level,
     execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('network.mac.lookup', 1, '查询设备 MAC 表',
     '在指定 H3C 设备上按 MAC 地址查询 VLAN 和端口归属；只读且不返回原始 CLI。',
     'network_device', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"mac":{"type":"string","pattern":"^(?:[0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2}$|^[0-9A-Fa-f]{4}[-.]?[0-9A-Fa-f]{4}[-.]?[0-9A-Fa-f]{4}$"}},"required":["mac"],"additionalProperties":false}'::jsonb, 60),
    ('network.arp.lookup', 1, '查询设备 ARP 表',
     '在指定 H3C 设备上按 IPv4 地址查询 MAC、VLAN 和接口归属；只读且不返回原始 CLI。',
     'network_device', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"ip":{"type":"string","pattern":"^(?:25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])(?:\\.(?:25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])){3}$"}},"required":["ip"],"additionalProperties":false}'::jsonb, 60)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds,
    updated_at=now();
