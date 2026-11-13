-- Move Huawei LTS query from a synthetic regional account target to the
-- concrete discovered log stream. The connector derives group/stream IDs from
-- the authorized resource identity, so callers no longer submit raw IDs.
UPDATE action_definitions
SET display_name = '查询华为云 LTS 日志',
    description = '查询所选 LTS 日志流的任意历史时间范围，原始结果仅短期交付',
    resource_type = 'huawei_lts_log_stream',
    parameter_schema = '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"startTime":{"type":"integer","minimum":1},"endTime":{"type":"integer","minimum":1},"keywords":{"type":"string","maxLength":1024},"limit":{"type":"integer","minimum":1,"maximum":500,"default":100}},"required":["name","startTime","endTime"],"additionalProperties":false}'::jsonb,
    timeout_seconds = 60
WHERE name = 'huawei.lts.logs.query' AND version = 1;
