# 变更记录

> 本文件记录值得让部署者知道的阶段性变更，格式参考 [Keep a Changelog](https://keepachangelog.com/)。
> 具体部署版本请以镜像 tag、`deploy/release/migrations.txt` 与提交历史为准，更细的提交粒度用 `git log` 查看。

## [未发布]

### 变更（已合并 master，尚未部署生产）

- **dispatch_token 哈希落库**：任务分发令牌明文仅一次性下发给 Connector，数据库只存
  sha256 哈希（迁移 `112_dispatch_token_hash.sql`），防止库只读者回放令牌伪造执行结果；
  同步移除 `WARDEN_ENABLE_LEGACY_DISPATCH_RESULT` 旧兼容路径。
- **钉钉卡片流自动重连**：连接失败 5 秒退避重试，避免一次网络抖动后审批卡片回调永久失联。
- **RLS 运行时探针**：启动断言主池角色受 RLS 约束且特权 DSN 已配，配置漂移即拒绝启动
  （不再静默失效）。
- **admin API key 审计溯源**：break-glass key 认证标记 `auth_source=admin_api_key`、
  登录会话标 `admin_session`，审计可区分来源。
- **请求日志补状态码 + X-Request-ID**、**前端 4 处加载失败不再静默吞空态**、
  **资源同步部分失败跳过 orphan**、**任务签名 claim 前校验**。

### 部署免构建

- 生产 Compose 示例与 Helm Chart 默认镜像改为阿里云 ACR 发布镜像
  （`registry.cn-hangzhou.aliyuncs.com/warden-stable/{warden,warden-web,warden-operator,warden-connector}`），
  `scripts/publish-acr.sh` 相应增补 operator/connector 两镜像；官方仓库公开、
  匿名可拉取，私有/自建仓库才需 `docker login`（Helm 配 `imagePullSecret`），
  自建仍为可选路径。

### 下次发版待办（逐项勾销）

- [x] 生产部署切换到「从 ACR 拉公开镜像」（已完成，生产 compose 已指向 `warden:v0.4.1`，服务器不再 `docker build`）。
- [x] 迁移 `112` 与上述「变更」已随 v0.4.1 部署生产（2026-11-23）。
- [x] P2 已清（2026-11-23）：死代码清理（UpdateStatus + 死枚举标注）、Heartbeat 停用/隔离态不复活、幂等键并发 23505 幂等返回、审批通知并发限流、前端卫生（状态枚举 union / 终态时长 / 去中文控制流键 / 心跳魔数命名化）。
- [ ] 剩余 P2 已评估登记为已知限制（见 architecture 债务表，不硬改）：登录限流单实例内存态（多副本需共享存储）、child/绑定表无 tenant_id（需设计）、在途配额 TOCTOU（护栏非精确）。
- [ ] 长期：迁移 106 第 8 步无条件撤销会话（`migrate-release` 每次重放即全站登出）；执行列表项目过滤 SQL 下推（已定维持现状）。

## [0.4.0] - 2026-11-19

首个正式版本：多租户身份与授权闭环、钉钉租户级群隔离、
工程化护栏（CI / 测试 / 迁移工具）与一轮全面的质量加固。

### 新增

- **企业 OIDC / SSO**：授权码流 + PKCE S256、state/nonce 校验、联邦身份自动建档（稳定内部标识 + IdP 展示名 / 邮箱 / 头像）、RP-initiated logout。未配置 `WARDEN_OIDC_ISSUER` 时行为与旧版完全一致。
- **账号与权限**：`GET /api/v1/accounts`、`PUT /api/v1/accounts/{id}/role`；普通账号管理入口只能指派 `user` / `tenant_admin`，内置应急超管不可改，每个租户至少保留一个租户管理员。变更与审计同事务，立即撤销目标会话；非内置 `platform_admin` 的 JWT 兼容识别仍作为已知限制保留。
- **身份与组织闭环**：SSO 邮箱域名路由、`unaffiliated` 待分配状态、管理员分配、邀请与接受、账号移出/恢复及组织视野切换。
- **AI 只读助手**：`POST /api/v1/ai/chat` 使用 OpenAI 兼容模型和 SSE 流式回答；资源、执行、审计三个只读工具继承当前身份的权限与数据范围，不创建业务 Execution 或变更资源。
- **统一权限模型**：角色 / Scope / `capabilities` 集中在 `server/internal/control/authorization.go`；`/auth/me` 下发能力清单，后端守卫与前端菜单读同一份名单；平台级接口增加 `RequirePlatformTenant`。
- **文档体系**：按教程 / 操作指南 / 参考 / 解释四类重构，根 `README.md` 改为门户，新增 [docs/README.md](docs/README.md) 文档地图及 8 篇专题文档。
- **工程化护栏**：最小 CI（`.github/workflows/ci.yml`，含 PostgreSQL service 常跑 DB 门控集成测试）；前端 vitest 纯逻辑单测进 `make verify` 与 CI；`make migrate` / `make migrate-release` 收敛手工 psql 循环；生产编排脱敏示例 `deploy/docker-compose.prod.example.yaml` + `deploy/nginx.prod.example.conf`。
- **演示数据工具**：`scripts/demo/seed_demo_data.py` 加 `--confirm-demo-environment` 生产护栏，`--reset` 按外键依赖完整清灌；演示通道能力名对齐真实连接器注册清单（可用动作 0 → 47）；`keepalive.sh` 心跳保活（生产建议托管为 systemd 服务）。

### 修复

- 首次 SSO 登录建档 500：`ProvisionIdentity` 中 `json.Marshal` 覆盖 `pgx.ErrNoRows` 导致空 UUID。
- IdP 的 userinfo 空实现把 id_token 的 `sub` 覆盖为空：Warden 侧对空 subject 返回 `OIDC_INVALID_SUBJECT`，避免所有用户被合并成同一身份。
- `localhost` / `127.0.0.1` 混用导致 state Cookie 丢失：登录与登出改用服务端下发的规范绝对 URL；失败回调不再提前清除 Cookie，可重试。
- 顶栏显示 `federated:<hash>`：改为优先展示 `display_name` / 邮箱 / 头像，内部标识仅用于登录与审计。
- 角色变更审计静默丢失：`audit_events.action` 是 PostgreSQL 枚举，补 `097_account_role_audit.sql` 迁移并登记发布清单。
- 钉钉审批多租户断裂与跨租户泄漏：钉钉链路原先使用空租户上下文，多租户化后免登/列表/决策全部失效、审计落空租户。现改为租户级群隔离——应用凭据部署级共享（与 OIDC 单 IdP 同构），每租户在渠道设置绑定自己的审批群（迁移 `107_dingtalk_tenant_group.sql`，一群一租户部分唯一索引）；酷应用按群解析租户并冻结进会话，卡片回调按请求锚定租户且校验回调群匹配，卡片投递只进请求租户的群，legacy Webhook 仅承载 platform 租户内容。未绑群租户明确返回 `DINGTALK_GROUP_NOT_BOUND`；无租户 claim 的旧钉钉会话失效需重登。
- env-only 部署未设置 `WARDEN_ADMIN_USERNAME` 时回退内置 `admin`，避免旧部署升级后因新增必填项无法启动；管理员密码仍为必需项。
- 列表完整性与错误语义：7 处 List 补齐 `rows.Err()`（审计/审批列表不再静默截断）；GetExecution/CancelExecution 区分 DB 错误（500）与不存在（404）并对齐执行视野三级阶梯；过期审批 worker 区分竞态与真实错误并记日志；内部错误（pgx/文件系统路径）不再直返客户端；OIDC 回调 error 参数截断清洗。
- 前端健壮性：ErrorBoundary（chunk 加载失败/渲染异常不再整站白屏）；Toast 错误不再显示成功图标（aria-live 播报）；字体自托管（私有化部署不依赖 Google Fonts，CSP 同步收紧）；执行状态逻辑改键原始枚举（未知状态可见，不再伪装排队中）。
- 内部品牌 Multica 残留清零（外部身份元数据改 `external_*` 通用键，旧键回退兼容）。

### 变更

- **破坏性：内部标识符统一为 warden 体系（迁移 106）**。权限 scope、API Key 前缀、Connector capability、资源标签、Cookie 与 Rego 包名统一为 warden 命名。旧格式明文 Key 解析层不再接受，存量旧 Key 统一吊销需重新签发；全部会话撤销，所有用户需重新登录。全新安装经 002–105 已直接产出 warden 标识符，106 为空操作。
- 生产 `WARDEN_OIDC_DEFAULT_ROLE` 保持 `user`；配置 `platform_admin` 会让服务启动失败。普通账号管理入口只开放 `user` / `tenant_admin` 指派；非内置 `platform_admin` 的 JWT 兼容识别尚需后续收敛。
- 未唯一匹配邮箱域名的新 SSO 身份默认进入 `unaffiliated`，不再自动落入 `platform`；只有显式配置 `WARDEN_OIDC_DEFAULT_TENANT_ID` 才启用默认租户兜底。
- 普通配置、外部凭据与内部密钥分三层管理：`config.yaml` + `.env` + `var/warden/secrets.json`，内部密钥由显式初始化命令生成。
- 前端菜单、路由守卫与顶栏不再按 `is_platform_admin` / 角色名判断，统一使用 `canUser(user, scope)` 消费 `capabilities`。
- 生产部署的 IdP 升级为支持 OIDC 发现、JWKS 与 RP client 注册的版本；RP 回调与登出回跳地址改为规范 HTTPS 绝对地址。
- 前端数据加载全站迁移 `useApiQuery`（防抖 / 请求序号守卫 / 稳定空引用），react-hooks 五条规则升为 error 门禁；列表 enrichment 与项目过滤 N+1 批量化；列表分页统一 limit/offset（执行列表过滤后分页不再缩水）；Outbox 死代码清除（写入钩子保留，投递未实现）。
- 迁移 `108_foreign_key_indexes.sql` 补齐 11 个外键列索引；依赖卫生（移除未用 clsx、vite 归 devDependencies、删 bun.lock、babel 7/8 peer 冲突根修）。

### 已知限制

- 执行列表的项目过滤在 Go 层分块扫描（分页语义正确，大偏移量会重复扫描）；SQL 下推属授权可见性语义重写，列入后续设计项。
- 静态 Admin API Key 不支持在线轮换；无行级安全（RLS）；非内置 `platform_admin` 的 JWT 兼容识别待收敛。详见 [SECURITY.md](SECURITY.md)。
- Outbox 仅有事务写入钩子，Publisher/消费者未实现（默认关闭）。

### 部署注意

- 发布迁移清单当前覆盖至 `108_foreign_key_indexes.sql`。从旧版升级必须按 `deploy/release/migrations.txt` 执行发布迁移（或 `make migrate-release`），包括账号角色审计、单超管数据迁移、待分配身份、邀请、AI 会话表、内部标识符统一（106）、钉钉租户群绑定列与外键补索引；不要绕过发布流程直接在已有数据库上重放全部历史迁移（001–034 不幂等，仅限全新空库 `make migrate`）。执行 106 前务必备份：旧 Key 吊销与会话撤销不可逆，Agent 需换发新 Key。启用钉钉审批的部署升级后需为各业务租户绑定审批群（platform 租户沿用部署级群配置）。
- 角色变更后目标账号被强制重新登录；JWT 里的 scopes 是登录时冻结的，刷新页面不会生效。
- 升级前先做数据库备份，并保留上一版镜像 ID、前端 `dist` 与 IdP 侧密钥。流程见 [日常运维](docs/guides.md#operations)。
- Helm Chart 默认镜像为占位值（`ghcr.io/your-org/...`），部署前必须 `--set` 为自建镜像或按 [接入指南](docs/guides.md#onboarding) 构建；`controlPlaneURL` 与镜像仓库均有必填校验。
