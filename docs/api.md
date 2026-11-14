# REST API 参考

> **面向**：前后端开发者与集成方。用于查接口、认证方式、权限守卫、请求体与错误码。
> 这是参考手册；上手见 [quickstart.md](quickstart.md)，概念见 [concepts.md](concepts.md)，权限规则见 [authorization.md](authorization.md)，配置见 [configuration.md](configuration.md)。
> 文档地图见 [README.md](README.md)。

本文档以 `server/internal/control/handler.go` 的 `RegisterRoutes` 为准。**路由、权限守卫与代码冲突时以代码为准并顺手修正本文档。** API 前缀为 `/api/v1`；健康检查和下载接口除外。

## 认证模型

| 类型 | 使用方 | 认证方式 |
|---|---|---|
| Public | 健康检查、管理员登录、安装包下载、配对启动 | 无通用身份认证；配对后续请求使用短期配对凭据 |
| Admin session | 管理 Web | `POST /auth/login` 设置 HttpOnly Cookie；浏览器请求携带 `credentials: include` |
| OIDC session | 企业 IdP 登录的 Web 用户 | 授权码 + PKCE S256；回调校验 state/nonce 后复用同一份 `pam_session` Cookie |
| Agent API Key | 外部 Agent | `Authorization: Bearer <agent-api-key>`，按 Key Scope 授权 |
| Registration Key | Multica 资源注册机器人 | `Authorization: Bearer <pam_rgk_...>`；绑定指定 PAM API audience，仅开放注册白名单 |
| Connector signature | Connector Runtime | 每 Connector 独立任务 Key签名；覆盖时间戳、nonce、方法、路径和请求体 |

当前支持共享控制面的多租户模式。业务对象按 tenant 隔离，tenant 从登录用户、API Key 或 Connector 凭据推导，客户端不能通过请求体选择或切换 tenant。平台管理员可以创建 tenant 和首个租户管理员；初始迁移数据属于 `platform` tenant。

用于外部编排平台集成的 Principal 可以在 `metadata.project_ids` 中声明最多 100 个 PAM Project ID。字段一旦存在，项目列表、资源列表、资源统计、单资源动作查询、诊断上下文和 Execution 创建都会在服务端强制限制到这些项目；它是现有项目成员权限之上的额外上限，不会绕过项目 RBAC，空数组表示不允许访问任何项目或资源。Jenkins Job 的额外 `consumer-projects` 关系允许消费项目读取和触发该 Job，但不授予配置、禁用、删除或通用 Provider 写权限。未声明该字段的既有 Principal 保持原有租户内访问行为。V0.1 的企业级租户隔离通过每个外部企业/工作区独立部署 PAM 实例实现。

### Agent Scope

| Scope | 能力 |
|---|---|
| `pam:resources:read` | 查询 Principal 项目范围内的项目、资源、动作及资源允许的动作 |
| `pam:executions:create` | 创建受策略控制的 Execution |
| `pam:executions:read:self` | 查询当前 Principal 自己的 Execution |
| `pam:executions:cancel:self` | 取消当前 Principal 自己的未完成 Execution |
| `pam:approve` | 查询和处理访问审批 |
| `pam:audit:read` | 查询全局审计事件 |
| `pam:credentials:manage` | 管理 Agent Principal 和 API Key |
| `pam:registration:read` | 查询 Connector、Project、Integration 和 Integration 项目范围（资源注册用途） |
| `pam:registration:write` | 创建/更新 Project、Integration 和 Integration 项目范围 |
| `pam:tenant:admin` | 管理当前认证租户的 Principal、Connector、Project、Integration、策略、通知和查询模板 |
| `pam:admin` | 平台管理员通配权限，包括租户生命周期和配额管理 |

## 通用约定

- 请求和响应使用 `application/json`，下载接口除外。
- 时间使用 RFC 3339。
- 创建 Execution 建议提供 `Idempotency-Key` 请求头；幂等范围为当前 Principal。
- API Key明文只在创建或轮换响应中返回一次，数据库不保存明文。
- 删除 Principal 是逻辑禁用，同时撤销其有效 Key。
- 列表无数据时返回 `[]`，不返回 `null`。

错误响应：

```json
{
  "error": "RESOURCE_NOT_FOUND",
  "message": "资源不存在"
}
```

常见状态码：`400` 请求不合法，`401` 未认证或凭据失效，`403` Scope不足，`404` 对象不存在或不属于当前 Agent，`409` 状态冲突，`500` 服务端错误，`503` 依赖未就绪。

## Public 与认证接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/livez` | 进程存活检查 |
| GET | `/readyz` | 数据库就绪检查 |
| GET | `/api/v1/health` | 与 readiness 等价的兼容端点 |
| POST | `/api/v1/auth/login` | 管理员密码登录并设置会话 Cookie |
| GET | `/api/v1/auth/me` | 查询当前登录身份、租户、角色、Scope、`capabilities`、展示名/邮箱/头像、登录来源与会话过期时间 |
| POST | `/api/v1/auth/logout` | 撤销当前会话并清除 Cookie；**只需已认证，无额外 Scope 包装**（SSO 用户请改用 `/auth/oidc/logout` 以同时终止 IdP 会话） |
| PUT | `/api/v1/auth/password` | 登录用户修改自己的密码；成功后撤销该用户的全部会话并清除当前 Cookie |
| GET | `/api/v1/auth/oidc/config` | 返回 `enabled`、`login_url`、`logout_url`（未启用时仅 `enabled:false`） |
| GET | `/api/v1/auth/oidc/start` | 生成 state/nonce/PKCE verifier（HttpOnly Cookie）并 302 到 IdP authorize 端点 |
| GET | `/api/v1/auth/oidc/callback` | 校验 state、nonce、PKCE 与 id_token，联邦建档后签发 `pam_session` 并 302 回控制台 |
| GET | `/api/v1/auth/oidc/logout` | RP-initiated logout：撤销本地会话并 302 到 IdP `end_session_endpoint` |

登录请求：

