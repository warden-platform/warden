# 配置参考

> **面向**：部署者、开发者、运维。用于查「某个环境变量是什么、默认值多少、是否必填」。
> 这是参考手册，按变量分组列全；上手步骤见 [quickstart.md](quickstart.md)，生产部署见 [installation.md](installation.md)，排障见 [troubleshooting.md](troubleshooting.md)。

**权威来源**：配置读取分散在 `server/internal/config/`、`server/internal/control/`、`server/internal/storage/postgres/`、`server/internal/policy/`、`server/cmd/`。本页是汇总；冲突时以代码为准。

## 本地配置约定

`server/internal/config/secret.go` 提供统一的 `Secret(name)` / `RequiredSecret(name, minLength)`：

- 每个敏感项支持 `NAME`（明文）与 `NAME_FILE`（文件路径）两种写法，**两者同时设置会直接报错**。
- `_FILE` 文件必须存在、可读、内容 trim 后非空。
- 本地开发将用户提供的外部凭据写入根目录 `.env`；生产可由 Secret 管理系统通过 `*_FILE` 挂载。
- 控制面启动时校验 4 个必需 Secret：**任务签名 Key、JWT 签名 Key、凭据加密 Key、Admin API Key 各 ≥32 字节且两两不同**，否则启动失败。
- 管理员密码 8–72 字节，bcrypt cost 12；同一配置不能同时设明文与 `*_FILE`。

| 文件 | 内容 | 是否提交 |
|---|---|---|
| `config.yaml` | 端口、URL、管理员用户名、OIDC 地址、AI 模型等非敏感配置 | 是 |
| `.env` | 管理员密码、数据库密码、AI/OIDC 等外部凭据，也可放本机配置覆盖项 | **否** |
| `.env.example` | 只有占位值的 `.env` 模板 | 是 |
| `var/warden/secrets.json` | 初始化生成的四个内部签名/加密密钥，用户无需编辑 | **否** |

首次安装运行 `scripts/init-local-secrets.sh` 生成内部密钥。该命令拒绝覆盖已有文件；
`server/internal/config/config.go` 负责读取 `config.yaml`、`.env` 和 `var/warden/secrets.json`，
并把结果映射为后端现有的 `WARDEN_*` 环境变量。`scripts/start-local-control.sh` 只负责启动二进制，
不再做配置翻译，也不会生成或改写密钥。
**优先级：进程环境变量 > `.env` > `config.yaml` > 内置默认。** Vite 同样读取 `config.yaml` 与根目录 `.env`。
`config.yaml` 可以不存在：此时控制面完全由 `WARDEN_*` 环境变量驱动（生产部署的遗留契约，
镜像内不打包 `config.yaml`）。注意 `oidc.enabled` 是显式开关——文件里写 `false` 会强制关闭 SSO；
删除整个 `oidc` 段则保留「配了 issuer 即启用」的遗留行为。
从 `.env.example` 创建 `.env` 并替换所有占位值，权限建议设为 `0600`。不要把真实密钥写入
`config.yaml`、`.env.example`、源码或启动脚本。

YAML 使用面向部署者的分组和小写字段，不直接暴露后端的 `WARDEN_*` 环境变量：

```yaml
server:
  port: 8080
  public_url: http://localhost:8080
  cors_origin: http://localhost:4173
  cookie_secure: false

database:
  url: postgres://localhost:5432/pam
  username: pam
  password: ${WARDEN_DATABASE_PASSWORD}

admin:
  username: admin
  password: ${WARDEN_ADMIN_PASSWORD}

web:
  host: localhost
  port: 4173
  control_target: http://localhost:8080

oidc:
  enabled: false

ai:
  enabled: false
  model: ""
  base_url: https://api.openai.com/v1
  timeout: 90s
  max_tokens: 1600
```

加载器再将这些字段映射为后端现有的环境变量。下方变量表描述内部接口和生产部署方式，
本地用户只需维护上述 YAML 结构。

`database.url` 不包含凭据；加载器会组合 `username` 和 `.env` 中的密码生成后端使用的 DSN。
生产部署仍可使用 `WARDEN_DATABASE_URL_FILE`，它与 `WARDEN_DATABASE_URL` 不能同时设置。

