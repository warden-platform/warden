# 权限模型

> **面向**：使用者、管理员与开发者。本文是「解释」文档：回答「谁能做什么、为什么这样设计、权限什么时候生效」。
> 通俗规则在前，技术附录在后，不需要懂代码也能读前半部分。
> 接口级约束见 [api.md](api.md)，SSO 接入与提权操作见 [oidc-setup.md](oidc-setup.md)，概念见 [concepts.md](concepts.md)，术语见 [glossary.md](glossary.md)。
>
> 三条产品决策（2026-11-14 确认）：**新同事默认最低权限、只有超级管理员能提权、当前全部手工指派**。

## 一句话

**飞书只负责证明"你是谁"，PAM 自己决定"你能做什么"。** 权限只在一个地方登记，页面上看得见的按钮和后台真正能调用的，读的是同一份名单。

## 一个比喻

把平台想成一栋办公楼。以前楼里有好几份"谁能进哪些房间"的名单：人事一份、门禁系统一份、前台贴一张、各楼层自己又贴一张。换了岗位之后，人事改了名单，门禁卡还是旧的——人进了大楼，却只被允许进 4 间房。

现在只留**一份名单放在前台**：改一次，所有门和所有指示牌同时生效。

## 楼里有四种身份

| 身份 | 谁来给权限 | 能做什么 |
|---|---|---|
| **超级管理员**（`platform_admin`） | 系统初始化，或由现有超级管理员指定 | 全楼通行，包括"给别人分权限"、租户管理、账号与权限页 |
| **部门管理员**（`tenant_admin`） | 超级管理员指定 | 管自己部门的人和资产；看不到别的部门，**不能把任何人提成超级管理员** |
| **普通成员**（`user`，默认） | 第一次飞书登录自动建档 | 运行总览、项目、资源目录、执行记录；提交申请、执行被批准过的动作 |
| **程序 / Agent** | 管理员发专用 API Key 或委托令牌 | 只能做指定动作、短时效；不算员工账号 |

**应急钥匙**：内置 `admin` 密码账号（`00000000-0000-0000-0000-000000000003`）平时不用，只在飞书故障或配置错误时开门。它是按固定 ID 认定的，页面上的"账号与权限"里不允许修改它。本轮按约定不做改动。

## 三条规矩

1. **新同事一律最低权限。** 第一次用飞书登录只会建一个 `user` 账号。`PAM_OIDC_DEFAULT_ROLE` 只允许填 `user` 或 `tenant_admin`，**填 `platform_admin` 会直接让服务启动失败**——绝不会因为"登录了一次"就自动变成超级管理员。
2. **调级别只在一个地方：平台管理 → 账号与权限。** 只有超级管理员能进，且只能改**自己部门（平台租户）内**的账号。改完立即撤销对方的登录会话，对方必须重新登录一次才拿到新权限，不用等、不用刷新碰运气。
3. **台账只增不删。** 每次变更都写审计（谁、什么时候、把谁从什么改成什么、撤销了几个会话），和角色变更在**同一个事务**里：台账写失败，角色变更就回滚，不会出现"改了但没记录"。

## 权限什么时候生效

| 动作 | 生效时机 |
|---|---|
| 首次飞书登录建档 | 立即，按 `user` |
| 超级管理员在页面改角色 | 立即；目标账号被强制重新登录 |
| 直接改数据库、或其他路径改了角色 | **不保证**，旧的登录令牌里带着旧权限，最多 8 小时后过期。所以不要绕过页面改库 |
| 登录 / 登出审计 | 立即，且带正确租户标记，审计页可查 |

## 部门（租户）之间的隔断

- 每个账号属于一个租户；账号列表、改角色、租户管理都只能操作**自己租户内**的对象。
- 平台级操作（建租户、改配额、账号与权限）额外要求身份属于 `platform` 租户。**别的部门里即使有人被标成超级管理员，也不能跨平台操作。**
- 超级管理员这个级别本身是"跨部门"的，所以更要把提权入口收在平台租户里。

## 还没做的（下一步，不急）

- 项目内部更细的权限（PROJECT_ADMIN / OPERATOR / VIEWER）目前还是另一套判定，默认不强制；以后合并到同一份名单。
- 按飞书组织架构自动分配级别：等飞书能稳定提供部门/组信息再接，届时优先级是 **手工指派 > IdP 同步 > 默认 `user`**。
- 入职自动开通、离职自动停用（SCIM）、细粒度 RLS、静态管理员 Key 轮换。
- 内置应急 `admin` 与静态 `PAM_ADMIN_API_KEY` 保持现状，仅在此登记风险：静态 Key 是全局共享、不可轮换的超级权限凭据，应限制存放范围。

