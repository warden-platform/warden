INSERT INTO action_definitions (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds) VALUES
  ('huawei.lts.logs.query', 1, '查询华为云 LTS 日志', '按日志组、日志流、任意历史时间范围和关键词执行有界 LTS 日志查询，原始结果仅短期交付', 'huawei_cloud_account', 'medium', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"logGroupId":{"type":"string","pattern":"^[a-zA-Z0-9_.-]{1,128}$"},"logStreamId":{"type":"string","pattern":"^[a-zA-Z0-9_.-]{1,128}$"},"startTime":{"type":"integer","minimum":1},"endTime":{"type":"integer","minimum":1},"keywords":{"type":"string","maxLength":1024},"limit":{"type":"integer","minimum":1,"maximum":500,"default":100}},"required":["name","logGroupId","logStreamId","startTime","endTime"],"additionalProperties":false}', 60)
ON CONFLICT (name, version) DO UPDATE SET display_name=EXCLUDED.display_name, description=EXCLUDED.description,
  resource_type=EXCLUDED.resource_type, risk_level=EXCLUDED.risk_level, execution_mode=EXCLUDED.execution_mode,
  parameter_schema=EXCLUDED.parameter_schema, timeout_seconds=EXCLUDED.timeout_seconds;
