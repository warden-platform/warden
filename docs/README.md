# HighTower 文档索引

本目录只版本化可复用、已脱敏并且需要随代码演进的项目文档。环境账号、真实地址、凭据、个人文件路径和一次性测试证据不得提交。

## 随代码提交到 Gitee

| 文档 | 用途 | 维护时机 |
|---|---|---|
| [`../README.md`](../README.md) | 产品定位、架构、开发与部署入口 | 功能或架构发生变化时 |
| [`../server/README.md`](../server/README.md) | Go 服务结构、配置与运行方式 | 后端入口或配置变化时 |
| [`../web/README.md`](../web/README.md) | 管理端结构与开发命令 | 前端结构或命令变化时 |
| [`api.md`](api.md) | REST API、认证方式、Scope 和请求示例 | 路由或请求结构变化时 |
| [`provider-request-contract.md`](provider-request-contract.md) | 通用资源只读请求、能力目录和安全策略 | 资源访问契约或治理边界变化时 |
| [`control-plane-endpoint-and-resilience-plan.md`](control-plane-endpoint-and-resilience-plan.md) | 控制面稳定地址和恢复设计 | 方案或实施状态变化时 |
| [`pam-business-architecture-and-roadmap-2026-2027.md`](pam-business-architecture-and-roadmap-2026-2027.md) | 通用插件接入、动态资源审批、业务/技术架构及年度路线图 | 产品定位、优先级或年度目标变化时 |
| [`commit-report-2026-08-19-to-2026-08-21.md`](commit-report-2026-08-19-to-2026-08-21.md) | 2026-08-19 以来的新增特性与 Bug 修复明细 | 阶段性提交范围变化时 |
| [`pam-current-business-and-technical-architecture.md`](pam-current-business-and-technical-architecture.md) | 当前业务架构图、技术架构图和汇报口径 | 核心组件、执行链路或能力边界变化时 |
| [`pam-target-plugin-identity-risk-orchestration-architecture.md`](pam-target-plugin-identity-risk-orchestration-architecture.md) | 插件化、Agent 细粒度身份、动态风险和执行计划编排目标架构 | 目标架构或实施优先级变化时 |
| [`HighTower_PAM_项目分析与商业化报告_优化版.md`](HighTower_PAM_项目分析与商业化报告_优化版.md) | 基于当前代码的项目分析、目标架构、竞品、商业化和 12 个月规划 | 代码基线、产品战略或商业假设变化时 |
| [`e2e/02-test.md`](e2e/02-test.md) | 管理端验收测试矩阵 | 产品行为变化时 |
| [`e2e/03-test-agent.md`](e2e/03-test-agent.md) | 浏览器测试执行规范 | 测试流程变化时 |

## 仅保留在本地

- `cluster-onboarding-walkthrough.md`：真实环境接入记录，含内网地址、个人路径和历史开发凭据示例。
- `e2e/04-test-report.md`：单次环境测试结果，包含实时资源数量、临时状态文件路径和现场观察。
- 截图、浏览器状态、日志和 `/tmp` 下的证据文件。

这些文件由 `.gitignore` 默认排除。需要分享现场材料时，应先脱敏，再放到受控的内部知识库或工单附件，不应直接提交代码仓库。

## 文档安全检查

提交前至少确认：

1. 没有密码、Token、Cookie、私钥、数据库 DSN 或可用的示例密钥。
2. 没有真实内网 IP、用户名、个人绝对路径或 kubeconfig 路径。
3. 命令使用 `<placeholder>`，不使用可直接工作的弱默认凭据。
4. 文档描述与当前代码、迁移编号和页面能力一致。
5. 临时计划和测试报告没有被误加入暂存区。