```json
{
  "username": "admin",
  "password": "<admin-password>"
}
```

登录成功响应中的 `token` 为兼容字段，浏览器认证以 HttpOnly Cookie 为准，不应把 JWT 保存到 localStorage。

租户管理员登录时在请求体中增加 `tenant` slug；平台管理员及迁移后的 `platform` 管理员可省略。`/auth/me`
返回 `tenant`、`role`、`scopes`、`capabilities`、`is_platform_admin`、`expires_at`、`display_name`、`email`、`avatar_url` 和 `auth_source`。**Web 应只用 `capabilities` 决定菜单和操作权限**：它由服务端把 scopes 规范化而来，`pam:admin` 会展开为全量能力点，因此后端 `RequireScope` 守卫与前端菜单读的是同一份名单；`role` / `is_platform_admin` 仅用于展示。联邦用户保留稳定的内部标识 `name`（`federated:<hash>`）用于登录、会话与审计，UI 一律展示 `display_name`。

### OIDC 单点登录

启用条件与环境变量见 `README.md` 的「OIDC/SSO 配置」。要点：

- 三个端点均为 Public；`login_url` / `logout_url` 由服务端按注册的 `redirect_uri` 推导，浏览器必须使用这两个绝对地址发起跳转。host-only 的 state/nonce cookie 要求登录与回调同源，混用 `localhost` 与 `127.0.0.1` 会返回 `INVALID_OIDC_STATE`。
- 授权码流强制 PKCE S256；缺失 `oidc_verifier` cookie 返回 `OIDC_PKCE_MISSING`，不会降级为无 PKCE 兑换。失败的回调不清除 state cookie，允许重试。
- 首次登录按 `(issuer, subject)` 建立 `federated_identities` 与 `principals` 记录；缺少 `sub` 返回 `OIDC_INVALID_SUBJECT`，避免所有用户被合并为同一身份。后续登录刷新展示名、邮箱、头像，但保留管理员在 PAM 中调整过的 `role`。
- 默认角色由 `PAM_OIDC_DEFAULT_ROLE` 决定，取值只允许 `user`（默认）/ `tenant_admin`；**配置 `platform_admin` 会导致启动失败**，非法值同样拒绝启动。生产环境保持最小权限，超级管理员只能通过账号接口手工指派。
- 联邦用户登出走 `/auth/oidc/logout`，同时终止 IdP 会话；普通登出仍用 `/auth/logout`。

### 账号与权限管理

以下接口需同时满足 `pam:admin` 与 `RequirePlatformTenant`（身份必须属于 `platform` 租户），用于给 SSO 用户指派角色：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/accounts` | 列出**当前租户**的登录账号：内部标识、IdP 展示名/邮箱/头像、来源（`oidc` / `local`）、角色、状态、租户、最近登录、`is_builtin_admin` / `is_self` |
| PUT | `/api/v1/accounts/{id}/role` | 请求体 `{"role":"user|tenant_admin|platform_admin"}`；返回新角色、原角色与撤销的会话数 |

账号列表、目标查询、管理员计数与更新 SQL 均带 `tenant_id` 过滤，跨租户目标返回 `ACCOUNT_NOT_FOUND`（404）。角色变更会立即 `UPDATE admin_sessions SET revoked_at=now()`，被改的账号需要重新登录才能拿到新的 JWT scope；审计事件 `account.role_updated` 与角色变更在**同一事务**内提交，审计写入失败则整个变更回滚。约束：内置应急超管 `00000000-…-0003` 不可修改（`BUILTIN_ADMIN_PROTECTED`）、不能给自己降权（`CANNOT_DEMOTE_SELF`）、必须保留至少一个非内置 `platform_admin`（`LAST_PLATFORM_ADMIN`）、非法角色返回 `INVALID_ACCOUNT_ROLE`、非平台租户返回 `PLATFORM_TENANT_REQUIRED`（403）。

`PAM_OIDC_DEFAULT_ROLE` 只允许 `user` / `tenant_admin`；配置 `platform_admin` 会导致启动失败——超级管理员只能由管理员在本接口手工指派。完整权限模型见 [`authorization.md`](authorization.md)。

## 平台租户管理

以下接口需同时满足 `pam:admin` 与 `RequirePlatformTenant`（仅 `platform` 租户）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/tenants` | 查询租户列表，包含配额和 Principal、Connector、Project、在途任务、管理员使用量 |
| GET | `/api/v1/tenants/{slug}` | 查询单个租户及其实时配额使用量 |
| POST | `/api/v1/tenants/bootstrap` | 原子创建租户、默认项目、配额和首个租户管理员 |
| GET | `/api/v1/tenants/{slug}/admins` | 查询租户管理员 |
| POST | `/api/v1/tenants/{slug}/admins` | 为有效租户增加管理员 |
| PUT | `/api/v1/tenants/{slug}/quota` | 更新租户配额 |
| POST | `/api/v1/tenants/{slug}/disable` | 停用租户及其 Principal、Connector；初始 `platform` 租户受保护 |

`POST /api/v1/tenants/bootstrap` 请求示例：

```json
{
  "slug": "acme",
  "name": "Acme Technology",
  "adminName": "acme-admin",
  "adminPassword": "<initial-password>",
  "quota": {
    "maxPrincipals": 1000,
    "maxConnectors": 100,
    "maxConcurrentTasks": 100,
    "maxRequestsPerMinute": 600,
    "auditRetentionDays": 365
  }
}
```

## 资源与动作