Compose 通过 `docker compose --env-file .env -f deploy/docker-compose.yaml ...` 读取
`WARDEN_DATABASE_PASSWORD`；可选 OpenBao 使用 `WARDEN_OPENBAO_ROOT_TOKEN`。其他集成密钥见对应章节。

---

## 控制面必需项

| 变量 | 默认值 | 说明 | 必填 |
|---|---|---|---|
| `WARDEN_DATABASE_URL` / `_FILE` | 无 | PostgreSQL DSN | 是 |
| `WARDEN_TASK_SIGNING_KEY` / `_FILE` | 无 | Connector TaskEnvelope 签名 Key，≥32 字节 | 是 |
| `WARDEN_JWT_SIGNING_KEY` / `_FILE` | 无 | Web 会话 JWT 签名 Key，≥32 字节，不得与任务 Key 相同 | 是 |
| `WARDEN_CREDENTIAL_ENCRYPTION_KEY` / `_FILE` | 无 | AES-GCM 加密每 Connector 任务 Key 的主密钥，≥32 字节 | 是 |
| `WARDEN_ADMIN_API_KEY` / `_FILE` | 空 | 管理端引导 Bearer Key，≥32 字节；为空时回退旧 `WARDEN_AGENT_API_KEY` | 是 |
| `WARDEN_AGENT_API_KEY` / `_FILE` | 空 | 旧版兼容变量，仅迁移期使用 | 条件 |
| `WARDEN_ADMIN_USERNAME` | 无 | 启动管理员登录名，不含 `@`；启动时同步到固定权限锚点 | 是 |
| `WARDEN_ADMIN_PASSWORD` / `_FILE` | 无 | 启动管理员密码哈希同步源；未配置会拒绝启动，不会回退默认账号或密码 | 是 |

## 运行、网络与会话

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WARDEN_PORT` | `8080` | 控制面 HTTP 监听端口 |
| `WARDEN_CORS_ORIGIN` | `http://localhost:4173` | 单个绝对 HTTP(S) origin，格式强校验 |
| `WARDEN_COOKIE_SECURE` | `true`（仅显式 `false` 才关闭） | 会话 / CSRF / OIDC Cookie 的 Secure 标记。**生产必须保持 true** |
| `WARDEN_JWT_TTL_HOURS` | `8` | JWT 有效期（小时），必须为正整数 |
| `WARDEN_PUBLIC_CONTROL_URL` | 空 | 对外可访问的控制面地址；用于 UI 安装命令、Registration audience 推导、钉钉审批链接 |
| `WARDEN_REGISTRATION_AUDIENCE` | 由 `WARDEN_PUBLIC_CONTROL_URL` 推导，再退化为 `http://localhost:<WARDEN_PORT>/api/v1` | Registration Key 绑定的 audience；必须是绝对 HTTP(S)、路径为 `/api/v1`、无 user/query/fragment；与 `WARDEN_PUBLIC_CONTROL_URL` 同时配置时必须一致 |
| `WARDEN_TRUST_PROXY_HEADERS` | `false` | 位于受信任反向代理后设 `true`，用于登录/配对限流识别客户端 IP |
| `WARDEN_TRUSTED_PROXY_CIDRS` | 空 | 允许提供 `X-Forwarded-For` 的代理 CIDR，逗号分隔；使用代理头时必填 |
| `WARDEN_ARTIFACT_ROOT` | `.`（容器内 `/app-artifacts`） | 下载制品 / Helm Chart / 安装脚本根目录 |
| `WARDEN_RELEASE_VERSION` | 读 Chart `appVersion` | UI 展示的发布版本 |
| `WARDEN_ENABLE_LEGACY_CONNECTOR_KEY` | `false` | 旧共享 Connector Key 兼容开关；迁移完成后必须保持关闭 |
| `WARDEN_ENABLE_LEGACY_DISPATCH_RESULT` | `false` | 兼容不回传 dispatch token 的旧 Connector |
| `WARDEN_OUTBOX_ENABLED` | `false` | 启用事务 Outbox 写入；仅在部署了消费者时开启 |
| `WARDEN_SA_DIR` | 空（用 InCluster 配置） | K8s Connector 本地覆盖 ServiceAccount 目录；与 `KUBERNETES_SERVICE_HOST/PORT` 配合 |

