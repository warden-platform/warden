-- Huawei Cloud SWR Enterprise read-only actions. Discovery creates the
-- instance -> namespace -> repository -> tag hierarchy from the official
-- services/swr/v2 APIs; these definitions expose detail reads for each level.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('huawei.swr.instance.get', 1, '查看 SWR 企业版实例详情',
     '读取华为云 SWR 企业版实例的规格、版本、访问地址、VPC 和运行状态。',
     'huawei_swr_instance', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('huawei.swr.namespace.get', 1, '查看 SWR 命名空间详情',
     '读取 SWR 命名空间的公开属性、镜像仓库数量、扫描配置和漏洞阻断策略。',
     'huawei_swr_namespace', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('huawei.swr.repository.get', 1, '查看 SWR 镜像仓库详情',
     '读取 SWR 镜像仓库的名称空间归属、标签/制品数量、下载次数和更新时间。',
     'huawei_swr_repository', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('huawei.swr.tag.get', 1, '查看 SWR 镜像标签详情',
     '先解析标签对应的制品摘要，再按 digest 读取 SWR 制品详情，包括大小、媒体类型、拉取时间和推送时间。',
     'huawei_swr_tag', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