| 方法 | 路径 | Scope | 说明 |
|---|---|---|---|
| GET | `/api/v1/resources` | `pam:resources:read` | 查询资源；支持 `availableForExecution=true` 和 `projectId=<pam-project-id>`，可执行资源同样返回有效项目、环境及各自来源 |
| GET | `/api/v1/resources/stats` | `pam:resources:read` | 查询当前、活跃、异常和历史资源聚合统计 |
| GET | `/api/v1/actions` | `pam:resources:read` | 查询动作定义 |
| GET | `/api/v1/resources/{id}/actions` | `pam:resources:read` | 查询指定资源可执行的动作及参数 Schema |
| PUT | `/api/v1/resources/{id}/project` | 租户管理员 | 为单个资源覆盖项目归属 |
| DELETE | `/api/v1/resources/{id}/project` | 租户管理员 | 删除项目覆盖，恢复自动继承 |
| PUT | `/api/v1/resources/{id}/environment` | 租户管理员 | 为单个资源覆盖环境；支持 `development`、`testing`、`production` |
| DELETE | `/api/v1/resources/{id}/environment` | 租户管理员 | 删除环境覆盖，恢复自动继承 |

资源统计中 `current` 不包含 `ORPHANED`，`abnormal` 仅统计当前资源中的非 ACTIVE 状态，`history` 对应已从目标环境消失的 ORPHANED 记录：

```json
{
  "total": 2531,
  "current": 2463,
  "active": 2463,
  "abnormal": 0,
  "history": 68,
  "byType": {},
  "byStatus": {"ACTIVE": 2463, "ORPHANED": 68}
}
```

## Execution

| 方法 | 路径 | Scope | 说明 |
|---|---|---|---|
| POST | `/api/v1/executions` | `pam:executions:create` | 创建受策略约束的执行 |
| GET | `/api/v1/executions` | `pam:executions:read:self` | 查询执行；Agent只能看到自己的记录，支持 `projectId=<pam-project-id>` 按资源项目过滤 |
| GET | `/api/v1/executions/{id}` | `pam:executions:read:self` | 查询执行详情 |
| POST | `/api/v1/executions/{id}/cancel` | `pam:executions:cancel:self` | 取消尚未完成的执行 |

创建执行示例：

```http
POST /api/v1/executions
Authorization: Bearer <agent-api-key>
Idempotency-Key: <unique-key>
Content-Type: application/json
```

```json
{
  "taskId": "agent-task-001",
  "externalContext": {
    "source": "federated",
    "workspaceId": "workspace-prod",
    "projectId": "external-project-payments",
    "agentId": "agent-release-01",
    "taskId": "agent-task-001",
    "issueId": "issue-4821",
    "originatorUserId": "user-1001",
    "accountableUserId": "user-1001",
    "originatorSource": "human",
    "delegatedFromTaskId": ""
  },
  "resourceId": "<resource-id>",
  "action": "deployment.get",
  "actionVersion": 1,
  "parameters": {
    "name": "example",
    "namespace": "default"
  },
  "reason": "读取部署状态"
}
```

`externalContext` 是外部编排平台的结构化归因信息，当前支持 `source`、
`workspaceId`、`projectId`、`agentId`、`taskId`、`issueId`、
`originatorUserId`、`accountableUserId`、`originatorSource` 和
`delegatedFromTaskId`。`source` 和至少一个关联标识必填；各标识只接受有限长度的
安全字符。PAM 将其原样保存在 Execution、审批请求和相关审计事件中，用于追踪
“哪个 Agent、哪个任务、代表谁发起”，但不会把这些字段当作授权依据，也不会因为
请求体中的用户 ID 放宽 Principal、Tenant 或项目成员校验。若同时提供顶层 `taskId`
和 `externalContext.taskId`，二者必须一致；仅提供后者时会兼容填入 Execution 的
顶层任务字段。重试时若同一个 Principal 使用相同幂等键但外部上下文不同，PAM 会返回
`IDEMPOTENCY_CONFLICT`。

服务端根据策略返回 `WAITING_APPROVAL`、`QUEUED` 或明确失败状态。调用方应保存返回的 `execution_id` 并通过查询接口跟踪终态。

风险为 `low` 的查询动作保持免审批。生产环境变更具有不可放宽的审批下限：策略矩阵可以进一步拒绝，但不能把生产变更降为自动执行。变更只会下发给同时具备动作 capability 和 `pam.change-safety@v1` 的 Connector。Connector 在变更前读取目标状态摘要，成功后重新读取验证；结果中的 `_pam_safety` 包含前后状态、验证状态和 `MANUAL`/`NOT_SUPPORTED` 回滚语义。超时或连接中断产生 `UNKNOWN` 时，Connector 会重新读取目标状态并明确禁止自动重试。

Kubernetes 保留 Deployment、StatefulSet、CronJob 等高频结构化动作，并增加 `kubernetes.resource.get/list/preview/create/update/delete` 通用资源动作。通用动作只接受固定白名单 GVK：ConfigMap、Service、Deployment、StatefulSet、DaemonSet、Job、CronJob、Ingress、NetworkPolicy 和 PodDisruptionBudget；Secret、RBAC、CRD、Webhook、Node 及其他未列出的类型默认拒绝。list/create 绑定已授权 Namespace PAM 资源，get/update/delete 绑定已发现的具体 PAM 资源，不能通过参数切换 Connector、Namespace、Kind 或 Name。

create/update 在实际写入前必须先调用 `kubernetes.resource.preview` 完成 server-side dry-run；预检返回净化后的 `before`、`after` 与 `previewHash`，写请求会重跑 dry-run 并要求哈希完全一致。update 需要读取动作返回的 `expectedResourceVersion`，delete 同时要求 `expectedResourceVersion`、精确的 `Kind/Namespace/Name` 二次确认和 `Background`/`Foreground`/`Orphan` 传播策略。create 使用 Execution ID 注解实现确定性幂等，Manifest 限制为 1 MiB，`status` 与 `managedFields` 被移除，密码、Token、私钥、client secret、Authorization 等敏感字段会被拒绝并在结果中净化。只读与预检动作免审批；生产环境写操作强制审批。超时或连接中断进入 `UNKNOWN` 后仅重新读取目标状态，禁止自动重放。

专用工作负载动作仍使用 `expectedGeneration` 与实时 `resourceVersion` 保护并发更新；镜像动作固定真实容器名并受镜像仓库白名单约束，CronJob 手动触发根据 Execution ID 生成确定性 Job 名。开发/测试环境写操作可按策略直接执行，生产环境强制进入审批。

