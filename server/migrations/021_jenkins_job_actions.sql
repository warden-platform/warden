-- jenkins.job.config.get: read-only, returns the job's config.xml so agents can
-- study existing pipeline definitions (inline script or SCM block) before
-- creating new jobs.
-- jenkins.job.create: high-risk, requires approval. Creates a Pipeline job in
-- either inline (CpsFlowDefinition) or scm (CpsScmFlowDefinition) mode. Targets
-- the jenkins_controller resource since the job does not exist yet.

INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('jenkins.job.config.get', 1, '查看 Jenkins Job 配置', '读取 Job 的 config.xml，包含 Pipeline 脚本或 SCM 配置块；凭据仅返回 ID 引用，不返回 Secret', 'jenkins_job', 'low', 'SYNCHRONOUS', '{"type":"object","additionalProperties":false}', 30),
    ('jenkins.job.create', 1, '创建 Jenkins Job', '创建 Pipeline Job。inline 模式接收 pipelineScript；scm 模式接收仓库地址+凭据+分支+Jenkinsfile 路径，运行时从 Git 加载脚本', 'jenkins_controller', 'high', 'SYNCHRONOUS', '{"type":"object","properties":{"name":{"type":"string","pattern":"^[A-Za-z0-9][A-Za-z0-9 _.\\\\-]{0,254}(/[A-Za-z0-9][A-Za-z0-9 _.\\\\-]{0,254})*$","description":"Job 全名，支持 folder/sub/job 形式"},"mode":{"type":"string","enum":["inline","scm"]},"pipelineScript":{"type":"string","maxLength":65536},"scm":{"type":"object","properties":{"repoUrl":{"type":"string","maxLength":2048},"credentialsId":{"type":"string","maxLength":128},"branch":{"type":"string","maxLength":256},"scriptPath":{"type":"string","maxLength":256}},"required":["repoUrl"],"additionalProperties":false},"description":{"type":"string","maxLength":4000}},"required":["name","mode"],"additionalProperties":false,"allOf":[{"if":{"properties":{"mode":{"const":"inline"}}},"then":{"required":["pipelineScript"]}},{"if":{"properties":{"mode":{"const":"scm"}}},"then":{"required":["scm"]}}]}', 60)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name,
    description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type,
    risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode,
    parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