## 策略决策

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WARDEN_DECISION_MODE` | `embedded` | `remote` 启用独立 OPA；Provider 异常一律 DENY |
| `OPA_ENDPOINT` | `http://localhost:8181` | 远程 OPA 地址；强制 HTTPS，仅 loopback 允许 HTTP |
| `WARDEN_OPA_BEARER_TOKEN` / `_FILE` | 空 | 远程 OPA Bearer Token |
| `WARDEN_ALLOW_INSECURE_OPA` | `false` | 仅可信私网兼容无 TLS OPA |

## OIDC / SSO

`WARDEN_OIDC_ENABLED=true` 是显式总开关；`=false` 强制关闭（即使配了 issuer）。**不设置该开关时沿用旧契约**：配置了 `WARDEN_OIDC_ISSUER` 即启用，为空则完全禁用——存量部署无需任何改动。禁用时登录页不显示 SSO 入口；discovery 在首次请求时才做，启动不依赖 IdP 可用。

最小启用配置只需 4 行（其余走默认）：

```text
WARDEN_OIDC_ENABLED=true
WARDEN_OIDC_ISSUER=https://<idp.example.com>
WARDEN_OIDC_CLIENT_ID=<client-id>
WARDEN_OIDC_CLIENT_SECRET_FILE=/etc/warden/secrets/oidc_client_secret
```

| 变量 | 默认值 | 说明 | 启用时必填 |
|---|---|---|---|
| `WARDEN_OIDC_ENABLED` | 空（沿用 issuer 契约） | 显式开关：`true` 启用 / `false` 强制关闭；非法值启动失败 | 否 |
| `WARDEN_OIDC_ISSUER` | 空 | IdP 地址，需可访问 `/.well-known/openid-configuration` | 是 |
| `WARDEN_OIDC_CLIENT_ID` | 空 | IdP 注册的 client id | 是 |
| `WARDEN_OIDC_CLIENT_SECRET` / `_FILE` | 空 | client secret；两者不可同时设置 | 是 |
| `WARDEN_OIDC_REDIRECT_URL` | 由 `WARDEN_PUBLIC_CONTROL_URL` 推导 | 回调地址，必须与 IdP 白名单**逐字符一致**；服务端据此推导规范 origin。不填时自动取 `<PUBLIC_CONTROL_URL>/api/v1/auth/oidc/callback` | 否（二者至少配一） |
| `WARDEN_OIDC_DEFAULT_TENANT_ID` | `platform` | 联邦身份默认租户 slug，外键指向 `tenants(slug)` | 否 |
| `WARDEN_OIDC_DEFAULT_ROLE` | `user` | 首次建档角色，仅允许 `user` / `tenant_admin`；**配置 `platform_admin` 会启动失败** | 否 |

启用但缺必填项时**启动失败并列出缺少的变量**，不会静默降级。scope 固定 `openid profile email`。接入步骤与踩坑见 [oidc-setup.md](oidc-setup.md)。

## 联邦断言登录

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WARDEN_FEDERATED_ASSERTION_PUBLIC_KEY` / `_FILE` | 空 | PEM 公钥（RSA/ECDSA/Ed25519）；未配置时端点返回 `FEDERATED_AUTH_NOT_CONFIGURED` |
| `WARDEN_FEDERATED_ASSERTION_ISSUER` | `https://idp.example.com` | 断言 issuer |
| `WARDEN_FEDERATED_ASSERTION_AUDIENCE` | `pam` | 断言 audience |

详见 [federated-authorization.md](federated-authorization.md)。

## 钉钉（可选，成对校验）

| 变量（均支持 `_FILE`） | 约束 |
|---|---|
| `WARDEN_DINGTALK_WEBHOOK` + `WARDEN_DINGTALK_SECRET` | 成对出现；Webhook 必须 HTTPS 官方域名；Secret ≥16 字节；依赖 `WARDEN_PUBLIC_CONTROL_URL` |
| `WARDEN_DINGTALK_APP_KEY` + `WARDEN_DINGTALK_APP_SECRET` | 成对出现；Secret ≥16 字节 |
| `WARDEN_DINGTALK_CARD_TEMPLATE_ID` + `WARDEN_DINGTALK_OPEN_CONVERSATION_ID` + `WARDEN_DINGTALK_ROBOT_CODE` | 三者必须同时配置，且依赖 CoolApp 配置 |

