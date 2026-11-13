INSERT INTO action_definitions (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds) VALUES
  ('aliyun.sls.account.get', 1, '查看阿里云 SLS Project', '列出当前 Project 的 Logstore', 'aliyun_sls_account', 'low', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name"],"additionalProperties":false}', 30),
  ('aliyun.sls.logs.query', 1, '查询阿里云 SLS 日志', '按 Logstore、任意历史时间范围和查询表达式执行有界日志查询，原始结果仅短期交付', 'aliyun_sls_logstore', 'medium', 'SYNCHRONOUS',
   '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"from":{"type":"integer","minimum":1},"to":{"type":"integer","minimum":1},"query":{"type":"string","maxLength":2048},"line":{"type":"integer","minimum":1,"maximum":500,"default":100}},"required":["name","from","to"],"additionalProperties":false}', 60)
ON CONFLICT (name, version) DO UPDATE SET display_name=EXCLUDED.display_name, description=EXCLUDED.description,
  resource_type=EXCLUDED.resource_type, risk_level=EXCLUDED.risk_level, execution_mode=EXCLUDED.execution_mode,
  parameter_schema=EXCLUDED.parameter_schema, timeout_seconds=EXCLUDED.timeout_seconds;
