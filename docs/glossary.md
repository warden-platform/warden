# 术语表

> **面向**：所有人。用于快速查一个词在 HighTower PAM 里**确切**指什么。
> 本页是参考，只给定义与出处；背景与设计理由见 [concepts.md](concepts.md)、[architecture.md](architecture.md)。

## 身份与权限

| 术语 | 含义 |
|---|---|
| **Principal** | 身份主体。类型有 `user` / `agent_type` / `agent_instance` / `workload`。用户、SSO 账号、Agent 机器账号都是 Principal |
| **内置 admin** | `002_seed.sql` 写入的固定 ID `00000000-0000-0000-0000-000000000003` 的应急超管。特权来自固定 ID 判定，不来自 `metadata.role` |
| **break-glass** | IdP 故障或配置错误时的应急登录通道，即内置 `admin` 密码登录。日常不建议使用 |
| **Tenant / 租户** | 客户 / 组织级数据隔离边界。业务表带 `tenant_id`，值为租户 slug |
| **Tenant slug** | 租户的字符串标识（如 `platform`），是 `tenant_id` 实际存的值，也是 `/tenants/{slug}` 的路径参数 |
| **Platform tenant** | 初始租户 `platform`。平台级接口额外要求身份属于该租户 |
| **Project / 项目** | 租户内部的业务线 / 环境分组，层级在租户之下 |
| **ProjectRole** | 项目成员角色：`PROJECT_ADMIN` / `OPERATOR` / `VIEWER`，与平台角色是两个维度 |
| **Role** | 平台级角色：`user`（默认）/ `tenant_admin` / `platform_admin` |
| **Scope** | 权限点字符串，如 `pam:resources:read`、`pam:approve`、`pam:admin`（通配） |
| **Capability / capabilities** | `/auth/me` 下发的能力清单，由 scopes 规范化而来；`pam:admin` 展开为全量。前端菜单与路由只按它判断 |
| **RequirePlatformTenant** | 平台级接口的租户守卫：身份不属于 `platform` 时返回 403 `PLATFORM_TENANT_REQUIRED` |
| **Agent API Key** | 每个 Agent Principal 独立的 `pam_agk_` 凭据，数据库只存 SHA-256，明文仅创建/轮换时返回一次 |
| **Registration Key** | `pam_rgk_` 前缀的资源注册机器人专用 Key，绑定 PAM API audience，Scope 受限 |
| **Admin API Key** | `PAM_ADMIN_API_KEY`（兼容旧 `PAM_AGENT_API_KEY`），静态引导用超管凭据，仅用于平台初始化与应急 |
| **Delegation Token** | `pam_dtk_` 短期委托令牌，绑定外部上下文、acting project 与归因主体 |
| **OIDC / SSO** | 企业 IdP 单点登录。PAM 作为 RP，授权码 + PKCE S256 |
| **FederatedIdentity** | `(issuer, subject)` → PAM Principal 的映射记录，含展示名、邮箱、头像 |
| **Assertion 登录** | 外部后端用非对称私钥签短期 JWT，PAM 用公钥验签后换取短期 Bearer Token |
| **JIT 建档** | 首次 SSO 登录时自动创建本地账号（默认 `user`），权限后续由管理员指派 |

## 资源与动作

| 术语 | 含义 |
|---|---|
| **Resource / 资源** | 目标系统里可操作的对象：Deployment、systemd 服务、Jenkins Job、数据库表等 |
| **ActionDefinition / 动作** | 版本化结构化动作，如 `deployment.update_image@v2`。带参数 Schema、风险等级、超时 |
| **ResourceAction** | 资源与动作的绑定关系（某资源上允许哪些动作） |
| **Capabilities（Connector）** | Connector 声明自己支持的动作与版本；控制面用 `SupportsAction` 精确匹配 `name@vN` |
| **Connector** | 部署在目标环境、主动出站轮询控制面的执行代理 |
| **Integration** | 挂在 Connector 上的一等公民连接配置（Jenkins / Grafana / DB / 云厂商 / SLS） |
| **Runner** | Connector 内部执行某类目标动作的适配器 |
| **Operator** | `pam-operator`，负责 K8s 集群配对与 Connector 生命周期调谐 |
| **Helper** | `pam-linux-helper`，root 权限的固定动作代理，通过 Unix Socket 提供 Docker/systemd 动作 |
| **Pairing / 配对** | Connector 与控制面建立信任的流程；配对码与 bootstrap token 只存 SHA-256，10 分钟有效 |
| **Task Key** | 每个 Connector 独立的 32 字节任务签名密钥，控制面 AES-GCM 加密存储 |
| **Credential / 凭据** | 目标系统凭据。明文不入库、不返回 Agent、不写普通日志 |
| **Credential Reference** | 目标侧凭据位置的引用（K8s Secret 名或 Connector 固定凭据目录中的文件名） |
| **Project Scope / 项目归属** | 资源、Integration、Connector 上的项目与环境归属，可继承或单资源覆盖 |

