# 操作指南：故障排查

> **面向**：值班与排障人员。
> **目标**：按症状快速定位到具体层（配置 / 认证 / 权限 / Connector / 执行 / 数据），并知道哪些问题必须去外部平台处理。
> 部署见 [installation.md](installation.md)，运维见 [operations.md](operations.md)，SSO 细节见 [oidc-setup.md](oidc-setup.md)。

## 先按顺序做三件事

1. **确认控制面活着**：`GET /api/v1/health`、`/livez`、`/readyz` 是否 200；不是 200 先看启动日志与数据库。
2. **确认是哪一层**：浏览器 → 前端静态资源 → 反向代理 → 控制面 → 数据库 / IdP / Connector。用 `curl -i` 看状态码与 `Location`，不要只看页面文案。
3. **看日志与审计**：控制面日志、Connector 日志、`audit_events` 最近记录。授权失败会写 `authorization.denied`。

诊断命令模板（把地址替换为实际值，不要打印 Secret 内容）：

```bash
BASE=https://<pam.example.com>
curl -sS -i "$BASE/api/v1/health" | head -10
curl -sS "$BASE/api/v1/auth/oidc/config"; echo
curl -sS -o /dev/null -w '%{http_code}\n' "$BASE/api/v1/auth/me"     # 401 表示未登录
curl -sS -D - -o /dev/null "$BASE/api/v1/auth/oidc/start" | grep -iE '^HTTP/|^location:|^set-cookie:'
```

---

## 一、控制面启动失败

| 症状 | 常见原因 | 处理 |
|---|---|---|
| 日志提示 `must use independent values` | 4 个必需 Secret 有重复或未达 32 字节 | 分别重新生成 `task_signing_key` / `jwt_signing_key` / `credential_encryption_key` / `admin_api_key` |
| 提示 `... or ..._FILE is required` | 必需 Secret 缺失或文件为空 | 检查变量名、`_FILE` 路径、文件权限与内容（trim 后非空） |
| 明文变量与 `*_FILE` 同时设置 | `config.Secret` 明确禁止 | 只保留一种写法，生产保留 `_FILE` |
| `数据库连接失败` | DSN 错误、PG 未就绪、网络不通 | 检查 `PAM_DATABASE_URL_FILE`、防火墙、容器健康状态 |
| 启动成功但接口 500 / 缺表 | **忘记执行迁移** | 控制面不会自动跑迁移，按 [operations.md](operations.md) 补齐 |
| `PAM_OIDC_DEFAULT_ROLE must be one of ...` | 把默认角色配成了 `platform_admin` | 改成 `user` 或 `tenant_admin`；超管只能页面指派 |
| 端口被占用 | 本地启动脚本会清理 `PAM_PORT` 上的进程 | 换端口或先停掉占用者，注意别误杀别的服务 |

## 二、登录问题

| 症状 | 常见原因 | 处理 |
|---|---|---|
| 密码登录一直失败 | `PAM_ADMIN_PASSWORD_FILE` 未配置或密码哈希未同步 | 配好后重启，运行时会同步 bcrypt 哈希；仍失败查登录限流（10 次 / 5 分钟） |
| 登录页没有 SSO 按钮 | `PAM_OIDC_ISSUER` 为空（OIDC 未启用）或前端是旧产物 | 检查 `/oidc/config` 的 `enabled`；重新构建并替换前端 `dist` |
| 飞书 `20029` / `redirect_uri 请求不合法` | **IdP 上游**回调未加入飞书后台白名单 | 去飞书应用后台登记 IdP 的回调地址。这步在外部平台，PAM 与代码都改不了 |
| `INVALID_OIDC_STATE` / state 校验失败 | 登录与回调 host 混用 `localhost` / `127.0.0.1`；Cookie 丢失；反代未传 `X-Forwarded-Proto`；本地 HTTP 下 Secure Cookie 被丢弃 | 统一 host、`PAM_PUBLIC_CONTROL_URL`、`PAM_OIDC_REDIRECT_URL`；本地设 `PAM_COOKIE_SECURE=false`；检查代理头。失败回调不清 Cookie，可直接重试 |
| `OIDC_PKCE_MISSING` | 回调缺 `oidc_verifier` Cookie 或 IdP 不支持 S256 | 确认同源、IdP 开启 PKCE；PAM 不会降级 |
| `OIDC_INVALID_SUBJECT` | IdP 的 userinfo 实现把 `sub` 覆盖为空 | 在 IdP 侧补全 userinfo；PAM 拒绝空 `sub` 是为了防止所有用户被合并成一个身份 |
| 首次 SSO 登录 500 | 旧版本 `ProvisionIdentity` 错误变量被覆盖、`pgx.ErrNoRows` 丢失 | 已修复，升级控制面 |
| 顶栏显示 `federated:<hash>` | 前端读了内部标识 `name` 而不是 `display_name` | 使用新版前端；`/auth/me` 已下发展示名 / 邮箱 / 头像 |
| 登出后 IdP 会话仍在 | 用了 `/auth/logout` 而不是 `/auth/oidc/logout` | SSO 用户走 `logout_url` |
| 顶栏没有邮箱 | 上游身份源没返回企业邮箱 | 不影响功能；在 IdP 侧补 claim |

