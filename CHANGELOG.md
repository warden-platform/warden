# 变更记录

> 本文件记录值得让部署者知道的阶段性变更，格式参考 [Keep a Changelog](https://keepachangelog.com/)。
> Warden 改名后尚未单独发布正式版本；仓库保留改名前的历史 tag，因此本文件用「未发布」记录当前阶段变更。具体部署版本请以镜像 tag、`deploy/release/migrations.txt` 与提交历史为准，更细的提交粒度用 `git log` 查看。

## [未发布]

### 新增

- **企业 OIDC / SSO**：授权码流 + PKCE S256、state/nonce 校验、联邦身份自动建档（稳定内部标识 + IdP 展示名 / 邮箱 / 头像）、RP-initiated logout。未配置 `WARDEN_OIDC_ISSUER` 时行为与旧版完全一致。
- **账号与权限**：`GET /api/v1/accounts`、`PUT /api/v1/accounts/{id}/role`；普通账号管理入口只能指派 `user` / `tenant_admin`，内置应急超管不可改，每个租户至少保留一个租户管理员。变更与审计同事务，立即撤销目标会话；非内置 `platform_admin` 的 JWT 兼容识别仍作为已知限制保留。
- **身份与组织闭环**：SSO 邮箱域名路由、`unaffiliated` 待分配状态、管理员分配、邀请与接受、账号移出/恢复及组织视野切换。
- **AI 只读助手**：`POST /api/v1/ai/chat` 使用 OpenAI 兼容模型和 SSE 流式回答；资源、执行、审计三个只读工具继承当前身份的权限与数据范围，不创建业务 Execution 或变更资源。
- **统一权限模型**：角色 / Scope / `capabilities` 集中在 `server/internal/control/authorization.go`；`/auth/me` 下发能力清单，后端守卫与前端菜单读同一份名单；平台级接口增加 `RequirePlatformTenant`。
- **文档体系**：按教程 / 操作指南 / 参考 / 解释四类重构，根 `README.md` 改为门户，新增 [docs/README.md](docs/README.md) 文档地图及 12 篇专题文档。

### 修复

- 首次 SSO 登录建档 500：`ProvisionIdentity` 中 `json.Marshal` 覆盖 `pgx.ErrNoRows` 导致空 UUID。
- IdP 的 userinfo 空实现把 id_token 的 `sub` 覆盖为空：Warden 侧对空 subject 返回 `OIDC_INVALID_SUBJECT`，避免所有用户被合并成同一身份。
- `localhost` / `127.0.0.1` 混用导致 state Cookie 丢失：登录与登出改用服务端下发的规范绝对 URL；失败回调不再提前清除 Cookie，可重试。
- 顶栏显示 `federated:<hash>`：改为优先展示 `display_name` / 邮箱 / 头像，内部标识仅用于登录与审计。
- 角色变更审计静默丢失：`audit_events.action` 是 PostgreSQL 枚举，补 `097_account_role_audit.sql` 迁移并登记发布清单。
- 旧文档与代码不一致：`/auth/logout` 权限描述、Principal 接口的 Scope 标注、OIDC 默认角色可选值、服务端入口清单等。
- 钉钉审批多租户断裂与跨租户泄漏：钉钉链路原先使用空租户上下文，多租户化后免登/列表/决策全部失效、审计落空租户。现改为租户级群隔离——应用凭据部署级共享（与 OIDC 单 IdP 同构），每租户在渠道设置绑定自己的审批群（迁移 `107_dingtalk_tenant_group.sql`，一群一租户部分唯一索引）；酷应用按群解析租户并冻结进会话，卡片回调按请求锚定租户且校验回调群匹配，卡片投递只进请求租户的群，legacy Webhook 仅承载 platform 租户内容。未绑群租户明确返回 `DINGTALK_GROUP_NOT_BOUND`；无租户 claim 的旧钉钉会话失效需重登。
- env-only 部署未设置 `WARDEN_ADMIN_USERNAME` 时回退内置 `admin`，避免旧部署升级后因新增必填项无法启动；管理员密码仍为必需项。

### 变更

- **破坏性：内部标识符 pam → warden 全量切换（迁移 `106_pam_to_warden_identifiers.sql`）**。权限 scope 前缀 `pam:` → `warden:`、API Key 前缀 `pam_agk_/pam_rgk_/pam_dtk_` → `warden_*`、Connector capability `pam.change-safety` → `warden.change-safety`、资源标签 `pam-*` → `warden-*`、Cookie 与 Rego 包名同步切换。旧格式明文 Key 解析层不再接受，存量旧 Key 统一吊销需重新签发；全部会话撤销，所有用户需重新登录。全新安装经 002–105 已直接产出 warden 标识符，106 为空操作。
- 生产 `WARDEN_OIDC_DEFAULT_ROLE` 保持 `user`；配置 `platform_admin` 会让服务启动失败。普通账号管理入口只开放 `user` / `tenant_admin` 指派；非内置 `platform_admin` 的 JWT 兼容识别尚需后续收敛。
- 未唯一匹配邮箱域名的新 SSO 身份默认进入 `unaffiliated`，不再自动落入 `platform`；只有显式配置 `WARDEN_OIDC_DEFAULT_TENANT_ID` 才启用默认租户兜底。
- 普通配置、外部凭据与内部密钥分三层管理：`config.yaml` + `.env` + `var/warden/secrets.json`，内部密钥由显式初始化命令生成。
- 前端菜单、路由守卫与顶栏不再按 `is_platform_admin` / 角色名判断，统一使用 `canUser(user, scope)` 消费 `capabilities`。
- 生产部署的 IdP 升级为支持 OIDC 发现、JWKS 与 RP client 注册的版本；RP 回调与登出回跳地址改为规范 HTTPS 绝对地址。

### 部署注意

- 发布迁移清单当前覆盖至 `107_dingtalk_tenant_group.sql`。从旧版升级必须按 `deploy/release/migrations.txt` 执行发布迁移，包括账号角色审计、单超管数据迁移、待分配身份、邀请、AI 会话表、pam → warden 标识符切换与钉钉租户群绑定列；不要绕过发布流程直接在已有数据库上重放全部历史迁移。执行 106 前务必备份：旧 Key 吊销与会话撤销不可逆，Agent 需换发新 Key。启用钉钉审批的部署升级后需为各业务租户绑定审批群（platform 租户沿用部署级群配置）。
- 角色变更后目标账号被强制重新登录；JWT 里的 scopes 是登录时冻结的，刷新页面不会生效。
- 升级前先做数据库备份，并保留上一版镜像 ID、前端 `dist` 与 IdP 侧密钥。流程见 [日常运维](docs/guides.md#operations)。
