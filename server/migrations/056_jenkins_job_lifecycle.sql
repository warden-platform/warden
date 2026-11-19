-- Split Jenkins Pipeline lifecycle operations into explicit create, update,
-- lightweight configure, and destructive delete actions.

INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('jenkins.job.create', 1, '创建 Jenkins Job', '仅创建新的 Pipeline Job；同名 Job 返回 JENKINS_JOB_CONFLICT，不会隐式更新。inline 模式接收 Pipeline Script，scm 模式接收仓库、Credential ID、分支和 Jenkinsfile 路径', 'jenkins_controller', 'high', 'SYNCHRONOUS', '{"type":"object","properties":{"name":{"type":"string","pattern":"^[A-Za-z0-9][A-Za-z0-9 _.\\\\-]{0,254}(/[A-Za-z0-9][A-Za-z0-9 _.\\\\-]{0,254})*$","description":"Job 全名，支持 folder/sub/job 形式"},"mode":{"type":"string","enum":["inline","scm"]},"pipelineScript":{"type":"string","maxLength":65536},"scm":{"type":"object","properties":{"repoUrl":{"type":"string","maxLength":2048},"credentialsId":{"type":"string","maxLength":128,"description":"仅接受 Jenkins Credential ID，不接受 Secret"},"branch":{"type":"string","maxLength":256},"scriptPath":{"type":"string","maxLength":256}},"required":["repoUrl"],"additionalProperties":false},"description":{"type":"string","maxLength":4000},"quietPeriod":{"type":"integer","minimum":0,"maximum":3600}},"required":["name","mode"],"additionalProperties":false,"allOf":[{"if":{"properties":{"mode":{"const":"inline"}}},"then":{"required":["pipelineScript"]}},{"if":{"properties":{"mode":{"const":"scm"}}},"then":{"required":["scm"]}}]}', 60),
    ('jenkins.job.update', 1, '更新 Jenkins Pipeline', '更新现有 Job 的完整 Pipeline 定义；Job 名称固定，不支持 rename/move。支持 inline Pipeline Script 或 SCM 仓库、Credential ID、分支和 Jenkinsfile 路径', 'jenkins_job', 'high', 'SYNCHRONOUS', '{"type":"object","properties":{"mode":{"type":"string","enum":["inline","scm"]},"pipelineScript":{"type":"string","maxLength":65536},"scm":{"type":"object","properties":{"repoUrl":{"type":"string","maxLength":2048},"credentialsId":{"type":"string","maxLength":128,"description":"仅接受 Jenkins Credential ID，不接受 Secret"},"branch":{"type":"string","maxLength":256},"scriptPath":{"type":"string","maxLength":256}},"required":["repoUrl"],"additionalProperties":false},"description":{"type":"string","maxLength":4000},"quietPeriod":{"type":"integer","minimum":0,"maximum":3600}},"required":["mode"],"additionalProperties":false,"allOf":[{"if":{"properties":{"mode":{"const":"inline"}}},"then":{"required":["pipelineScript"]}},{"if":{"properties":{"mode":{"const":"scm"}}},"then":{"required":["scm"]}}]}', 60),
    ('jenkins.job.configure', 1, '轻量配置 Jenkins Job', '仅结构化修改 Job 描述和静默期；不修改 Pipeline Script、SCM 配置或 Job 名称', 'jenkins_job', 'high', 'SYNCHRONOUS', '{"type":"object","properties":{"description":{"type":"string","maxLength":4000},"quietPeriod":{"type":"integer","minimum":0,"maximum":3600}},"additionalProperties":false,"minProperties":1}', 60),
    ('jenkins.job.delete', 1, '删除 Jenkins Job', '永久删除现有 Job。审批通过后仍须输入完整 Job 名称进行二次确认', 'jenkins_job', 'critical', 'SYNCHRONOUS', '{"type":"object","properties":{"confirmJobName":{"type":"string","minLength":1,"maxLength":1024,"description":"输入完整 Job 名称以确认永久删除"}},"required":["confirmJobName"],"additionalProperties":false}', 60)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