## 三、权限问题

| 症状 | 常见原因 | 处理 |
|---|---|---|
| 登录后菜单只剩 4 项、很多功能看不到 | 首次 SSO 建档角色是 `user`（最小权限） | 由超管在「平台管理 → 账号与权限」提权，然后**退出重新 SSO 登录** |
| 提权后刷新页面仍无效 | JWT 里的 `scopes` 是登录时冻结的 | 必须重新登录；页面变更已撤销目标会话，正常重新登录即可 |
| 直接改库后权限没变 | 未撤销会话，旧 JWT 最多 8 小时后过期 | 不要绕过页面改库；确需修数据后手动撤销会话并补审计 |
| 接口 403、`authorization.denied` 审计 | Scope 不足或 Key 权限不够 | 对照 [authorization.md](authorization.md) 的能力清单；检查用的是 Agent Key 还是管理员会话 |
| `PLATFORM_TENANT_REQUIRED` | 身份不属于 `platform` 租户却访问平台级接口 | 平台级操作必须由 `platform` 租户的超管执行 |
| 静态 Admin Key 调用 401 | Key 轮换 / 文件替换后调用方未更新 | 更新调用方的 Bearer Key |

## 四、Connector 与资源

| 症状 | 常见原因 | 处理 |
|---|---|---|
| Connector 一直 `DRAFT` | 配对未完成、配对码过期（10 分钟）、Web 未确认 | 重新取配对码并在 Web 确认 |
| 已下线 Connector 又尝试上线 | 正常保护行为：注册 / 心跳发现 `DELETE_REQUESTED` 会拒绝激活 | 不需要处理；若确需恢复，走重新接入 |
| 资源目录为空 | 未接 Connector，或资源同步尚未完成（5min 一轮）、Connector 心跳超 90s | 先查心跳与 `/connectors/{id}/check`，再等一轮同步 |
| `CONNECTOR_UPGRADE_REQUIRED` | 动作版本与 Connector capabilities 不匹配 | 升级 Connector 二进制到支持该 `name@vN` 的版本 |
| `LEGACY_ONBOARDING_DISABLED` (410) | 调用了旧清单接入接口 | 新集群用 Operator 配对；旧接口仅迁移期用 `PAM_ENABLE_LEGACY_CONNECTOR_KEY=true` |
| 资源归属项目 / 环境不对 | 继承链是「单资源覆盖 → Integration → Connector」 | 在资源或通道设置页调整，见 [actions.md](actions.md) |
| 网络 Connector 连不上设备 | 凭据引用、Host Key 指纹或出站网络 | 确认凭据目录文件名、`pam:` 引用、预登记指纹与出站 HTTPS 可达 |

## 五、执行与审批

| 症状 | 常见原因 | 处理 |
|---|---|---|
| Execution 一直 `WAITING_APPROVAL` | 策略判定为需审批，或审批单过期 | 在审批页处理；过期后重新发起 |
| 一直 `QUEUED` | 没有在线 Connector 领取任务 | 查心跳、任务轮询与 Connector 状态 |
| `UNKNOWN` | 执行超时 / 结果不确定 | **只读状态重读**，不要自动重放；人工确认目标状态 |
| 生产变更没进审批却被放行 | 检查是否误用了非生产环境标记或策略矩阵被改 | Rego 的 `DENY` 不可被矩阵覆盖，但 `ALLOW` 可被收紧；核对策略矩阵 |
| `JENKINS_JOB_CONFLICT` | 同名 Job 已存在 | `jenkins.job.create` 不会覆盖，改用更新动作 |

## 六、数据与审计