## AI 只读助手（可选）

| 变量 | 默认值 / 约束 |
|---|---|
| `WARDEN_AI_ENABLED` | `true`；设为 `false` 时接口返回 `AI_DISABLED` |
| `WARDEN_AI_MODEL` | 与 `WARDEN_AI_API_KEY` 成对配置；为空时返回 `AI_NOT_CONFIGURED` |
| `WARDEN_AI_BASE_URL` | `https://api.openai.com/v1`，可替换为 OpenAI 兼容服务地址 |
| `WARDEN_AI_API_KEY` / `_FILE` | 模型服务密钥，本地写入 `.env`；不会持久化到 AI 会话 |
| `WARDEN_AI_TIMEOUT` | `90s`，允许 `5s` 至 `5m` |
| `WARDEN_AI_MAX_TOKENS` | `1600`，允许 128 至 8192 |

当前只开放资源、执行和审计只读工具，模型配置不会扩大用户权限。SSE 反向代理必须关闭响应缓冲；
服务已发送 `X-Accel-Buffering: no`。

## 发布 / UI 覆盖项

未设置时读取随发布附带的 `deploy/helm/warden-operator/Chart.yaml` 与 `values.yaml`；Chart 不可用时 UI 会报错并禁止复制安装命令，不回退到写死的旧版本。

| 变量 | 说明 |
|---|---|
| `WARDEN_OPERATOR_CHART_REF` | UI Helm 命令使用的 Chart 路径 / 仓库 URL / OCI Reference，默认 `/downloads/warden-operator-chart.tgz` |
| `WARDEN_OPERATOR_IMAGE_REPOSITORY` / `WARDEN_OPERATOR_IMAGE_TAG` | UI Helm 命令使用的 Operator 镜像 |
| `WARDEN_CONNECTOR_IMAGE` | UI Helm 命令使用的完整 Connector 镜像及 Tag |
| `WARDEN_JENKINS_URL` / `WARDEN_JENKINS_USERNAME` / `WARDEN_JENKINS_API_TOKEN` / `WARDEN_JENKINS_ALLOWED_JOBS` | 旧环境变量式 Jenkins Runner；现已改为在 Connector 页面注册 Integration，不建议使用 |

## Connector / Operator 端变量

| 变量 | 默认值 / 约束 | 适用 |
|---|---|---|
| `WARDEN_CONTROL_URL` | K8s `http://warden-control:8080`；Linux `http://127.0.0.1:8080`；Host `https://127.0.0.1:8080`；Network / Operator 必填 | 全部 Connector |
| `WARDEN_TASK_SIGNING_KEY` / `_FILE` | ≥32 字节，配对后注入的每 Connector 独立 Key | 全部 Connector |
| `WARDEN_CONNECTOR_NAME` | `kubernetes-connector` / `h3c-network-connector` | K8s / Network |
| `WARDEN_CONNECTOR_ENVIRONMENT` | `testing`（安装脚本可设为 `production`） | 全部 |
| `WARDEN_CONNECTOR_NAMESPACE` | `warden-system` | K8s |
| `WARDEN_KUBERNETES_ENABLED` | 必须显式 `true` | K8s Connector |
| `WARDEN_ALLOWED_NAMESPACES` | 必填，逗号分隔，禁止 `*` | K8s Connector |
| `WARDEN_ALLOWED_IMAGE_PREFIXES` | 必填 | K8s Connector |
| `WARDEN_EXECUTION_SERVICE_ACCOUNT` | `warden-executor` | K8s Connector |
| `WARDEN_KUBERNETES_TOKEN_AUDIENCE` | `https://kubernetes.default.svc` | K8s Connector |
| `WARDEN_LINUX_STATE_FILE` | `/var/lib/warden-linux/state.json` | Linux Connector |
| `WARDEN_LINUX_CREDENTIAL_DIR` | `/etc/warden-linux/credentials` | Linux Connector |
| `WARDEN_LINUX_HELPER_SOCKET` | `/run/warden-linux/helper.sock` | Linux Helper |
| `WARDEN_NETWORK_CREDENTIAL_DIR` | `/var/lib/warden-network/credentials` | Network Connector |
| `WARDEN_HOST_STATE_FILE` | 平台默认 | Host Connector |
| `WARDEN_OPERATOR_NAMESPACE` / `WARDEN_OPERATOR_ID` | `warden-system` / `warden-operator-<namespace>` | Operator |
| `WARDEN_CONNECTOR_IMAGE` / `WARDEN_IMAGE_PULL_SECRET` | Operator 必填 / 可选 | Operator |
| `WARDEN_FOREGROUND` | 空；设为 `1` 让本地启动脚本前台运行 | 本地脚本 |

