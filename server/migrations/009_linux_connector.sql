ALTER TABLE cluster_pairings
    ADD COLUMN IF NOT EXISTS connector_type VARCHAR(30) NOT NULL DEFAULT 'kubernetes',
    ADD COLUMN IF NOT EXISTS available_services TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS machine_metadata JSONB NOT NULL DEFAULT '{}';

INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('linux.host.info', 1, '查看主机信息', '读取操作系统、内核和运行时间', 'linux_host', 'low', 'SYNCHRONOUS', '{"type":"object","properties":{}}', 30),
    ('linux.process.list', 1, '查看进程', '读取主机进程列表', 'linux_host', 'low', 'SYNCHRONOUS', '{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":200,"default":50}}}', 30),
    ('linux.disk.usage', 1, '查看磁盘使用率', '读取文件系统容量与使用率', 'linux_host', 'low', 'SYNCHRONOUS', '{"type":"object","properties":{}}', 30),
    ('systemd.service.status', 1, '查看服务状态', '读取 systemd 服务状态', 'systemd_service', 'low', 'SYNCHRONOUS', '{"type":"object","properties":{}}', 30),
    ('systemd.service.logs', 1, '查看服务日志', '读取 systemd journal 日志', 'systemd_service', 'low', 'SYNCHRONOUS', '{"type":"object","properties":{"lines":{"type":"integer","minimum":1,"maximum":1000,"default":200}}}', 60)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name, description=EXCLUDED.description,
    parameter_schema=EXCLUDED.parameter_schema, timeout_seconds=EXCLUDED.timeout_seconds;