## 审批与审计

| 方法 | 路径 | Scope | 说明 |
|---|---|---|---|
| GET | `/api/v1/access-requests` | `pam:approve` | 查询审批，可用 `status` 过滤 |
| POST | `/api/v1/access-requests/{id}/approve` | `pam:approve` | 批准请求 |
| POST | `/api/v1/access-requests/{id}/reject` | `pam:approve` | 拒绝请求 |
| GET | `/api/v1/audit-events` | `pam:audit:read` | 查询审计事件；支持 `action`、`limit`（默认 500，最大 1000）和 `offset` |
| GET | `/api/v1/approval-rules` | 租户管理员 | 查询审批规则 |
| PUT | `/api/v1/approval-rules/{id}` | 租户管理员 | 更新是否审批、优先级或说明 |
| GET | `/api/v1/policy-rules` | 租户管理员 | 查询可视化策略矩阵规则、命中统计、冲突与遮蔽关系 |
| POST | `/api/v1/policy-rules` | 租户管理员 | 新建项目/资源/动作/环境/风险/身份规则 |
| PUT | `/api/v1/policy-rules/{id}` | 租户管理员 | 完整更新策略规则或启停规则 |
| DELETE | `/api/v1/policy-rules/{id}` | 租户管理员 | 删除策略规则 |
| POST | `/api/v1/policy-rules/{id}/copy` | 租户管理员 | 复制为默认停用的新规则 |
| POST | `/api/v1/policy-rules/simulate` | 租户管理员 | 模拟规则命中和 Rego + 矩阵最终决策 |
| GET | `/api/v1/notification-channels/dingtalk` | 租户管理员 | 查询钉钉渠道启用状态、通知范围及 Secret 配置状态，不返回凭据 |
| PUT | `/api/v1/notification-channels/dingtalk` | 租户管理员 | 更新渠道名称、启用状态和审批事件范围 |
| POST | `/api/v1/notification-channels/dingtalk/test` | 租户管理员 | 发送脱敏的钉钉连通性测试消息 |
| POST | `/api/v1/dingtalk/auth` | 公开、同源 | 使用 `auth_code`、`corp_id` 和 `open_conversation_id` 完成酷应用免登；仅接受已配置审批群的成员，无需预先绑定，审批人钉钉身份自动写入审计 |
| GET | `/api/v1/dingtalk/access-requests` | 钉钉酷应用会话 | 查询待审批请求及其资源、动作和风险上下文 |
| POST | `/api/v1/dingtalk/access-requests/{id}/approve` | 钉钉酷应用会话 | 在酷应用内批准请求 |
| POST | `/api/v1/dingtalk/access-requests/{id}/reject` | 钉钉酷应用会话 | 在酷应用内拒绝请求 |
| POST | `/api/v1/dingtalk/logout` | 钉钉酷应用会话 | 清除酷应用审批会话 |

审批或拒绝请求体可包含业务原因；具体字段以对应页面和 Handler 校验为准。生产集成应始终提供可审计原因。

策略矩阵支持 `ALLOW`、`REQUIRE_APPROVAL`、`DENY`。Rego `DENY` 是不可绕过的安全底线；
矩阵可以把 Rego `ALLOW` 收紧为审批/拒绝，也可以在非生产环境把 `REQUIRE_APPROVAL` 放宽为自动放行；生产变更始终至少需要审批。
规则按优先级降序匹配，同优先级选择项目、资源、动作、环境、风险、身份条件更精确的规则。

钉钉互动卡片需要企业内部应用的 `PAM_DINGTALK_APP_KEY_FILE`、
`PAM_DINGTALK_APP_SECRET_FILE`、`PAM_DINGTALK_CARD_TEMPLATE_ID_FILE` 和
`PAM_DINGTALK_OPEN_CONVERSATION_ID_FILE`、`PAM_DINGTALK_ROBOT_CODE_FILE`。Robot Code
需要独立配置，实际值允许与 Client ID/AppKey 相同。控制面通过官方 Stream SDK 接收卡片按钮回调，
不需要公网回调 URL。`PAM_DINGTALK_WEBHOOK_FILE` 与 `PAM_DINGTALK_SECRET_FILE`
仅用于可选的 Markdown 兜底通知。
互动卡片根据回调 User ID 调用钉钉 `/topapi/v2/user/get` 查询处理人姓名，应用需具备成员信息读权限 `qyapi_get_member`；查询失败时降级记录 User ID，不阻断审批。显式绑定仅供兼容酷应用免登使用。
Web、互动卡片和兼容的酷应用页面复用同一个审批事务；数据库行锁确保并发操作只产生一个结果。
AppSecret、AccessToken、免登码和会话 JWT 不写入数据库、审计或 API 响应。Webhook 通知是独立可选能力，发送失败写入审计但不回滚审批事务。

## Agent Principal 与 API Key

以下路由在 `handler.go` 中由 `RequireTenantAdmin` 保护（等效于 `pam:tenant:admin` 或通配的 `pam:admin`），不是 `pam:credentials:manage`。

| 方法 | 路径 | 守卫 | 说明 |
|---|---|---|---|
| GET | `/api/v1/principals?kind=agent_instance` | `RequireTenantAdmin` | 查询 Agent Principal |
| POST | `/api/v1/principals` | `RequireTenantAdmin` | 创建 Agent Principal；请求体中的 `metadata.role` 会被剥离，无法借此写入角色 |
| DELETE | `/api/v1/principals/{id}` | `RequireTenantAdmin` | 禁用 Principal 并撤销 Key |
| GET | `/api/v1/principals/{id}/api-keys` | `RequireTenantAdmin` | 查询 Key 元数据，不返回明文或 Hash |
| POST | `/api/v1/principals/{id}/api-keys` | `RequireTenantAdmin` | 创建只显示一次的 Key |
| POST | `/api/v1/principals/{id}/registration-api-keys` | `RequireTenantAdmin` | 为 `workload` Principal 创建固定 Scope、绑定 audience、默认 30 天的注册专用 Key |
| POST | `/api/v1/api-keys/{id}/revoke` | `RequireTenantAdmin` | 立即撤销 Key |
| POST | `/api/v1/api-keys/{id}/rotate` | `RequireTenantAdmin` | 创建新 Key 并撤销旧 Key |