## 前端 dev 与脚本变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WARDEN_WEB_PORT` / `WARDEN_WEB_HOST` | `4173` / `localhost` | Vite dev server |
| `WARDEN_CONTROL_TARGET` | `http://localhost:8080` | Vite `/api` 代理目标 |
| `WARDEN_API_ENDPOINT` | 无（必填） | `scripts/warden-agent.sh` 与 `pam-registration.sh` 的 Warden 地址，须以 `/api/v1` 结尾 |
| `WARDEN_AGENT_API_KEY` | 可选 | Agent 脚本的 Bearer Key |
| `WARDEN_REGISTRATION_API_KEY` | 可选 | 注册脚本的 `pam_rgk_` Key |
| `WARDEN_ACTION_VERSION` / `WARDEN_TRACE_ID` / `WARDEN_IDEMPOTENCY_KEY` | 按动作默认 / 空 / 空 | Agent 脚本调用参数 |
| `WARDEN_PORT` | `8080` | 本地启动脚本控制面端口 |
| `WARDEN_CORS_ORIGIN` | `http://localhost:4173` | 本地启动脚本设置的 CORS |

## 测试变量

| 变量 | 说明 |
|---|---|
| `WARDEN_TEST_DATABASE_URL` | opt-in PostgreSQL 集成测试；未设置时相关测试 `t.Skip` |
| `WARDEN_TEST_DATABASE_ENDPOINT` / `_USERNAME` / `_PASSWORD` | Connector 数据库集成测试 |
| `WARDEN_LOCAL_FIXTURE_CONNECTOR` / `WARDEN_KUBERNETES_TEST_KUBECONFIG` / `WARDEN_TEST_TASK_SIGNING_KEY_FILE` | K8s 变更集成测试 opt-in |

## 默认端口速查

| 端口 | 用途 |
|---|---|
| `8080` | `warden-control`（`WARDEN_PORT`）；也是前端 nginx 容器内部端口 |
| `4173` | Vite dev server（`WARDEN_WEB_PORT`） |
| `5433` | 本地 compose 映射的 PostgreSQL（容器内 5432） |
| `8181` | 可选独立 OPA |
| `8200` | 可选 OpenBao |
| `8090` / `8081` | 本地 OIDC 联调时的控制面 / Vite 端口（在 `config.yaml` 配置） |

## 健康检查

| 路径 | 位置 | 说明 |
|---|---|---|
| `/api/v1/health` | warden-control | 就绪检查（公开） |
| `/livez` | warden-control | 存活检查（公开） |
| `/readyz` | warden-control | 就绪检查（公开） |
| `/healthz` | 前端 nginx 容器 | 静态站点存活检查，**后端没有这个路由** |

## 配置红线

1. 生产 `WARDEN_COOKIE_SECURE` 必须为 `true`，且全站 HTTPS。
2. 四个必需 Secret 必须独立随机生成、分别存放，禁止复用。
3. `WARDEN_OIDC_DEFAULT_ROLE` 生产保持 `user`；超级管理员只能在页面手工指派。
4. `WARDEN_ENABLE_LEGACY_*` 兼容开关默认关闭，迁移完成后不得重新打开。
5. 反向代理后才启用 `WARDEN_TRUST_PROXY_HEADERS`，并同时限定 `WARDEN_TRUSTED_PROXY_CIDRS`。
6. 不要在文档、日志、Issue 或命令历史里放真实 Secret；示例一律用 `<placeholder>`。

## 下一步

- [installation.md](installation.md)：生产部署清单
- [oidc-setup.md](oidc-setup.md)：SSO 接入
- [operations.md](operations.md)：密钥轮换与迁移
- [troubleshooting.md](troubleshooting.md)：启动失败 / 401 / state 校验失败
