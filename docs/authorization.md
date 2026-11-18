# 权限模型与联邦授权

> **面向**：使用者、管理员、开发者与集成方。回答「谁能做什么、权限什么时候生效」，以及外部平台身份如何接入。
> 通俗规则在前，技术附录在后。接口级约束见 [api.md](api.md)，SSO 操作见 [guides.md#oidc](guides.md#oidc)。

## 本篇导航

- [权限模型](#authorization)：四种身份、三条规矩、能力清单、接口守卫
- [联邦授权与 Provisioning](#federation)：外部身份绑定、断言登录、权限边界

<a id="authorization"></a>

## 权限模型

> **面向**：使用者、管理员与开发者。本文是「解释」文档：回答「谁能做什么、为什么这样设计、权限什么时候生效」。
> 通俗规则在前，技术附录在后，不需要懂代码也能读前半部分。
> 接口级约束见 [api.md](api.md)，SSO 接入与提权操作见 [SSO 接入](guides.md#oidc)，概念见 [concepts.md](concepts.md)，术语见 [术语表](concepts.md#glossary)。
>
> 当前管理口径：**新身份默认没有业务组织权限、`platform_admin` 不通过普通管理入口指派、组织与角色变更必须可审计**。

### 一句话

**飞书只负责证明"你是谁"，Warden 自己决定"你能做什么"。** 权限只在一个地方登记，页面上看得见的按钮和后台真正能调用的，读的是同一份名单。

### 一个比喻

把平台想成一栋办公楼。以前楼里有好几份"谁能进哪些房间"的名单：人事一份、门禁系统一份、前台贴一张、各楼层自己又贴一张。换了岗位之后，人事改了名单，门禁卡还是旧的——人进了大楼，却只被允许进 4 间房。

现在只留**一份名单放在前台**：改一次，所有门和所有指示牌同时生效。

### 楼里有四种身份

| 身份 | 谁来给权限 | 能做什么 |
|---|---|---|
| **内置超级管理员**（`platform_admin`） | 固定 ID 的启动管理员按初始化规则获得；管理入口不能授予其他账号 | 全楼通行，包括"给别人分权限"、租户管理、账号与权限页 |
| **部门管理员**（`tenant_admin`） | 管理员通过账号、邀请等管理入口指派 | 管自己部门的人和资产；不能把任何人提成超级管理员 |
| **普通成员**（`user`，默认） | 第一次飞书登录自动建档，或由管理员指派 | 运行总览、项目、资源目录、执行记录；提交申请、执行被批准过的动作 |
| **程序 / Agent** | 管理员发专用 API Key 或委托令牌 | 只能做指定动作、短时效；不算员工账号 |

**应急钥匙**：启动管理员密码账号（`00000000-0000-0000-0000-000000000003`）平时不用，只在飞书故障或配置错误时开门。登录名由 `WARDEN_ADMIN_USERNAME` 提供，特权按固定 ID 认定，页面上的"账号与权限"里不允许修改它。静态 Admin API Key 也映射到这个内置身份，并持有 `warden:admin`，必须按超管凭据保护。

> **实现边界**：正常管理入口不能指派 `platform_admin`，迁移 `099` 也会把既有非内置超管降为 `tenant_admin`；但 JWT 签发目前仍识别其他 Principal 的 `metadata.role=platform_admin` 并授予 `warden:admin`，现有测试保留了这一兼容场景。因此“只有内置账号能拥有超管权限”尚不是运行时硬约束，不要通过直接改库绕过管理入口。严格单超管还需要收敛该兼容分支。

### 三条规矩

1. **新同事默认最小权限。** 生产保持 `WARDEN_OIDC_DEFAULT_ROLE=user`；未匹配组织的新身份进入 `unaffiliated`，只有自助资料权限。`WARDEN_OIDC_DEFAULT_ROLE` 只允许填 `user` 或 `tenant_admin`，**填 `platform_admin` 会直接让服务启动失败**。
2. **调级别只走受守卫的管理入口。**「平台管理 → 账号与权限」要求 `warden:admin` 和平台租户身份；内置超管可以切换组织视野后管理目标租户。普通账号只能被指派为 `user` / `tenant_admin`。改完立即撤销对方的登录会话，对方必须重新登录一次才拿到新权限。
3. **台账只增不删。** 每次变更都写审计（谁、什么时候、把谁从什么改成什么、撤销了几个会话），和角色变更在**同一个事务**里：台账写失败，角色变更就回滚，不会出现"改了但没记录"。

### 权限什么时候生效

| 动作 | 生效时机 |
|---|---|
| 首次飞书登录建档 | 立即，按 `user` |
| 超级管理员在页面改角色 | 立即；目标账号被强制重新登录 |
| 直接改数据库、或其他路径改了角色 | **不保证**，旧的登录令牌里带着旧权限，最多 8 小时后过期。所以不要绕过页面改库 |
| 登录 / 登出审计 | 立即，且带正确租户标记，审计页可查 |

### 部门（租户）之间的隔断

- 普通成员和租户管理员只能访问自己组织内的对象；`unaffiliated` 身份没有业务组织视野。
- 平台级操作（建租户、改并发保护阈值、账号与权限）同时要求 `warden:admin` 和身份属于 `platform` 租户。`X-Warden-Tenant` 只改变业务仓储的组织视野，不会改变身份本身所属的平台域。
- 持有 `warden:admin` 的身份可以跨组织查询和管理账号，审计使用目标记录的实际 `tenant_id`；正常管理流程应由内置超管执行。

### 还没做的（下一步，不急）

- 项目内部更细的权限（PROJECT_ADMIN / OPERATOR / VIEWER）目前还是另一套判定，默认不强制；以后合并到同一份名单。
- 按飞书组织架构自动分配级别：等飞书能稳定提供部门/组信息再接，届时优先级是 **手工指派 > IdP 同步 > 默认 `user`**。
- 入职自动开通、离职自动停用（SCIM）、细粒度 RLS、静态管理员 Key 轮换。
- 内置应急 `admin` 与静态 `WARDEN_ADMIN_API_KEY` 保持现状，仅在此登记风险：静态 Key 是全局共享、不可轮换的超级权限凭据，应限制存放范围。

---

### 附录：技术口径

#### 唯一权威来源

`server/internal/control/authorization.go` 集中定义：

- 角色常量 `RoleUser` / `RoleTenantAdmin` / `RolePlatformAdmin`；`validAccountRole` 识别三种角色，`validAssignableRole` 限制账号、邀请等管理入口只能指派 `user` / `tenant_admin`，`validOIDCDefaultRole` 同样禁止 `platform_admin`。
- Scope 常量 `ScopeAdmin` / `ScopeTenantAdmin` / ...，后端 `RequireScope` 与前端菜单声明共用同一批字符串。
- `Capabilities(scopes)`：把身份的 scopes 规范化成能力清单；`warden:admin` 是通配 scope，展开为全部 scope。`GET /api/v1/auth/me` 以 `capabilities` 字段下发。
- `RequirePlatformTenant`：平台级接口的租户守卫。

角色 → scopes 的映射只在 `issueJWTWithTTL` 中发生一次，使用 `defaultUserScopes()` / `tenantAdminScopes()`。前端用 `canUser(user, scope)`（回退到 `scopes`）判断，不再用 `is_platform_admin` 或 `role` 字符串做门控。

#### 当前能力清单

| Scope | 含义 | 默认 `user` | `tenant_admin` | `platform_admin` |
|---|---|:---:|:---:|:---:|
| `warden:self:profile` | 待分配身份的自助资料能力，不开放业务接口 | | | |
| `warden:admin` | 通配超级权限（展开为全部） | | | ✓ |
| `warden:tenant:admin` | 部门管理 | | ✓ | ✓ |
| `warden:resources:read` | 读资源 | ✓ | ✓ | ✓ |
| `warden:executions:create` | 创建执行 | ✓ | ✓ | ✓ |
| `warden:executions:read:self` | 读自己的执行 | ✓ | ✓ | ✓ |
| `warden:executions:cancel:self` | 取消自己的执行 | ✓ | ✓ | ✓ |
| `warden:credentials:manage` | 凭据管理 | | ✓ | ✓ |
| `warden:approve` | 审批 | | ✓ | ✓ |
| `warden:audit:read` | 审计 | | ✓ | ✓ |
| `warden:registration:read` / `write` | 接入注册 | | ✓ | ✓ |
| `warden:delegation:exchange` | 委托令牌交换 | | | ✓ |

前端 `AppSidebar.tsx` 的每个菜单项用 `requires` 声明所需 scope，与后端接口守卫对应：`/approvals` → `warden:approve`、`/audit` → `warden:audit:read`、`/integrations` → `warden:registration:read`、其余工作台菜单 → `warden:tenant:admin`，基础 4 项无要求。

#### 接口约束

| 接口 | 守卫 |
|---|---|
| `GET /api/v1/accounts`、`PUT /api/v1/accounts/{id}/role` | `RequireScope("warden:admin")` + `RequirePlatformTenant` |
| `GET/POST /api/v1/tenants*`（8 个） | 同上 |
| 其他写接口 | 原有 `RequireScope` / `RequireTenantAdmin` |

`accounts.go` 的列表、查询、计数、更新 SQL 全部带 `tenant_id` 过滤；跨租户目标返回 404（不泄露存在性）。`CreatePrincipal` 会剥离请求体 `metadata.role`，避免通过机器身份接口写入角色标记。

#### 其他已知遗留

钉钉审批链路缺少租户上下文（多租户下审批会 404、审计 tenant 为空）；联邦断言登录签发的短 JWT 未写 `admin_sessions`，暂不能调用 `/api/v1/`；项目权限存在 `projectAccess` / `principalProjectAccess` / `project_scope` 三套并行判定；`admin_sessions` 无 `tenant_id` 列；角色变更仍需显式撤销会话才生效（账号接口已做，其他入口未统一）。

#### 验证

`go test ./internal/control ./internal/storage/postgres`、`go build ./...`、`go vet ./...`、前端 `tsc --noEmit` + `npm run build`。新增单测覆盖能力清单展开、默认角色禁止 platform_admin、平台租户守卫。


---

<a id="federation"></a>

## 联邦授权与 Provisioning

> **面向**：需要把外部平台身份 / 企业 / 项目接入 Warden 的集成方。本文是「解释 + 契约」文档，说明绑定关系、断言登录流程与权限边界。
> 概念见 [concepts.md](concepts.md)，OIDC/SSO 见 [SSO 接入](guides.md#oidc)，接口见 [api.md](api.md)，配置项见 [配置参考](reference.md#configuration)。
> 文中「外部」指代对接的外部平台（脱敏口径）。

本期新增 Warden 侧的联邦授权基础层，兼容 外部 当前 `warden-ops` 的服务端 Token 调用方式。
外部 最新的 Issue Agent 发布治理仍然通过现有 `/api/v1/projects`、`/api/v1/resources` 和
`/api/v1/executions` 接口执行，不需要改动现有执行链路。

### 领域关系

```text
外部 enterprise_credit_code  -> Warden Tenant
外部 workspace.id             -> external workspace context
外部 project.id               -> Warden Project（管理员显式绑定）
外部 Agent                    -> Warden Agent Principal（管理员显式绑定）
```

外部 Workspace 成员不会因为联邦登录自动加入 Warden Project。Warden 的项目成员和资源权限仍由 Warden
租户管理员维护；普通 外部 member 可以继续在 外部 中发起已授权 Agent 任务，但实际执行仍由
Warden 的 Agent Principal、Project Membership、资源范围、审批和策略重新授权。

### 管理员预配置

#### 绑定企业与 Tenant

```http
POST /api/v1/federated/tenants
Authorization: Bearer <warden-tenant-admin-token>
Content-Type: application/json

{
  "source": "federated",
  "externalEnterpriseId": "统一社会信用代码",
  "externalWorkspaceId": "external-workspace-uuid"
}
```

这一步只允许把企业绑定到当前 Warden Tenant，不会创建项目或批量创建用户。

#### 绑定 外部 Project 与 Warden Project

```http
POST /api/v1/federated/projects
Authorization: Bearer <warden-tenant-admin-token>
Content-Type: application/json

{
  "source": "federated",
  "externalWorkspaceId": "external-workspace-uuid",
  "externalProjectId": "external-project-uuid",
  "externalProjectName": "生产发布",
  "projectId": "warden-project-uuid"
}
```

Warden 不会自动创建或猜测目标 Project。未绑定的外部 Project 在登录交换时返回
`PROJECT_BINDING_REQUIRED`，不能直接获得该项目的资源执行权限。

查询和撤销：

```http
GET    /api/v1/federated/tenants
GET    /api/v1/federated/projects
DELETE /api/v1/federated/projects/{bindingId}
```

### 外部 Assertion 登录交换

Warden 不接受 外部 的普通 Session JWT，也不接受共享 `JWT_SECRET`。外部 后端需要使用单独的
非对称私钥签发短期 JWT Assertion，Warden 使用配置的公钥验证。

必填字段：

```json
{
  "iss": "https://idp.example.com",
  "sub": "external-user-id",
  "aud": "warden",
  "jti": "one-time-assertion-id",
  "nonce": "one-time-nonce",
  "iat": 1720000000,
  "exp": 1720000300,
  "enterprise_id": "统一社会信用代码",
  "workspace_id": "external-workspace-uuid",
  "project_id": "external-project-uuid",
  "email": "user@example.com",
  "name": "外部 User",
  "workspace_role": "member"
}
```

交换：

```http
POST /api/v1/auth/federated/login
Content-Type: application/json

{"assertion":"<signed-jwt>"}
```

成功后 Warden 会：

1. 校验签名、Issuer、Audience、`iat`/`exp`、`jti` 和 `nonce`；
2. 校验企业和 Workspace 是否已经由 Warden 管理员绑定；
3. 按 `(issuer, subject)` 创建或更新 passwordless Warden User；
4. 不自动加入任何 Warden Project；
5. 如果 Assertion 携带 Project，校验该外部 Project 是否已经手工绑定；
6. 返回 Warden 签发的 15 分钟 Bearer Access Token 和该用户当前已被 Warden 授权的项目。

第一期不签发 Refresh Token；外部 后端在 Access Token 过期后重新生成一次性 Assertion 交换，
避免把长期 Warden 凭据保存到浏览器或 Agent 运行环境。

如果 Project 尚未绑定，返回：

```json
{
  "code": "PROJECT_BINDING_REQUIRED",
  "message": "外部 Project 尚未绑定 Warden Project",
  "externalProjectId": "external-project-uuid",
  "workspaceId": "external-workspace-uuid"
}
```

### 配置

```text
WARDEN_FEDERATED_ASSERTION_PUBLIC_KEY_FILE=/etc/warden/secrets/federated-assertion-public.pem
WARDEN_FEDERATED_ASSERTION_ISSUER=https://idp.example.com
WARDEN_FEDERATED_ASSERTION_AUDIENCE=warden
```

不配置公钥时，现有 Warden 登录和 外部 `warden-ops` 服务端 Token 链路保持不变，联邦登录端点返回
`FEDERATED_AUTH_NOT_CONFIGURED`。

### 权限边界

外部 `owner`/`admin` 只能作为接入发起人，不能仅凭 Assertion 自动成为 Warden `tenant_admin`。
Warden Project 的 `PROJECT_ADMIN`、`OPERATOR`、`VIEWER` 角色必须由 Warden 管理员显式分配。

普通 外部 member 不获得 Warden 控制台登录权限；如果其在 外部 中触发一个已经绑定且被策略允许的
Agent 任务，Warden 仍会以 Agent Principal 执行，并记录 `on_behalf_of` 和
`accountable_principal` 归因。

### 已知限制与下一步

- 断言登录签发的是 15 分钟 Bearer Token，**没有 Refresh Token**；过期后由外部后端重新生成一次性 Assertion 交换。
- 该短 JWT 当前未写入 `admin_sessions`，而会话认证分支会查这张表，因此它**暂不能调用 `/api/v1/` 的常规接口**；这是已知设计债，见 [现状与路线](architecture.md#roadmap)。
- 外部平台成员不会因为联邦登录自动加入 Warden Project；项目成员与资源权限必须由 Warden 管理员显式分配。
- 配置项 `WARDEN_FEDERATED_ASSERTION_*` 完整说明见 [配置参考](reference.md#configuration)。
- 排障（如 `PROJECT_BINDING_REQUIRED`、`FEDERATED_AUTH_NOT_CONFIGURED`）见 [troubleshooting.md](troubleshooting.md)。