创建 Principal：

```json
{
  "name": "agent-production-01",
  "metadata": {
    "multica_agent_id": "agent-production-01",
    "workspace_id": "<workspace-id>"
  }
}
```

创建资源注册机器人 Principal 时显式使用 `workload`：

```json
{
  "name": "PAM注册机器人",
  "kind": "workload",
  "metadata": {
    "platform": "multica",
    "purpose": "pam_resource_registration",
    "multica_agent_id": "<agent-id>"
  }
}
```

创建 Key：

```json
{
  "name": "multica-agent",
  "scopes": [
    "pam:resources:read",
    "pam:executions:create",
    "pam:executions:read:self",
    "pam:executions:cancel:self"
  ],
  "expiresAt": "2026-12-31T16:00:00Z"
}
```

创建 Registration Key；`audience` 可省略，服务端会绑定 `PAM_REGISTRATION_AUDIENCE`，或由
`PAM_PUBLIC_CONTROL_URL` 推导出的 `<control-url>/api/v1`：

```json
{
  "name": "pam-resource-registration",
  "audience": "https://pam-admin.example.com/api/v1",
  "expiresAt": "2026-10-01T00:00:00Z"
}
```

Registration Key 使用独立的 `pam_rgk_` 前缀，固定拥有 `pam:registration:read` 和
`pam:registration:write`，不能申请 `pam:admin`。它不能创建 Execution、处理审批、修改策略、
删除 Connector/Project/Integration 或读取 Secret 明文。轮换、撤销、过期、最后使用 IP 和
`authorization.denied` 审计与普通 Key 使用同一套 Credential 生命周期。

## 外部身份 Binding 与委托 Token

### 联邦授权与 Provisioning

PAM 支持外部联邦使用独立的非对称 JWT Assertion 进行企业/用户授权交换。普通外部 Session JWT
和 `JWT_SECRET` 不会被 PAM 接受。企业与 Project 必须先由 PAM 租户管理员显式绑定；联邦登录不会
批量创建 PAM Project 成员。完整 Assertion 字段、配置项和管理员操作流程见
[`docs/federated-authorization.md`](federated-authorization.md)。

| 方法 | 路径 | Scope | 说明 |
|---|---|---|---|
| POST | `/api/v1/auth/federated/login` | 无（Federated Assertion） | 校验短期 Assertion，JIT 创建/更新 PAM User 并签发 PAM Bearer Token |
| GET | `/api/v1/federated/tenants` | 租户管理员 | 查询当前租户的 联邦企业绑定 |
| POST | `/api/v1/federated/tenants` | 租户管理员 | 将 联邦企业/Workspace 绑定到当前 PAM Tenant |
| GET | `/api/v1/federated/projects` | 租户管理员 | 查询 联邦 Project → PAM Project 绑定 |
| POST | `/api/v1/federated/projects` | 租户管理员 | 手工绑定 联邦 Project 到已有 PAM Project |
| DELETE | `/api/v1/federated/projects/{id}` | 租户管理员 | 撤销外部 Project 绑定 |

| 方法 | 路径 | Scope | 说明 |
|---|---|---|---|
| GET | `/api/v1/external-bindings` | 租户管理员 | 查询当前租户的外部 Agent/User Binding |
| POST | `/api/v1/external-bindings` | 租户管理员 | 将外部 Agent/User 映射到 PAM Principal；Agent 必须同时绑定一个 PAM project |
| DELETE | `/api/v1/external-bindings/{id}` | 租户管理员 | 撤销 Binding，并立即撤销受其影响的委托 Token |
| POST | `/api/v1/external-delegations/exchange` | `pam:delegation:exchange` | 使用长期 Agent Key 换取短时委托 Token |
| POST | `/api/v1/external-delegations/{id}/revoke` | 租户管理员 | 单独撤销一个委托 Token |
| POST | `/api/v1/external-bindings/revoke-workspace` | 租户管理员 | 外部 Workspace 删除或紧急隔离时，批量撤销该 Workspace 的 Binding 和委托 Token |

创建 Agent Binding 示例：

```json
{
  "source": "federated",
  "workspaceId": "workspace-prod",
  "externalProjectId": "external-project-payments",
  "identityType": "AGENT",
  "externalId": "agent-release-01",
  "principalId": "<pam-agent-principal-id>",
  "projectId": "<pam-project-id>"
}
```

创建 User Binding 时使用 `identityType: "USER"`，不传 `projectId`；可以传
`externalProjectId` 将用户限制在某个外部项目，也可以省略以表示整个 Workspace。

长期 Agent Key 换取委托 Token：

```json
{
  "externalContext": {
    "source": "federated",
    "workspaceId": "workspace-prod",
    "projectId": "external-project-payments",
    "agentId": "agent-release-01",
    "taskId": "task-4821",
    "issueId": "issue-4821",
    "originatorUserId": "user-1001",
    "accountableUserId": "user-1001",
    "originatorSource": "human"
  },
  "ttlSeconds": 300
}
```

PAM 只有在以下条件全部满足时才会签发委托 Token：

- 当前长期 Key 属于 `agent_instance` 或 `workload` Principal，并拥有
  `pam:delegation:exchange`；
- `source + workspaceId + externalProjectId + agentId` 命中一个仍有效的 Agent Binding，
  且 Binding 所属 Principal 就是当前认证 Principal；
- Agent Binding 对应的 PAM project 仍允许该 Principal 执行；
- `originatorUserId` 和 `accountableUserId` 如有提供，必须命中有效的 USER Binding，且
  对应 PAM Principal 仍是启用的 `user`；
- `originatorSource` 为 `human` 时必须提供 `originatorUserId`。

