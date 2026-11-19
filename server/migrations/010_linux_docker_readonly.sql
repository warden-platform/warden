INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('docker.container.list', 1, '列出 Docker 容器', '读取主机当前运行的 Docker 容器', 'linux_host', 'low', 'SYNCHRONOUS', '{"type":"object","properties":{}}', 30),
    ('docker.container.inspect', 1, '查看容器详情', '读取 Docker 容器状态和镜像信息', 'docker_container', 'low', 'SYNCHRONOUS', '{"type":"object","properties":{}}', 30),
    ('docker.container.logs', 1, '查看容器日志', '读取 Docker 容器末尾日志', 'docker_container', 'low', 'SYNCHRONOUS', '{"type":"object","properties":{"lines":{"type":"integer","minimum":1,"maximum":1000,"default":200}}}', 60)
ON CONFLICT (name, version) DO UPDATE SET display_name=EXCLUDED.display_name, description=EXCLUDED.description,
    parameter_schema=EXCLUDED.parameter_schema, timeout_seconds=EXCLUDED.timeout_seconds;
