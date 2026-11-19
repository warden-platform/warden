首个正式版本。完整变更记录见 CHANGELOG.md [0.4.0] 条目。

## 亮点

- **多租户身份与授权闭环**：企业 OIDC/SSO（PKCE S256）、RAM 式 `用户名@租户` 登录、邮箱域名路由、邀请/分配/移出、统一 capabilities 权限模型、AI 只读助手。
- **钉钉租户级群隔离**（迁移 107）：应用凭据部署级共享，数据/卡片/队列/审计按租户严格隔离；未绑群 fail-closed。
- **工程化护栏**：CI（含 PostgreSQL 门控集成测试）、vitest 单测、react-hooks error 门禁、make migrate/migrate-release、生产编排脱敏示例。
- **质量加固**：rows.Err 完整性、404/500 语义、错误脱敏、ErrorBoundary、字体自托管、N+1 批量化、分页统一、useApiQuery 全站迁移、FK 补索引（迁移 108）。

## 破坏性变更（升级必读）

- 迁移 106：内部标识符统一为 warden 体系（scope、API Key 前缀、capability、资源标签、Cookie 与 Rego 包名）。旧格式 Key 吊销需重签发，全部会话撤销需重登。**执行前务必备份**。
- 升级按 `deploy/release/migrations.txt`（或 `make migrate-release`）执行至 108；001–034 不幂等，仅限全新空库。
- 启用钉钉审批的部署需为各业务租户绑定审批群。

## 已知限制

- 执行列表项目过滤为 Go 层分块扫描（SQL 下推列入后续设计项）；静态 Admin Key 不支持在线轮换；无 RLS；Outbox 仅写入钩子。详见 SECURITY.md。

## 部署

- 源码构建与部署：docs/guides.md；生产编排示例：deploy/docker-compose.prod.example.yaml。
- Helm Chart 0.7.0（appVersion 0.4.0）：默认镜像为占位值，部署前必须 --set 自建镜像（模板有必填校验）。
- 在线演示：https://warden.squawksync.cloud（演示租户 acme / demo 账号见 README）。