委托 Token 是服务端只保存 Hash 的 `pam_dtk_...` opaque credential，默认 5 分钟、最长
15 分钟。它携带签发时固定的外部上下文和 PAM `actingProjectId`；后续创建 Execution 时
可以不再传这两个字段，但如果显式传入，必须与 Token 完全一致。撤销 Agent Binding、
User Binding、父级 API Key 或 PAM Principal 会使对应 Token 立即失效。委托 Token 不包含
审批权限，也不能再次调用 Token Exchange。

通过委托 Token 创建的 Execution 返回 `delegationTokenId`、`onBehalfOfPrincipalId` 和
`accountablePrincipalId`；Access Request 使用同名的 snake_case 字段。三者是 PAM 内部
Principal/Token ID，不是外部用户 ID，适合用于审计关联和责任追踪。审计事件的
`metadata` 也会记录这三个字段以及 `external_context`，但不会把委托 Token 明文写入
数据库或审计日志。

审计查询支持按外部上下文定位责任链：`workspaceId`、`externalProjectId`、`agentId`、
`taskId`、`issueId`、`originatorUserId`、`delegationTokenId`、
`onBehalfOfPrincipalId` 和 `accountablePrincipalId`。这些过滤条件始终叠加当前租户
边界，不会跨租户搜索。

## Connector 与 Integration 管理

以下表格默认要求平台管理员或当前租户管理员。标注“注册读取/写入”的接口同时接受对应 Registration Scope：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/connectors` | 查询 Connector；注册读取 |
| GET | `/api/v1/install-config` | 查询安装所需公开配置 |
| POST | `/api/v1/connectors/onboard` | 旧版共享 Key onboarding；默认关闭并返回 410，仅迁移时显式启用 |
| POST | `/api/v1/connectors/network` | 预置 Network Connector 身份并一次性返回 taskSigningKey（Admin） |
| DELETE | `/api/v1/connectors/{id}` | 请求安全下线 |
| POST | `/api/v1/connectors/{id}/archive` | 归档已下线 Connector |
| POST | `/api/v1/connectors/{id}/check` | 运行连接检查 |
| PUT | `/api/v1/connectors/{id}/namespaces` | 更新 Kubernetes Namespace 范围 |
| PUT | `/api/v1/connectors/{id}/services` | 更新 Linux systemd 服务范围 |
| PUT | `/api/v1/connectors/{id}/project` | 设置 Connector 归属项目（`project_id` 传 `null` 表示归属默认项目） |
| PUT | `/api/v1/connectors/{id}/environment` | 设置 Connector 环境；请求体 `environment` 支持 `development`、`testing`、`production` |
| GET | `/api/v1/projects` | 查询项目列表（含默认项目）；注册读取 |
| POST | `/api/v1/projects` | 创建项目；注册写入；请求体为 `name`、可选 `description` |
| PUT | `/api/v1/projects/{id}` | 更新项目名称与描述；注册写入 |
| DELETE | `/api/v1/projects/{id}` | 删除项目；默认项目不可删除，关联通道/规则自动回落默认项目，有执行、审批、策略决策或审计历史时返回 `409 PROJECT_IN_USE` |
| GET | `/api/v1/project-namespace-rules` | 查询 Namespace 级项目覆盖规则 |
| PUT | `/api/v1/project-namespace-rules` | 新建或更新规则；请求体为 `project_id`、`connector_id`、`namespace` |
| DELETE | `/api/v1/project-namespace-rules/{id}` | 删除规则 |
| GET | `/api/v1/resources/{id}/project-scope` | 查询资源的项目可用范围；租户管理员 |
| PUT | `/api/v1/resources/{id}/project-scope` | 更新资源的项目可用范围；租户管理员 |
| GET | `/api/v1/projects/{projectId}/members` | 查询项目成员；租户管理员或该项目有效成员 |
| GET | `/api/v1/projects/{projectId}/member-candidates` | 查询可加入项目的当前租户 Principal；租户管理员或该项目管理员 |
| POST | `/api/v1/projects/{projectId}/members` | 添加项目成员；租户管理员或该项目管理员 |
| PUT | `/api/v1/projects/{projectId}/members/{principalId}` | 修改成员角色或状态；租户管理员或该项目管理员 |
| DELETE | `/api/v1/projects/{projectId}/members/{principalId}` | 停用项目成员；租户管理员或该项目管理员 |
| POST | `/api/v1/projects/{projectId}/users` | 创建可登录用户并加入项目；租户管理员 |
| GET | `/api/v1/project-users` | 查询当前租户的项目用户；租户管理员 |
| GET | `/api/v1/project-users/{principalId}` | 查询项目用户详情；租户管理员 |
| PUT | `/api/v1/project-users/{principalId}/status` | 启用或停用项目用户；停用时撤销其会话；租户管理员 |
| POST | `/api/v1/project-users/{principalId}/reset-password` | 重置项目用户密码并撤销其会话；租户管理员 |
| POST | `/api/v1/project-users/{principalId}/revoke-sessions` | 撤销项目用户的全部会话；租户管理员 |
| GET | `/api/v1/me/projects` | 查询当前身份可访问的项目 |
| GET | `/api/v1/project-membership-policy` | 查询当前租户是否强制执行项目成员权限 |
| PUT | `/api/v1/project-membership-policy` | 开启或关闭当前租户的项目成员权限；租户管理员 |
| GET | `/api/v1/integrations` | 查询全部 Integration；注册读取 |
| GET | `/api/v1/connectors/{id}/integrations` | 查询 Connector 的 Integration；注册读取 |
| POST | `/api/v1/connectors/{id}/integrations` | 创建 Integration；注册写入 |
| PUT | `/api/v1/connectors/{id}/integrations/{integrationId}` | 更新 Integration；注册写入 |
| DELETE | `/api/v1/connectors/{id}/integrations/{integrationId}` | 删除 Integration |
| GET | `/api/v1/integrations/{integrationId}/project-scope` | 查询 Integration 默认项目、默认环境、允许服务项目和路由规则；注册读取 |
| PUT | `/api/v1/integrations/{integrationId}/project-scope` | 原子更新默认项目、默认环境、允许服务项目和 Jenkins 路径前缀规则；注册写入 |
| GET | `/api/v1/resources/{id}/consumer-projects` | 查询 Jenkins Job 的额外可使用项目 |
| PUT | `/api/v1/resources/{id}/consumer-projects` | 更新 Jenkins Job 的额外可使用项目 |

### 资源项目范围与项目成员权限

租户内的资源可直接开放给多个项目，不需要创建共享项目。`PUT /api/v1/resources/{id}/project-scope`
请求体中的 `mode` 支持：

| 模式 | 语义 |
|---|---|
| `INHERITED` | 按资源、Namespace 规则、Integration、Connector 和默认项目的优先级继承项目范围 |
| `SELECTED_PROJECTS` | 仅对 `projectIds` 中明确指定的一个或多个项目开放 |
| `ALL_PROJECTS` | 对当前租户的全部项目开放；普通用户仍必须是相应项目的有效成员 |
| `UNASSIGNED` | 待分配隔离区；普通项目成员不可见且不能执行 |

示例：

```json
{
  "mode": "SELECTED_PROJECTS",
  "projectIds": ["<project-a-uuid>", "<project-b-uuid>"],
  "reason": "订单与结算项目共同使用"
}
```

项目成员角色固定为：

| 角色 | 查看项目资源 | 发起执行 | 管理项目成员 |
|---|---:|---:|---:|
| `PROJECT_ADMIN` | 是 | 是 | 是 |
| `OPERATOR` | 是 | 是 | 否 |
| `VIEWER` | 是 | 否 | 否 |

平台管理员和租户管理员拥有当前租户全部项目的隐式权限。审批保持租户级；审批通过前服务端会重新校验
原申请人的项目成员状态、角色以及资源项目范围，权限已变化时拒绝继续执行。

项目成员强制策略按租户配置。迁移后缺少 `projectMembershipEnforcement` 设置时按 `false` 处理，
此时是兼容模式，不根据成员关系过滤项目和资源。上线时应先为现有用户及服务 Principal 配置成员关系，
验证 `VIEWER`、`OPERATOR`、`PROJECT_ADMIN` 和租户管理员，再调用：

```http
PUT /api/v1/project-membership-policy
Content-Type: application/json

