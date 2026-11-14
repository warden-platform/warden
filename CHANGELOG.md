# 变更记录

> 本文件记录值得让部署者知道的阶段性变更，格式参考 [Keep a Changelog](https://keepachangelog.com/)。
> HighTower PAM **目前尚无正式 Release / tag**，因此只有「未发布」一节；具体部署版本请以镜像 tag、`deploy/release/migrations.txt` 与提交历史为准。更细的提交粒度用 `git log` 查看。

## [未发布]

### 新增

- **企业 OIDC / SSO**：授权码流 + PKCE S256、state/nonce 校验、联邦身份自动建档（稳定内部标识 + IdP 展示名 / 邮箱 / 头像）、RP-initiated logout。未配置 `PAM_OIDC_ISSUER` 时行为与旧版完全一致。
- **账号与权限**：`GET /api/v1/accounts`、`PUT /api/v1/accounts/{id}/role`；页面可给 SSO 用户指派 `user` / `tenant_admin` / `platform_admin`。内置应急超管不可改、保留至少一个 SSO 超管、变更与审计同事务、立即撤销目标会话、账号 SQL 带 `tenant_id` 过滤。
- **统一权限模型**：角色 / Scope / `capabilities` 集中在 `server/internal/control/authorization.go`；`/auth/me` 下发能力清单，后端守卫与前端菜单读同一份名单；平台级接口增加 `RequirePlatformTenant`。
- **文档体系**：按教程 / 操作指南 / 参考 / 解释四类重构，根 `README.md` 改为门户，新增 [docs/README.md](docs/README.md) 文档地图及 12 篇专题文档。

### 修复

- 首次 SSO 登录建档 500：`ProvisionIdentity` 中 `json.Marshal` 覆盖 `pgx.ErrNoRows` 导致空 UUID。
- IdP 的 userinfo 空实现把 id_token 的 `sub` 覆盖为空：PAM 侧对空 subject 返回 `OIDC_INVALID_SUBJECT`，避免所有用户被合并成同一身份。
- `localhost` / `127.0.0.1` 混用导致 state Cookie 丢失：登录与登出改用服务端下发的规范绝对 URL；失败回调不再提前清除 Cookie，可重试。
- 顶栏显示 `federated:<hash>`：改为优先展示 `display_name` / 邮箱 / 头像，内部标识仅用于登录与审计。
- 角色变更审计静默丢失：`audit_events.action` 是 PostgreSQL 枚举，补 `097_account_role_audit.sql` 迁移并登记发布清单。
- 旧文档与代码不一致：`/auth/logout` 权限描述、Principal 接口的 Scope 标注、OIDC 默认角色可选值、服务端入口清单等。

### 变更

- 生产 `PAM_OIDC_DEFAULT_ROLE` 固定为 `user`；配置 `platform_admin` 会让服务启动失败，超级管理员只能由管理员在页面手工指派。
- 前端菜单、路由守卫与顶栏不再按 `is_platform_admin` / 角色名判断，统一使用 `canUser(user, scope)` 消费 `capabilities`。
- 生产部署的 IdP 升级为支持 OIDC 发现、JWKS 与 RP client 注册的版本；RP 回调与登出回跳地址改为规范 HTTPS 绝对地址。

### 部署注意

- 新增迁移 `097_account_role_audit.sql` 必须随发布执行并登记 `deploy/release/migrations.txt`。
- 角色变更后目标账号被强制重新登录；JWT 里的 scopes 是登录时冻结的，刷新页面不会生效。
- 升级前先做数据库备份，并保留上一版镜像 ID、前端 `dist` 与 IdP 侧密钥。流程见 [docs/operations.md](docs/operations.md)。

---

## 历史资料

仓库内 `docs/` 下还保留若干阶段性报告、架构方案与 e2e 记录（如 `commit-report-2026-08-19-to-2026-08-21.md`）。它们描述的是当时基线，**默认不纳入版本控制**，也不代表当前实现状态；判断现状请以代码和 [docs/roadmap.md](docs/roadmap.md) 为准。