| 症状 | 常见原因 | 处理 |
|---|---|---|
| 审计页缺某类事件 | `audit_events.action` 是 PostgreSQL 枚举，新动作名没配套迁移 → 写入静默失败 | 补 `ALTER TYPE audit_action ADD VALUE` 迁移并登记发布清单；日志里会有审计写入失败记录 |
| 审计事件 `tenant_id` 为空 | 早期数据，或某些链路（如钉钉审批）缺租户上下文 | 属已知设计债，见 [roadmap.md](roadmap.md) |
| 角色改了但审计没有 | 审计与角色变更在同一事务；写入失败会整体回滚 | 看日志与审计枚举，修复后重试 |
| 迁移重放报错 | 迁移未写成幂等，或顺序不对 | 按 `deploy/release/migrations.txt` 顺序执行；不要跳号 |

## 七、前端与代理

| 症状 | 常见原因 | 处理 |
|---|---|---|
| 页面白屏 / 静态资源 404 | 前端 `dist` 未替换完整或 nginx 缺 SPA fallback | 检查 `try_files ... /index.html`，重新拷贝完整 `dist` |
| 前端调 API 401 | Cookie 未带上（跨域、Secure、SameSite）或代理路径不对 | 检查 `/api` 反代、`credentials: 'include'`、HTTPS 与 `PAM_COOKIE_SECURE` |
| 菜单显示但接口 403（或反之） | 前端产物与后端版本不一致 | 同一次发布一起替换镜像与 `dist`；`capabilities` 来自 `/auth/me` |
| 健康检查失败 | 用了 `/healthz` 打后端 | 后端用 `/livez` / `/readyz` / `/api/v1/health`；`/healthz` 只属于前端 nginx |

## 错误码速查

| 错误码 | 含义 | 去哪查 |
|---|---|---|
| `INVALID_OIDC_STATE` | OIDC state 校验失败 | [oidc-setup.md](oidc-setup.md) |
| `OIDC_PKCE_MISSING` | 缺 PKCE verifier，拒绝降级 | 同上 |
| `OIDC_INVALID_SUBJECT` | id_token 缺 `sub` | 同上 |
| `FEDERATED_AUTH_NOT_CONFIGURED` | 未配置断言验签公钥 | [federated-authorization.md](federated-authorization.md) |
| `PROJECT_BINDING_REQUIRED` | 外部项目未绑定 PAM 项目 | 同上 |
| `INVALID_ACCOUNT_ROLE` | 角色不在白名单 | [authorization.md](authorization.md) |
| `BUILTIN_ADMIN_PROTECTED` | 内置应急超管不可修改 | 同上 |
| `CANNOT_DEMOTE_SELF` | 不能给自己降权 | 同上 |
| `LAST_PLATFORM_ADMIN` | 不能降权最后一个非内置超管 | 同上 |
| `PLATFORM_TENANT_REQUIRED` | 平台级接口要求 `platform` 租户 | 同上 |
| `ACCOUNT_NOT_FOUND` | 账号不存在或不在当前租户 | 同上 |
| `CONNECTOR_UPGRADE_REQUIRED` | Connector 不支持该动作版本 | [onboarding.md](onboarding.md) |
| `LEGACY_ONBOARDING_DISABLED` | 旧版清单接入已关闭 | 同上 |
| `JENKINS_JOB_CONFLICT` | 同名 Job 已存在 | [actions.md](actions.md) |
| `RESOURCE_NOT_FOUND` | 对象不存在或不属于当前身份 | [api.md](api.md) |
| `20029` | 飞书上游回调白名单未配置 | [oidc-setup.md](oidc-setup.md) |

通用状态码：`400` 请求不合法、`401` 未认证、`403` Scope 不足、`404` 不存在、`409` 状态冲突、`500` 服务端错误、`503` 依赖未就绪。

## 哪些问题必须去外部平台处理

- 飞书 / 企业身份源的**回调白名单**、应用权限、成员信息读权限。
- IdP 的 RP 注册信息（redirect_uri、client secret、`sub` claim、PKCE 支持、签名密钥）。
- TLS 证书、DNS、企业级防火墙与出站网络策略。
- 目标系统自身的 RBAC、只读账号与凭据轮换。

这些改不了代码，排障时先确认外部配置能省大量时间。

## 求助时请带上

1. 症状与复现步骤、发生时间（含时区）；
2. 相关端点与状态码、`Location`、Cookie 名称（**不要贴 Cookie 值或任何 Secret**）；
3. 控制面与 Connector 的相关日志片段（已脱敏）；
4. 部署版本号 / 镜像 tag / 前端产物时间；
5. 最近是否做过升级、迁移、密钥轮换或权限变更。

## 下一步

- [operations.md](operations.md)：备份、升级与回滚
- [oidc-setup.md](oidc-setup.md)：SSO 完整配置
- [authorization.md](authorization.md)：权限规则
- [configuration.md](configuration.md)：变量核对