{"enforced": true}
```

开启后，普通身份的项目列表、资源列表、统计、拓扑、诊断、资源动作和执行入口都会按项目成员关系限制。
服务 Principal 和 Registration Key 对应的 Principal 也应在启用前加入所需项目。

项目用户生命周期接口仅管理 `kind=user` 且 `metadata.role=project_user` 的 Principal，不会修改平台管理员、
租户管理员、Agent 或服务 Principal。用户自行修改密码、管理员重置密码或停用用户都会撤销已有会话。

项目删除采用审计保留优先策略。默认项目不能删除；项目一旦被 Execution、Access Request、Access Grant、
Policy Decision 或 Audit Event 引用，就不能硬删除，接口返回 `409 PROJECT_IN_USE`。这类历史引用不会级联清理，
以保证 `acting_project_id` 对应的执行和审批证据链长期可追溯。

Integration 请求结构：

```json
{
  "protocol": "jenkins",
  "name": "jenkins-production",
  "endpoint": "https://jenkins.example.internal",
  "username": "service-account",
  "credentialRef": "jenkins-api-token",
  "projectId": "<project-uuid>",
  "environment": "production",
  "scope": {},
  "status": "ACTIVE"
}
```

支持的协议以当前 Connector Runner 为准，包括 Jenkins、Grafana、PostgreSQL、MySQL、华为云和阿里云 SLS。`credentialRef` 只引用目标侧凭据；Secret 明文不进入前端或 pam-control 数据库。阿里云 SLS 新接入填写种子 Region，凭据为目标侧保存的 `AK:SK` 或等价 JSON；`scope.regionPolicy` 控制全部/指定/单 Region，`scope.allowedProjects` 可限制 Project。旧项目级官方 HTTPS Endpoint 继续兼容。

Jenkins Job 生命周期使用独立动作：`jenkins.job.create` 只创建且同名冲突，`jenkins.job.update` 更新 inline/SCM Pipeline 定义，`jenkins.job.configure` 仅修改描述和静默期，`jenkins.job.delete` 需要审批及完整 Job 名称二次确认。Job 身份由资源固定，不提供 rename/move；SCM 参数只接受 Credential ID，不接受或返回 Secret。

数据库表资源可使用 `database.table.rows.query@v1` 查询业务行。请求必须提供与资源一致的 `name`、`namespace` 和 1-20 个 `columns`；可选 `filters`、`orderBy`、`limit`（最大 100）及 `offset`（最大 10000）。所有过滤值均使用驱动参数，字段/Schema/表名由 Connector 对照实时 `information_schema` 校验并引用。该动作不接受 SQL 文本、不支持 JOIN 或写操作，响应限制为 1 MiB，超长单元格会截断并返回 `truncatedCells`。该有界查询默认直接执行并完整审计，不进入人工审批；原始行结果在 PAM 中保留 10 分钟，随后替换为清理标记，执行参数、身份、目标、终态和审计继续保留。

Integration 项目范围请求示例：

```json
{
  "defaultProjectId": "<project-uuid>",
  "environment": "production",
  "projectIds": ["<project-uuid>", "<shared-project-uuid>"],
  "rules": [
    {
      "matcher_type": "JENKINS_PATH_PREFIX",
      "pattern": "order-service/",
      "project_id": "<project-uuid>",
      "priority": 100
    }
  ]
}
```

有效主归属优先级为：单资源覆盖 → Integration 路由规则/云资源 `pam-project-id` 标签 → Kubernetes Namespace 规则（仅原生 Kubernetes 资源）→ Integration 默认项目 → Connector 默认项目（仅非 Integration 原生资源）→ 系统默认项目。Integration 的项目归属与执行通道相互独立；创建时未明确传 `projectId` 会写入系统默认项目，不会继承 Connector。升级前未明确归属的 Integration 会一次性保留当时的有效项目，之后不再跟随 Connector 变化。Jenkins Job 仍只有一个主归属；前端可通过单资源项目接口覆盖 Job 主归属或删除覆盖以恢复 Integration/路由继承。`consumer-projects` 表示额外可查看/触发该 Job 的项目，不授予配置或删除治理权。

有效环境独立计算。普通资源优先级为：单资源环境覆盖 → Integration 默认环境 → Connector 环境。数据库资源增加层级继承：表覆盖 → Schema 覆盖 → 数据库实例覆盖 → Integration 默认环境 → Connector 环境；Schema 则从数据库实例继承。父级覆盖是动态计算，不会批量复制到每个子资源，因此新发现的 Schema/表也会自动继承。资源响应中的 `environment` 是最终有效值，`environment_source` 会返回 `RESOURCE`、`DATABASE_SCHEMA`、`DATABASE_INSTANCE`、`INTEGRATION` 或 `CONNECTOR`。项目与环境覆盖互相独立，修改其中一项不会隐式创建另一项覆盖。

华为云 `scope.regionPolicy` 支持 `single`、`all`、`allowlist`。`all` 会从 IAM 项目列表发现账号可访问的全部 Region，并可通过 `deniedRegions` 排除区域。`huawei.ecs.create@v1` 只挂在区域级 `huawei_cloud_account` 资源上，必须明确提交 `projectId` 和 `region`；服务端会校验允许项目并注入不可覆盖的 `pam-*` 标签。该动作是高风险动作，进入审批链路。

ECS 实例支持 `huawei.ecs.start@v1`、`huawei.ecs.stop@v1`、`huawei.ecs.reboot@v1`、`huawei.ecs.update_metadata@v1` 和 `huawei.ecs.delete@v1`。生命周期动作等待华为云 Job 完成并回读最终状态；治理更新只接受最多 3 个业务标签和备注，`pam-*` 项目/环境/Integration 标签由服务端按当前资源归属生成；删除必须提交与固定目标完全一致的 `confirmServerName`，并显式选择是否同时删除公网 IP 和数据盘。所有动作均拒绝跨 Region 或跨资源目标并进入审批。

AIOps 新增动作均为结构化契约：`pod.metrics`、`node.metrics` 读取 Metrics API；华为云自动发现 `huawei_lts_log_group` 和 `huawei_lts_log_stream`，`huawei.lts.logs.query` 直接绑定日志流，由 Connector 从授权资源身份推导日志组/日志流 ID；`aliyun.sls.logs.query` 直接绑定 Logstore。两类云日志查询不限制云端历史范围，但单次最多 500 条/4 MiB，原始结果在 PAM 中仅保留 10 分钟；Grafana 增加 `grafana.folder.update` 与只返回非敏感配置的 `grafana.datasource.get`，Folder 更新必须携带只读预取到的当前 `version` 以执行乐观锁校验，Folder/Dashboard/Datasource 更新均把 UID 固定为授权资源，并递归拒绝 `secureJsonData`、密码、Token、API Key 和 Authorization 字段；Dashboard ConfigMap 只接受 `grafana-dashboard-*` 名称和单个 `.json` Dashboard，并始终进入审批。

## Operator 与 Linux 配对

| 方法 | 路径 | 调用方 | 说明 |
|---|---|---|---|
| POST | `/api/v1/operator/pairings` | Operator | 创建 Kubernetes 配对 |
| GET | `/api/v1/operator/pairings/{id}` | Operator | 查询配对状态 |
| POST | `/api/v1/operator/pairings/{id}/ready` | Operator | 标记 Connector 就绪 |
| POST | `/api/v1/operator/pairings/{id}/deleted` | Operator | 标记目标侧清理完成 |
| POST | `/api/v1/linux/pairings` | Linux Connector | 创建 Linux 配对 |
| POST | `/api/v1/cluster-pairings/options` | Admin | 根据配对码查询集群选项 |
| POST | `/api/v1/cluster-pairings/confirm` | Admin | 确认 Kubernetes 配对范围 |
| POST | `/api/v1/linux-pairings/options` | Admin | 根据配对码查询主机选项 |
| POST | `/api/v1/linux-pairings/confirm` | Admin | 确认 Linux 管理范围 |

配对码和 bootstrap token 是短期凭据，不应写入日志、工单正文或代码仓库。

## Connector Runtime

以下接口不接受管理员 Cookie 或 Agent Key，只接受 Connector 请求签名：

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/connectors/register` | 注册或恢复 Connector |
| POST | `/api/v1/connectors/{id}/heartbeat` | 上报心跳与能力 |
| GET | `/api/v1/connectors/{id}/tasks` | 原子领取待执行任务 |
| POST | `/api/v1/connectors/{id}/tasks/{executionId}/result` | 回传带 dispatch lease 的执行结果 |
| POST | `/api/v1/resources/sync` | 同步资源快照 |
| GET | `/api/v1/connector-runtime/{id}/integrations` | 获取分配给 Connector 的 Integration 配置 |

Connector 签名协议属于内部协议。外部客户端不应模拟该接口；应使用 Agent Execution API。

## 下载接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/downloads/install-linux.sh` | Linux 安装脚本 |
| GET | `/downloads/install-network.sh` | Network Connector 安装脚本 |
| GET | `/downloads/uninstall-linux.sh` | Linux 卸载脚本 |
| GET | `/downloads/pam-linux-connector-linux/{arch}` | Linux Connector，`arch` 为受支持架构 |
| GET | `/downloads/pam-network-connector-linux/{arch}` | Network Connector，`arch` 为受支持架构 |
| GET | `/downloads/pam-linux-helper-linux/{arch}` | Linux Helper |
| GET | `/downloads/pam-operator-chart.tgz` | Operator Helm Chart |

## 维护规则

修改 `RegisterRoutes`、Scope、请求字段或错误语义时，必须同步更新本文档。接口兼容性以实际 Handler 和自动化测试为最终依据。
