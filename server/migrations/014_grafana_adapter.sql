INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('grafana.instance.get', 1, '查看 Grafana 实例', '读取 Grafana 版本与健康状态', 'grafana_instance', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('grafana.folder.list', 1, '列出 Grafana Folder', '列出仪表盘文件夹', 'grafana_instance', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('grafana.folder.get', 1, '查看 Grafana Folder', '读取文件夹详情', 'grafana_folder', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('grafana.folder.create', 1, '创建 Grafana Folder', '创建仪表盘文件夹', 'grafana_instance', 'medium', 'SYNCHRONOUS', '{"type":"object","properties":{"title":{"type":"string","maxLength":255},"uid":{"type":"string","pattern":"^[a-zA-Z0-9_-]{1,40}$"}},"required":["title"],"additionalProperties":false}', 60),
    ('grafana.dashboard.list', 1, '列出 Grafana Dashboard', '列出仪表盘', 'grafana_instance', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('grafana.dashboard.get', 1, '查看 Grafana Dashboard', '读取仪表盘模型', 'grafana_dashboard', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('grafana.dashboard.export', 1, '导出 Grafana Dashboard', '导出仪表盘 JSON，最多 1 MiB', 'grafana_dashboard', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 60),
    ('grafana.dashboard.create', 1, '创建 Grafana Dashboard', '创建仪表盘，需提供 dashboard 对象', 'grafana_instance', 'high', 'SYNCHRONOUS', '{"type":"object","properties":{"dashboard":{"type":"object"},"folderUid":{"type":"string","pattern":"^[a-zA-Z0-9_-]{1,40}$"},"message":{"type":"string","maxLength":500}},"required":["dashboard"],"additionalProperties":false}', 60),
    ('grafana.dashboard.update', 1, '更新 Grafana Dashboard', '更新仪表盘，UID 固定为资源 UID', 'grafana_dashboard', 'high', 'SYNCHRONOUS', '{"type":"object","properties":{"dashboard":{"type":"object"},"folderUid":{"type":"string","pattern":"^[a-zA-Z0-9_-]{1,40}$"},"message":{"type":"string","maxLength":500},"overwrite":{"type":"boolean"}},"required":["dashboard"],"additionalProperties":false}', 60),
    ('grafana.dashboard.delete', 1, '删除 Grafana Dashboard', '删除指定仪表盘', 'grafana_dashboard', 'high', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('grafana.datasource.list', 1, '列出 Grafana Datasource', '列出数据源', 'grafana_instance', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('grafana.datasource.query', 1, '查询 Grafana Datasource', '对数据源执行查询，datasource.uid 固定为资源 UID', 'grafana_datasource', 'low', 'SYNCHRONOUS', '{"type":"object","properties":{"queries":{"type":"array","maxItems":20,"items":{"type":"object"}},"from":{"type":"string"},"to":{"type":"string"}},"required":["queries"],"additionalProperties":false}', 60)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