---

## 附录：技术口径

### 唯一权威来源

`server/internal/control/authorization.go` 集中定义：

- 角色常量 `RoleUser` / `RoleTenantAdmin` / `RolePlatformAdmin`；`validAccountRole` 用于账号接口，`validOIDCDefaultRole` 更窄（禁止 `platform_admin`）。
- Scope 常量 `ScopeAdmin` / `ScopeTenantAdmin` / ...，后端 `RequireScope` 与前端菜单声明共用同一批字符串。
- `Capabilities(scopes)`：把身份的 scopes 规范化成能力清单；`pam:admin` 是通配 scope，展开为全部 scope。`GET /api/v1/auth/me` 以 `capabilities` 字段下发。
- `RequirePlatformTenant`：平台级接口的租户守卫。

角色 → scopes 的映射只在 `issueJWTWithTTL` 中发生一次，使用 `defaultUserScopes()` / `tenantAdminScopes()`。前端用 `canUser(user, scope)`（回退到 `scopes`）判断，不再用 `is_platform_admin` 或 `role` 字符串做门控。

### 当前能力清单

| Scope | 含义 | 默认 `user` | `tenant_admin` | `platform_admin` |
|---|---|:---:|:---:|:---:|
| `pam:admin` | 通配超级权限（展开为全部） | | | ✓ |
| `pam:tenant:admin` | 部门管理 | | ✓ | ✓ |
| `pam:resources:read` | 读资源 | ✓ | ✓ | ✓ |
| `pam:executions:create` | 创建执行 | ✓ | ✓ | ✓ |
| `pam:executions:read:self` | 读自己的执行 | ✓ | ✓ | ✓ |
| `pam:executions:cancel:self` | 取消自己的执行 | ✓ | ✓ | ✓ |
| `pam:credentials:manage` | 凭据管理 | | ✓ | ✓ |
| `pam:approve` | 审批 | | ✓ | ✓ |
| `pam:audit:read` | 审计 | | ✓ | ✓ |
| `pam:registration:read` / `write` | 接入注册 | | ✓ | ✓ |
| `pam:delegation:exchange` | 委托令牌交换 | | | ✓ |

前端 `AppSidebar.tsx` 的每个菜单项用 `requires` 声明所需 scope，与后端接口守卫对应：`/approvals` → `pam:approve`、`/audit` → `pam:audit:read`、`/integrations` → `pam:registration:read`、其余工作台菜单 → `pam:tenant:admin`，基础 4 项无要求。

### 接口约束

| 接口 | 守卫 |
|---|---|
| `GET /api/v1/accounts`、`PUT /api/v1/accounts/{id}/role` | `RequireScope("pam:admin")` + `RequirePlatformTenant` |
| `GET/POST /api/v1/tenants*`（8 个） | 同上 |
| 其他写接口 | 原有 `RequireScope` / `RequireTenantAdmin` |

`accounts.go` 的列表、查询、计数、更新 SQL 全部带 `tenant_id` 过滤；跨租户目标返回 404（不泄露存在性）。`CreatePrincipal` 会剥离请求体 `metadata.role`，避免通过机器身份接口写入角色标记。

### 已知遗留（本轮未改）

钉钉审批链路缺少租户上下文（多租户下审批会 404、审计 tenant 为空）；联邦断言登录签发的短 JWT 未写 `admin_sessions`，暂不能调用 `/api/v1/`；项目权限存在 `projectAccess` / `principalProjectAccess` / `project_scope` 三套并行判定；`admin_sessions` 无 `tenant_id` 列；角色变更仍需显式撤销会话才生效（账号接口已做，其他入口未统一）。

### 验证

`go test ./internal/control ./internal/storage/postgres`、`go build ./...`、`go vet ./...`、前端 `tsc --noEmit` + `npm run build`。新增单测覆盖能力清单展开、默认角色禁止 platform_admin、平台租户守卫。

### 下一步

- [oidc-setup.md](oidc-setup.md)：启用 SSO 并在页面提权
- [api.md](api.md)：账号接口的请求体与错误码
- [roadmap.md](roadmap.md)：已知设计债与第二阶段规划
- [troubleshooting.md](troubleshooting.md)：「登录后功能变少」「提权后没生效」
