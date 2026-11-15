# 联邦授权与 Provisioning（第一期）

> **面向**：需要把外部平台身份 / 企业 / 项目接入 Warden 的集成方。本文是「解释 + 契约」文档，说明绑定关系、断言登录流程与权限边界。
> 概念见 [concepts.md](concepts.md)，OIDC/SSO 见 [oidc-setup.md](oidc-setup.md)，接口见 [api.md](api.md)，配置项见 [configuration.md](configuration.md)。
> 文中「外部」指代对接的外部平台（脱敏口径）。

本期新增 Warden 侧的联邦授权基础层，兼容 外部 当前 `pam-ops` 的服务端 Token 调用方式。
外部 最新的 Issue Agent 发布治理仍然通过现有 `/api/v1/projects`、`/api/v1/resources` 和
`/api/v1/executions` 接口执行，不需要改动现有执行链路。

## 领域关系

```text
外部 enterprise_credit_code  -> Warden Tenant
外部 workspace.id             -> external workspace context
外部 project.id               -> Warden Project（管理员显式绑定）
外部 Agent                    -> Warden Agent Principal（管理员显式绑定）
```

外部 Workspace 成员不会因为联邦登录自动加入 Warden Project。Warden 的项目成员和资源权限仍由 Warden
租户管理员维护；普通 外部 member 可以继续在 外部 中发起已授权 Agent 任务，但实际执行仍由
Warden 的 Agent Principal、Project Membership、资源范围、审批和策略重新授权。

## 管理员预配置

### 绑定企业与 Tenant

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

### 绑定 外部 Project 与 Warden Project

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

## 外部 Assertion 登录交换

Warden 不接受 外部 的普通 Session JWT，也不接受共享 `JWT_SECRET`。外部 后端需要使用单独的
非对称私钥签发短期 JWT Assertion，Warden 使用配置的公钥验证。

必填字段：

```json
{
  "iss": "https://idp.example.com",
  "sub": "external-user-id",
  "aud": "pam",
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

## 配置

```text
WARDEN_FEDERATED_ASSERTION_PUBLIC_KEY_FILE=/etc/warden/secrets/federated-assertion-public.pem
WARDEN_FEDERATED_ASSERTION_ISSUER=https://idp.example.com
WARDEN_FEDERATED_ASSERTION_AUDIENCE=pam
```

不配置公钥时，现有 Warden 登录和 外部 `pam-ops` 服务端 Token 链路保持不变，联邦登录端点返回
`FEDERATED_AUTH_NOT_CONFIGURED`。

## 权限边界

外部 `owner`/`admin` 只能作为接入发起人，不能仅凭 Assertion 自动成为 Warden `tenant_admin`。
Warden Project 的 `PROJECT_ADMIN`、`OPERATOR`、`VIEWER` 角色必须由 Warden 管理员显式分配。

普通 外部 member 不获得 Warden 控制台登录权限；如果其在 外部 中触发一个已经绑定且被策略允许的
Agent 任务，Warden 仍会以 Agent Principal 执行，并记录 `on_behalf_of` 和
`accountable_principal` 归因。

## 已知限制与下一步

- 断言登录签发的是 15 分钟 Bearer Token，**没有 Refresh Token**；过期后由外部后端重新生成一次性 Assertion 交换。
- 该短 JWT 当前未写入 `admin_sessions`，而会话认证分支会查这张表，因此它**暂不能调用 `/api/v1/` 的常规接口**；这是已知设计债，见 [roadmap.md](roadmap.md)。
- 外部平台成员不会因为联邦登录自动加入 Warden Project；项目成员与资源权限必须由 Warden 管理员显式分配。
- 配置项 `WARDEN_FEDERATED_ASSERTION_*` 完整说明见 [configuration.md](configuration.md)。
- 排障（如 `PROJECT_BINDING_REQUIRED`、`FEDERATED_AUTH_NOT_CONFIGURED`）见 [troubleshooting.md](troubleshooting.md)。