## 决策与执行

| 术语 | 含义 |
|---|---|
| **PolicyDecision** | 一次请求不可变的策略结论：`ALLOW` / `DENY` / `REQUIRE_APPROVAL` |
| **Embedded Rego** | 默认内嵌的 OPA 策略，显式动作白名单、默认 DENY |
| **Remote OPA** | `PAM_DECISION_MODE=remote` 时使用的独立策略服务，强制 HTTPS（回环可 HTTP） |
| **Policy Matrix / 策略矩阵** | 管理端可视化规则层，可收紧策略但不能覆盖 Rego 的 DENY |
| **ApprovalRule** | 按风险等级 + 环境 + 优先级决定是否需审批的规则 |
| **AccessRequest** | 人工审批单，状态 `PENDING / APPROVED / REJECTED / EXPIRED / CANCELLED` |
| **AccessGrant** | 一次性、限时、绑定资源/动作/参数哈希的授权，状态 `ACTIVE / USED / REVOKED / EXPIRED` |
| **Execution** | 一次动作的完整状态与结果 |
| **Execution 状态** | `WAITING_APPROVAL / QUEUED / DISPATCHED / RUNNING / SUCCEEDED / FAILED / UNKNOWN / CANCELLING / CANCELLED / EXPIRED` |
| **UNKNOWN** | 结果不确定的终态。只触发只读状态重读，**不自动重放**变更动作 |
| **TaskEnvelope** | 控制面交给 Connector 的签名任务数据结构（HMAC-SHA256），含 Grant、参数哈希、时间窗、TraceID |
| **DispatchToken** | 单次任务领取凭证，防止同一任务被重复执行 |
| **Idempotency-Key** | 创建 Execution 的请求头，按当前 Principal 幂等 |
| **Outbox** | 事务性事件表，供后续异步发布；Publisher 后台循环尚未实现 |
| **AuditEvent** | append-only 审计记录，`action` 为 PostgreSQL 枚举 |

## 部署与运维

| 术语 | 含义 |
|---|---|
| **pam-control** | 控制面主进程，REST API，默认 `:8080` |
| **`*_FILE` Secret** | 文件型 Secret 约定：同名明文变量与 `_FILE` 不可同时设置，文件内容 trim 后非空 |
| **Migration / 迁移** | `server/migrations/*.sql` 按编号顺序执行的 SQL；启动不自动执行 |
| **Release migration manifest** | `deploy/release/migrations.txt`，发布流水线重放的幂等迁移清单（编号 ≥ 035 必须登记） |
| **`PAM_COOKIE_SECURE`** | 会话 Cookie 的 Secure 标记，默认 `true`；仅本地 HTTP 开发可设 `false` |
| **`PAM_PUBLIC_CONTROL_URL`** | 对外可访问控制面地址，用于安装命令、audience 推导与审批链接 |
| **`/livez` `/readyz`** | 控制面存活 / 就绪探针；`/api/v1/health` 为兼容端点。前端 nginx 另有 `/healthz` |
| **Admin Web** | React/Vite 管理端，开发默认 `:4173`，容器 nginx 暴露 `:8080` |
| **RP** | OIDC 里的 Relying Party，此处指 PAM 控制面 |
| **IdP** | Identity Provider；本地联调常用 `unauth`，上游可接飞书等企业身份源 |
| **PKCE S256** | 授权码流防截获机制，PAM 强制要求，缺失 verifier 时拒绝降级 |

## 索引

按主题深入：[concepts.md](concepts.md) · [architecture.md](architecture.md) · [authorization.md](authorization.md) · [api.md](api.md) · [configuration.md](configuration.md) · [actions.md](actions.md)
