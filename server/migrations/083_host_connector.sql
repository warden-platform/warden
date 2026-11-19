-- Host connector command action.
INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('host.command.run', 1, '执行受控主机命令', '按管理员白名单直接执行程序及参数，不经过 Shell', 'host_machine', 'high', 'ASYNCHRONOUS', '{"type":"object","properties":{"program":{"type":"string","pattern":"^[A-Za-z0-9._-]{1,128}$"},"args":{"type":"array","maxItems":64,"items":{"type":"string","maxLength":4096}}},"required":["program"],"additionalProperties":false}', 120)
ON CONFLICT (name, version) DO UPDATE SET parameter_schema=EXCLUDED.parameter_schema, timeout_seconds=EXCLUDED.timeout_seconds;
