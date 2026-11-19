-- H3C interface mutations are deliberately bounded to discovered interface
-- resources. Every write requires a live preview hash, explicit Integration
-- enablement, approval, Connector change-safety support and post-read checks.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level,
     execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('network.interface.description.preview', 1, '预览接口描述变更',
     '读取接口当前配置并生成绑定当前状态、目标描述和保存选项的预检哈希；不修改设备。',
     'network_interface', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"description":{"type":"string","minLength":1,"maxLength":63},"allowCritical":{"type":"boolean","default":false},"save":{"type":"boolean","default":false}},"required":["description"],"additionalProperties":false}'::jsonb, 60),
    ('network.interface.description.update', 1, '修改接口描述',
     '按已通过的预检修改单个已发现接口的描述；写后重新读取验证，默认不保存启动配置。',
     'network_interface', 'high', 'SYNCHRONOUS',
     '{"type":"object","properties":{"description":{"type":"string","minLength":1,"maxLength":63},"allowCritical":{"type":"boolean","default":false},"save":{"type":"boolean","default":false},"previewHash":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["description","previewHash"],"additionalProperties":false}'::jsonb, 90),
    ('network.interface.access_vlan.preview', 1, '预览 Access VLAN 变更',
     '读取接口当前配置并验证端口类型、保护标记和影响确认；不修改设备。',
     'network_interface', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"vlan":{"type":"integer","minimum":2,"maximum":4093},"allowDisruptive":{"type":"boolean"},"allowCritical":{"type":"boolean","default":false},"save":{"type":"boolean","default":false}},"required":["vlan","allowDisruptive"],"additionalProperties":false}'::jsonb, 60),
    ('network.interface.access_vlan.update', 1, '修改 Access VLAN',
     '按已通过的预检修改单个 Access 端口的 VLAN；需要显式影响确认，写后重新读取验证，默认不保存启动配置。',
     'network_interface', 'high', 'SYNCHRONOUS',
     '{"type":"object","properties":{"vlan":{"type":"integer","minimum":2,"maximum":4093},"allowDisruptive":{"type":"boolean"},"allowCritical":{"type":"boolean","default":false},"save":{"type":"boolean","default":false},"previewHash":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["vlan","allowDisruptive","previewHash"],"additionalProperties":false}'::jsonb, 90),
    ('network.interface.trunk_vlan.preview', 1, '预览 Trunk VLAN 变更',
     '读取接口当前配置并验证 Trunk 类型、VLAN 当前成员关系、保护标记和影响确认；不修改设备。',
     'network_interface', 'low', 'SYNCHRONOUS',
     '{"type":"object","properties":{"operation":{"type":"string","enum":["add","remove"]},"vlan":{"type":"integer","minimum":2,"maximum":4093},"allowDisruptive":{"type":"boolean","default":false},"allowCritical":{"type":"boolean","default":false},"save":{"type":"boolean","default":false}},"required":["operation","vlan"],"additionalProperties":false}'::jsonb, 60),
    ('network.interface.trunk_vlan.add', 1, '允许 Trunk VLAN',
     '按已通过的预检向单个 Trunk 端口增加一个 VLAN；写后重新读取验证，默认不保存启动配置。',
     'network_interface', 'high', 'SYNCHRONOUS',
     '{"type":"object","properties":{"vlan":{"type":"integer","minimum":2,"maximum":4093},"allowCritical":{"type":"boolean","default":false},"save":{"type":"boolean","default":false},"previewHash":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["vlan","previewHash"],"additionalProperties":false}'::jsonb, 90),
    ('network.interface.trunk_vlan.remove', 1, '移除 Trunk VLAN',
     '按已通过的预检从单个 Trunk 端口移除一个 VLAN；需要显式影响确认，写后重新读取验证，默认不保存启动配置。',
     'network_interface', 'high', 'SYNCHRONOUS',
     '{"type":"object","properties":{"vlan":{"type":"integer","minimum":2,"maximum":4093},"allowDisruptive":{"type":"boolean"},"allowCritical":{"type":"boolean","default":false},"save":{"type":"boolean","default":false},"previewHash":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["vlan","allowDisruptive","previewHash"],"additionalProperties":false}'::jsonb, 90)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds,
    updated_at=now();
