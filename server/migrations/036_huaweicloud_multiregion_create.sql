-- Huawei Cloud account-scoped ECS creation. The action targets a regional
-- huawei_cloud_account resource; control injects trusted Warden context and the
-- Connector injects reserved warden-* tags before signing the cloud request.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES (
    'huawei.ecs.create', 1, '创建 ECS 云服务器',
    '在明确选择的 Region 和 Warden 项目中创建 ECS，并自动写入不可覆盖的 warden-* 治理标签。',
    'huawei_cloud_account', 'high', 'ASYNCHRONOUS',
    '{
      "type":"object",
      "required":["projectId","region","name","flavor","imageId","vpcId","subnetId","securityGroupIds"],
      "properties":{
        "projectId":{"type":"string","format":"uuid","description":"Warden 项目"},
        "region":{"type":"string","pattern":"^[a-z]{2}(?:-[a-z]+)+-[0-9]{1,2}$","description":"华为云 Region"},
        "name":{"type":"string","pattern":"^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$","description":"ECS 名称"},
        "description":{"type":"string","maxLength":85,"description":"资源备注"},
        "flavor":{"type":"string","description":"规格 ID"},
        "imageId":{"type":"string","description":"镜像 ID"},
        "vpcId":{"type":"string","description":"VPC ID"},
        "subnetId":{"type":"string","description":"子网 ID"},
        "securityGroupIds":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"string"},"description":"安全组 ID 列表"},
        "systemDiskSize":{"type":"integer","minimum":40,"maximum":1024,"default":40,"description":"系统盘 GiB"},
        "tags":{"type":"object","maxProperties":3,"additionalProperties":{"type":"string"},"description":"最多 3 个业务标签；warden-* 前缀保留"}
      },
      "additionalProperties":false
    }',
    300
)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
