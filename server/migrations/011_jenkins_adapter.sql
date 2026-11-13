INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('jenkins.controller.get', 1, '查看 Jenkins 控制器', '读取 Jenkins 版本、模式和静默状态', 'jenkins_controller', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('jenkins.job.get', 1, '查看 Jenkins Job', '读取 Job 状态和最近构建摘要', 'jenkins_job', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('jenkins.job.configure', 1, '配置 Jenkins Job', '结构化修改 Job 描述和静默期；不接受原始 XML 或脚本', 'jenkins_job', 'high', 'SYNCHRONOUS', '{"type":"object","properties":{"description":{"type":"string","maxLength":4000},"quietPeriod":{"type":"integer","minimum":0,"maximum":3600}},"additionalProperties":false,"minProperties":1}', 60),
    ('jenkins.job.enable', 1, '启用 Jenkins Job', '启用指定 Job', 'jenkins_job', 'medium', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('jenkins.job.disable', 1, '禁用 Jenkins Job', '禁用指定 Job', 'jenkins_job', 'high', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('jenkins.job.build', 1, '触发 Jenkins Build', '触发指定 Job，可传递受审计的标量参数', 'jenkins_job', 'medium', 'ASYNCHRONOUS', '{"type":"object","properties":{"parameters":{"type":"object","maxProperties":50,"additionalProperties":{"type":["string","number","integer","boolean"]}}},"additionalProperties":false}', 60),
    ('jenkins.build.get', 1, '查看 Jenkins Build', '读取构建状态和耗时', 'jenkins_build', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('jenkins.build.logs', 1, '查看 Jenkins Build 日志', '读取构建控制台日志，最多 1 MiB', 'jenkins_build', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 60),
    ('jenkins.build.stop', 1, '停止 Jenkins Build', '停止正在执行的构建', 'jenkins_build', 'high', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('jenkins.build.retry', 1, '重试 Jenkins Build', '为原 Job 重新触发一次构建，可传递受审计的标量参数', 'jenkins_build', 'medium', 'ASYNCHRONOUS', '{"type":"object","properties":{"parameters":{"type":"object","maxProperties":50,"additionalProperties":{"type":["string","number","integer","boolean"]}}},"additionalProperties":false}', 60),
    ('jenkins.queue.get', 1, '查看 Jenkins Queue Item', '读取队列项状态', 'jenkins_queue_item', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('jenkins.queue.cancel', 1, '取消 Jenkins Queue Item', '取消排队中的构建', 'jenkins_queue_item', 'high', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('jenkins.node.get', 1, '查看 Jenkins Node', '读取节点在线状态和执行器摘要', 'jenkins_node', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('jenkins.node.set_offline', 1, '设置 Jenkins Node 上下线', '结构化设置节点临时离线状态和原因', 'jenkins_node', 'high', 'SYNCHRONOUS', '{"type":"object","properties":{"offline":{"type":"boolean"},"message":{"type":"string","maxLength":500}},"required":["offline"],"additionalProperties":false}', 30),
    ('jenkins.credential_reference.get', 1, '查看 Jenkins Credential 引用', '仅返回凭据 ID、类型和描述，不返回 Secret', 'jenkins_credential_reference', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
