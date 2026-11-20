# 配置与动作参考

> **面向**：部署者、开发者、运维、集成方与 Agent 开发者。用于查「某个变量是什么」与「系统支持哪些动作、默认怎么判」。
> 这是参考手册合集，按变量/动作分组列全；上手见 [quickstart.md](quickstart.md)，生产部署见 [guides.md#installation](guides.md#installation)。
> **权威来源**：配置读取分散在 `server/internal/{config,control,storage/postgres,policy}` 与 `server/cmd/`；冲突时以代码为准。

## 本篇导航

- [配置参考](#configuration)：环境变量、默认值、必填性、端口、健康检查、配置红线
- [动作与策略参考](#actions)：各目标系统动作清单、默认策略、变更安全契约

<a id="configuration"></a>

## 配置参考

> **面向**：部署者、开发者、运维。用于查「某个环境变量是什么、默认值多少、是否必填」。
> 这是参考手册，按变量分组列全；上手步骤见 [quickstart.md](quickstart.md)，生产部署见 [生产部署](guides.md#installation)，排障见 [troubleshooting.md](troubleshooting.md)。

**权威来源**：配置读取分散在 `server/internal/config/`、`server/internal/control/`、`server/internal/storage/postgres/`、`server/internal/policy/`、`server/cmd/`。本页是汇总；冲突时以代码为准。

### 本地配置约定

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
  url: postgres://localhost:5432/warden
  username: warden
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

### 控制面必需项

| 变量 | 默认值 | 说明 | 必填 |
|---|---|---|---|
| `WARDEN_DATABASE_URL` / `_FILE` | 无 | PostgreSQL DSN | 是 |
| `WARDEN_TASK_SIGNING_KEY` / `_FILE` | 无 | Connector TaskEnvelope 签名 Key，≥32 字节 | 是 |
| `WARDEN_JWT_SIGNING_KEY` / `_FILE` | 无 | Web 会话 JWT 签名 Key，≥32 字节，不得与任务 Key 相同 | 是 |
| `WARDEN_CREDENTIAL_ENCRYPTION_KEY` / `_FILE` | 无 | AES-GCM 加密每 Connector 任务 Key 的主密钥，≥32 字节 | 是 |
| `WARDEN_ADMIN_API_KEY` / `_FILE` | 空 | 管理端引导 Bearer Key，≥32 字节；为空时回退旧 `WARDEN_AGENT_API_KEY` | 是 |
| `WARDEN_AGENT_API_KEY` / `_FILE` | 空 | 旧版兼容变量，仅迁移期使用 | 条件 |
| `WARDEN_ADMIN_USERNAME` | `admin` | 启动管理员登录名，不含 `@`；`config.yaml` 通常通过 `admin.username` 显式提供，env-only 部署可省略；启动时同步到固定权限锚点 | 否 |
| `WARDEN_ADMIN_PASSWORD` / `_FILE` | 无 | 启动管理员密码哈希同步源；未配置会拒绝启动，不会回退默认账号或密码 | 是 |

### 运行、网络与会话

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WARDEN_PORT` | `8080` | 控制面 HTTP 监听端口 |
| `WARDEN_CORS_ORIGIN` | `http://localhost:4173` | 单个绝对 HTTP(S) origin，格式强校验 |
| `WARDEN_COOKIE_SECURE` | `false`（内置默认与随附 `config.yaml` 均为 `false`） | 会话 / CSRF / OIDC Cookie 的 Secure 标记；消费端按 `!= "false"` 判定，仅在完全不加载 `config.yaml` 的纯环境变量部署且未设置时视为 `true`。**生产必须显式设为 `true`** |
| `WARDEN_JWT_TTL_HOURS` | `8` | JWT 有效期（小时），必须为正整数 |
| `WARDEN_PUBLIC_CONTROL_URL` | 空 | 对外可访问的控制面地址；用于 UI 安装命令、Registration audience 推导、钉钉审批链接 |
| `WARDEN_REGISTRATION_AUDIENCE` | 由 `WARDEN_PUBLIC_CONTROL_URL` 推导，再退化为 `http://localhost:<WARDEN_PORT>/api/v1` | Registration Key 绑定的 audience；必须是绝对 HTTP(S)、路径为 `/api/v1`、无 user/query/fragment；与 `WARDEN_PUBLIC_CONTROL_URL` 同时配置时必须一致 |
| `WARDEN_TRUST_PROXY_HEADERS` | `false` | 位于受信任反向代理后设 `true`，用于登录/配对限流识别客户端 IP |
| `WARDEN_TRUSTED_PROXY_CIDRS` | 空 | 允许提供 `X-Forwarded-For` 的代理 CIDR，逗号分隔；使用代理头时必填 |
| `WARDEN_ARTIFACT_ROOT` | `.`（容器内 `/app-artifacts`） | 下载制品 / Helm Chart / 安装脚本根目录 |
| `WARDEN_RELEASE_VERSION` | 读 Chart `appVersion` | UI 展示的发布版本 |
| `WARDEN_ENABLE_LEGACY_CONNECTOR_KEY` | `false` | 旧共享 Connector Key 兼容开关；迁移完成后必须保持关闭 |
| `WARDEN_ENABLE_LEGACY_DISPATCH_RESULT` | `false` | 兼容不回传 dispatch token 的旧 Connector |
| `WARDEN_SA_DIR` | 空（用 InCluster 配置） | K8s Connector 本地覆盖 ServiceAccount 目录；与 `KUBERNETES_SERVICE_HOST/PORT` 配合 |

### 策略决策

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WARDEN_DECISION_MODE` | `embedded` | `remote` 启用独立 OPA；Provider 异常一律 DENY |
| `OPA_ENDPOINT` | `http://localhost:8181` | 远程 OPA 地址；强制 HTTPS，仅 loopback 允许 HTTP |
| `WARDEN_OPA_BEARER_TOKEN` / `_FILE` | 空 | 远程 OPA Bearer Token |
| `WARDEN_ALLOW_INSECURE_OPA` | `false` | 仅可信私网兼容无 TLS OPA |

### OIDC / SSO

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
| `WARDEN_OIDC_DEFAULT_TENANT_ID` | 空（`unaffiliated`） | 首次联邦建档在未唯一匹配邮箱域名时的默认租户；为空则保持 `unaffiliated`，显式设置为 ACTIVE 业务租户 slug 才启用默认租户兜底 | 否 |
| `WARDEN_OIDC_DEFAULT_ROLE` | `user` | 首次建档角色，仅允许 `user` / `tenant_admin`；**配置 `platform_admin` 会启动失败** | 否 |

启用但缺必填项时**启动失败并列出缺少的变量**，不会静默降级。scope 固定 `openid profile email`。接入步骤与踩坑见 [SSO 接入](guides.md#oidc)。

### 联邦断言登录

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WARDEN_FEDERATED_ASSERTION_PUBLIC_KEY` / `_FILE` | 空 | PEM 公钥（RSA/ECDSA/Ed25519）；未配置时端点返回 `FEDERATED_AUTH_NOT_CONFIGURED` |
| `WARDEN_FEDERATED_ASSERTION_ISSUER` | `https://idp.example.com` | 断言 issuer |
| `WARDEN_FEDERATED_ASSERTION_AUDIENCE` | `warden` | 断言 audience |

详见 [联邦授权](authorization.md#federation)。

### 钉钉（可选，成对校验）

| 变量（均支持 `_FILE`） | 约束 |
|---|---|
| `WARDEN_DINGTALK_WEBHOOK` + `WARDEN_DINGTALK_SECRET` | 成对出现；Webhook 必须 HTTPS 官方域名；Secret ≥16 字节；依赖 `WARDEN_PUBLIC_CONTROL_URL`。Webhook 指向部署级群，仅承载 platform 租户内容 |
| `WARDEN_DINGTALK_APP_KEY` + `WARDEN_DINGTALK_APP_SECRET` | 成对出现；Secret ≥16 字节。应用凭据部署级共享（与 OIDC 单 IdP 同构） |
| `WARDEN_DINGTALK_CARD_TEMPLATE_ID` + `WARDEN_DINGTALK_OPEN_CONVERSATION_ID` + `WARDEN_DINGTALK_ROBOT_CODE` | 三者必须同时配置，且依赖 CoolApp 配置。`OPEN_CONVERSATION_ID` 语义为 **platform 租户的审批群**兜底；业务租户必须在「钉钉审批」渠道设置中绑定本组织的审批群（`open_conversation_id`），一个群只能绑定一个租户（迁移 107 部分唯一索引），未绑群租户的卡片投递跳过、酷应用免登返回 `DINGTALK_GROUP_NOT_BOUND` |

### AI 只读助手（可选）

| 变量 | 默认值 / 约束 |
|---|---|
| `WARDEN_AI_ENABLED` | `false`（随附 `config.yaml` 显式关闭；不加载该文件时代码内置默认为 `true`）；设为 `false` 时接口返回 `AI_DISABLED` |
| `WARDEN_AI_MODEL` | 与 `WARDEN_AI_API_KEY` 成对配置；为空时返回 `AI_NOT_CONFIGURED` |
| `WARDEN_AI_BASE_URL` | `https://api.openai.com/v1`，可替换为 OpenAI 兼容服务地址 |
| `WARDEN_AI_API_KEY` / `_FILE` | 模型服务密钥，本地写入 `.env`；不会持久化到 AI 会话 |
| `WARDEN_AI_TIMEOUT` | `90s`，允许 `5s` 至 `5m` |
| `WARDEN_AI_MAX_TOKENS` | `1600`，允许 128 至 8192 |

当前只开放资源、执行和审计只读工具，模型配置不会扩大用户权限。SSE 反向代理必须关闭响应缓冲；
服务已发送 `X-Accel-Buffering: no`。

### 发布 / UI 覆盖项

未设置时读取随发布附带的 `deploy/helm/warden-operator/Chart.yaml` 与 `values.yaml`；Chart 不可用时 UI 会报错并禁止复制安装命令，不回退到写死的旧版本。

| 变量 | 说明 |
|---|---|
| `WARDEN_OPERATOR_CHART_REF` | UI Helm 命令使用的 Chart 路径 / 仓库 URL / OCI Reference，默认 `/downloads/warden-operator-chart.tgz` |
| `WARDEN_OPERATOR_IMAGE_REPOSITORY` / `WARDEN_OPERATOR_IMAGE_TAG` | UI Helm 命令使用的 Operator 镜像 |
| `WARDEN_CONNECTOR_IMAGE` | UI Helm 命令使用的完整 Connector 镜像及 Tag |
| `WARDEN_JENKINS_URL` / `WARDEN_JENKINS_USERNAME` / `WARDEN_JENKINS_API_TOKEN` / `WARDEN_JENKINS_ALLOWED_JOBS` | 旧环境变量式 Jenkins Runner；现已改为在 Connector 页面注册 Integration，不建议使用 |

### Connector / Operator 端变量

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

### 前端 dev 与脚本变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WARDEN_WEB_PORT` / `WARDEN_WEB_HOST` | `4173` / `localhost` | Vite dev server |
| `WARDEN_CONTROL_TARGET` | `http://localhost:8080` | Vite `/api` 代理目标 |
| `WARDEN_API_ENDPOINT` | 无（必填） | `scripts/warden-agent.sh` 与 `warden-registration.sh` 的 Warden 地址，须以 `/api/v1` 结尾 |
| `WARDEN_AGENT_API_KEY` | 可选 | Agent 脚本的 Bearer Key |
| `WARDEN_REGISTRATION_API_KEY` | 可选 | 注册脚本的 `warden_rgk_` Key |
| `WARDEN_ACTION_VERSION` / `WARDEN_TRACE_ID` / `WARDEN_IDEMPOTENCY_KEY` | 按动作默认 / 空 / 空 | Agent 脚本调用参数 |
| `WARDEN_PORT` | `8080` | 本地启动脚本控制面端口 |
| `WARDEN_CORS_ORIGIN` | `http://localhost:4173` | 本地启动脚本设置的 CORS |

### 测试变量

| 变量 | 说明 |
|---|---|
| `WARDEN_TEST_DATABASE_URL` | opt-in PostgreSQL 集成测试；未设置时相关测试 `t.Skip` |
| `WARDEN_TEST_DATABASE_ENDPOINT` / `_USERNAME` / `_PASSWORD` | Connector 数据库集成测试 |
| `WARDEN_LOCAL_FIXTURE_CONNECTOR` / `WARDEN_KUBERNETES_TEST_KUBECONFIG` / `WARDEN_TEST_TASK_SIGNING_KEY_FILE` | K8s 变更集成测试 opt-in |
| `WARDEN_LOCAL_FIXTURE_DURATION` | 本地 fixture 测试运行时长（Go duration，如 `10m`）；默认 `10m` |

### 默认端口速查

| 端口 | 用途 |
|---|---|
| `8080` | `warden-control`（`WARDEN_PORT`）；也是前端 nginx 容器内部端口 |
| `4173` | Vite dev server（`WARDEN_WEB_PORT`） |
| `5433` | 本地 compose 映射的 PostgreSQL（容器内 5432） |
| `8181` | 可选独立 OPA |
| `8200` | 可选 OpenBao |
| `8090` / `8081` | 本地 OIDC 联调时的控制面 / Vite 端口（在 `config.yaml` 配置） |

### 健康检查

| 路径 | 位置 | 说明 |
|---|---|---|
| `/api/v1/health` | warden-control | 就绪检查（公开） |
| `/livez` | warden-control | 存活检查（公开） |
| `/readyz` | warden-control | 就绪检查（公开） |
| `/healthz` | 前端 nginx 容器 | 静态站点存活检查，**后端没有这个路由** |

### 配置红线

1. 生产 `WARDEN_COOKIE_SECURE` 必须为 `true`，且全站 HTTPS。
2. 四个必需 Secret 必须独立随机生成、分别存放，禁止复用。
3. `WARDEN_OIDC_DEFAULT_ROLE` 生产保持 `user`；普通账号只能通过管理入口指派为 `user` / `tenant_admin`，不能指派 `platform_admin`。
4. `WARDEN_ENABLE_LEGACY_*` 兼容开关默认关闭，迁移完成后不得重新打开。
5. 反向代理后才启用 `WARDEN_TRUST_PROXY_HEADERS`，并同时限定 `WARDEN_TRUSTED_PROXY_CIDRS`。
6. 不要在文档、日志、Issue 或命令历史里放真实 Secret；示例一律用 `<placeholder>`。


---

<a id="actions"></a>

## 动作与策略参考

> **面向**：集成方、Agent 开发者、策略维护者。用于查「系统支持哪些动作、版本多少、默认是否需要审批」。
> 这是参考手册。概念见 [concepts.md](concepts.md)，接口见 [api.md](api.md)，接入目标系统见 [接入指南](guides.md#onboarding)。

### 通用规则

- 动作用 `name@vN` 标识，例如 `deployment.update_image@v2`。控制面用 `SupportsAction(capabilities, action, version)` 精确匹配；版本不匹配返回 `CONNECTOR_UPGRADE_REQUIRED`。
- 参数按 `ActionDefinition.ParameterSchema` 校验，目标资源身份由所选 Warden 资源固定，不由调用方随意指定。
- 内嵌 Rego 用**显式动作集合**判定，默认 `DENY`。策略服务异常同样 DENY。
- **不开放任意 Shell、原始 SQL、任意 `kubectl`、任意 HTTP/API 透传**。
- 生产环境的变更动作默认进入审批；`UNKNOWN` 结果只做只读状态重读，不自动重放。

查询某个资源当前可用动作：

```bash
# API
curl -H "Authorization: Bearer <agent-api-key>" \
  "<warden-api-endpoint>/api/v1/resources/<resource_id>/actions"

# 客户端脚本
scripts/warden-agent.sh list-actions <resource_id>
```

### Kubernetes

| 动作 | 版本 | 说明 |
|---|---|---|
| `deployment.get` | v1 | 读取 Deployment 受限摘要 |
| `deployment.update_image` | v2 | 固定目标容器和镜像仓库，校验 `expectedGeneration` 后更新镜像 |
| `deployment.restart` / `deployment.scale` | v1 | 校验 `expectedGeneration` 后滚动重启或设置 0–1000 副本 |
| `deployment.rollout_status` | v1 | 查询副本和 Generation 状态 |
| `statefulset.restart` / `statefulset.scale` / `statefulset.update_image` | v1 | 固定 StatefulSet、Namespace 和目标容器的结构化变更 |
| `cronjob.suspend` / `cronjob.resume` / `cronjob.trigger` | v1 | 暂停/恢复调度，或按 Execution ID 幂等创建一次性 Job |
| `pod.logs.read` | v1 | 读取 Pod 日志（尾行限制） |
| `pod.metrics` / `node.metrics` | v1 | 通过 `metrics.k8s.io` 读单 Pod/Node 的 CPU 与内存实时用量 |
| `namespace.dashboard_configmap.apply` | v1 | 仅创建/更新 `grafana-dashboard-*` 看板 ConfigMap（需审批） |
| `configmap.dashboard.delete` | v1 | 仅删除 `grafana-dashboard-*` 看板 ConfigMap（需审批） |
| `kubernetes.resource.get` / `list` | v1 | 在 Connector 与 Namespace 固定范围内读取允许的 GVK；只读免审批 |
| `kubernetes.resource.preview` | v1 | 对 create/update 做 server-side dry-run，返回净化后的 before/after 与 `previewHash` |
| `kubernetes.resource.create` / `update` / `delete` | v1 | 对受治理 GVK 执行 CRUD（ConfigMap、Service、Deployment、StatefulSet、DaemonSet、Job、CronJob、Ingress、NetworkPolicy、PodDisruptionBudget） |

**通用写操作不是任意 API 透传**：GVK 采用固定安全白名单，Secret、RBAC、CRD、Webhook 与集群级资源默认拒绝；create/update 必须先 dry-run 并校验 `previewHash`；update/delete 要求当前 `resourceVersion`；delete 需输入 `Kind/Namespace/Name` 并选择传播策略。Manifest 最大 1 MiB，敏感字段被拒绝，结果移除 `managedFields` 与执行注解并过滤敏感值。生产写操作强制审批，超时进入 `UNKNOWN`。

### Linux 主机

| 动作 | 版本 | 资源类型 | 说明 |
|---|---|---|---|
| `linux.host.info` | v1 | `linux_host` | 操作系统、内核、运行时间 |
| `linux.process.list` | v1 | `linux_host` | 进程列表（按 CPU 排序，可限条数） |
| `linux.disk.usage` | v1 | `linux_host` | 文件系统容量与使用率 |
| `systemd.service.status` | v1 | `systemd_service` | systemd 服务状态属性 |
| `systemd.service.logs` | v1 | `systemd_service` | journal 日志（可限行数） |
| `docker.container.list` | v1 | `linux_host` | 列出白名单内容器 |
| `docker.container.inspect` | v1 | `docker_container` | 容器 ID、名称、镜像、状态、启动时间、重启次数 |
| `docker.container.logs` | v1 | `docker_container` | 末尾日志，最多 1000 行 / 约 1 MiB |
| `systemd.service.start` / `stop` / `restart` | v1 | `systemd_service` | 已发现且在白名单内的服务 |
| `docker.container.start` / `stop` / `restart` | v1 | `docker_container` | 按发现时的固定容器 ID 操作白名单容器 |

主机查询与 systemd 只读动作由**非 root** Connector 用固定参数的 `exec.CommandContext` 执行；生命周期动作经 `root:warden-linux 0660` 的 Unix Socket 交给 root Helper。Docker Socket 不授予 `warden-linux` 用户。生命周期动作只接受固定枚举与已发现的稳定资源标识，执行前后实时读状态并写 `_warden_safety` 审计摘要。不提供任意命令、Shell、`docker run/exec/remove`、镜像拉取或参数透传。

> 已知限制：`docker.container.list` 当前只返回运行中容器，不等同于 `docker ps -a`；日志结果目前只有行数/大小限制，尚未做敏感信息检测。

### Jenkins

| 资源类型 | 查询动作 | 变更动作（均需审批） |
|---|---|---|
| `jenkins_controller` | `jenkins.controller.get` | `jenkins.job.create` |
| `jenkins_job` | `jenkins.job.get`、`jenkins.job.config.get` | `jenkins.job.update`、`jenkins.job.configure`、`jenkins.job.delete`、`jenkins.job.enable`、`jenkins.job.disable`、`jenkins.job.build` |
| `jenkins_build` | `jenkins.build.get`、`jenkins.build.logs` | `jenkins.build.stop`、`jenkins.build.retry` |
| `jenkins_queue_item` | `jenkins.queue.get` | `jenkins.queue.cancel` |
| `jenkins_node` | `jenkins.node.get` | `jenkins.node.set_offline` |
| `jenkins_credential_reference` | `jenkins.credential_reference.get` | — |

`jenkins.job.create` 仅创建新 Job，同名返回 `JENKINS_JOB_CONFLICT`；`update` 明确改 inline Pipeline Script 或 SCM 仓库/分支/Jenkinsfile 路径；`configure` 只改 `description` 与 `quietPeriod`；`delete` 审批后还需输入完整 Job 名二次确认。不支持 rename/move，不暴露原始 Jenkins API。SCM 只接受 Credential ID 引用，凭据仅同步 ID、类型与描述，任何 Secret 都不会返回。

Integration 在「Connector → 查看详情 → Integrations」注册；资源目录只展示 Connector 随后发现并同步的业务资源。

### Grafana

Folder、Dashboard 的结构化查询与 CRUD，以及 Datasource 列表、查询和无 Secret 的 Prometheus/Loki Datasource CRUD；所有变更进入审批。Folder/Dashboard/Datasource 更新会先用只读动作取当前配置回填表单，更新目标 UID 固定为所选资源；Folder 更新携带当前 `version`，由 Grafana 乐观锁阻止并发覆盖。`secureJsonData`、密码、Token、API Key 与 Authorization 字段不会进入普通动作、Execution 参数、审计或数据库。

### 数据库

支持实例、Schema、Table/View 元数据读取，以及 `database.table.rows.query` 结构化单表行查询：

- 必须显式选择不超过 20 个真实字段；
- 只支持 AND 条件、有限排序、最多 100 行、1 MiB 结果；
- Connector 根据实时表结构校验标识符并参数化所有值；
- **不接受原始 SQL、JOIN、跨表查询、DML、DDL 或 dump**；生产仍应使用数据库原生只读账号。

该动作默认直接执行并完整审计，不进人工审批；原始行结果仅保留 10 分钟，Warden 长期保留的是身份、目标、参数、终态与审计，不作为业务数据副本。

### 华为云 / 阿里云 SLS

- **华为云**：账号级 Integration，AK/SK 只保存在 Connector 目标侧。Endpoint 作为种子 Region，默认发现 IAM 用户可访问的全部 Region，也可指定 Region 或排除列表。资源目录将 ECS/RDS/ELB/OBS/LTS 平级展示；LTS 自动发现日志组和日志流，查询动作挂载日志流，单次最多 500 条 / 4 MiB。ECS 支持创建、开机、软关机、软重启、治理标签/备注更新与删除，所有变更固定账号/Region/资源 ID 并进入审批；删除需完整 ECS 名称二次确认。RDS/ELB/OBS 创建暂不开放。
- **阿里云 SLS**：RAM 账号级 Integration，只需一个种子 Region。`scope.regionPolicy` 支持 `single` / `all` / `allowlist`；`all` 会并发探测官方 Region Endpoint 并用 `ListProject` 只保留 RAM 有权访问的 Project。资源目录按「账号 → Region → Project → Logstore」展示，查询动作挂载 Logstore。查询单次最多 500 条 / 4 MiB。不开放任意阿里云 API 或 CLI。

云日志原始结果同样只保留 10 分钟，长期日志仍保存在云平台。

### H3C 网络设备

`warden-network-connector` 提供设备、接口、VLAN 发现；设备健康、LLDP、链路聚合、STP、端口、MAC/ARP 查询。`network.device.audit` 支持按设备角色执行固定的 `standard` / `security` / `core` / `router` / `ac` / `aggregation` / `switch` / `poe` 只读审计 Profile，返回敏感行净化后的命令分段、风险提示与角色摘要。

交换机接口描述、Access VLAN、Trunk VLAN 变更**默认关闭**；开启后仍须经过实时预检、审批、固定命令执行与写后复核，不开放任意 CLI。凭据引用只能指向 `warden:` 托管凭据或 Connector 本地凭据目录中的文件名，并校验预登记的 Host Key SHA256 指纹。

### macOS / Windows 主机

`warden-host-connector` 的 `host.command.run` 允许调用方提供 program + args，但：

- program 必须落在 `allowedCommands` 白名单，路径与 Shell 元字符被拒绝；
- 参数有数量与长度限制，环境变量裁剪为白名单；
- 用 `exec.CommandContext(program, args...)` 直接执行，**没有 `/bin/sh -c`**；
- Rego 对该动作强制 `REQUIRE_APPROVAL`。

这是「不提供任意 Shell」口径下唯一需要特别说明的特例。

### 默认策略行为（内嵌 Rego）

| 环境 | 动作 | 决策 |
|---|---|---|
| 开发 / 测试 | Kubernetes 已注册的查询及结构化工作负载变更 | `ALLOW` |
| 生产 | Kubernetes 只读动作 | `ALLOW` |
| 生产 | Deployment / StatefulSet / CronJob 结构化变更 | `REQUIRE_APPROVAL` |
| 开发 / 测试 | systemd / Docker 生命周期动作 | `ALLOW` |
| 生产 | systemd / Docker 生命周期动作 | `REQUIRE_APPROVAL` |
| 任意环境 | Dashboard ConfigMap 变更 | `REQUIRE_APPROVAL` |
| 任意环境 | Jenkins 查询 / 变更 | `ALLOW` / `REQUIRE_APPROVAL` |
| 任意环境 | Grafana 查询 / 变更 | `ALLOW` / `REQUIRE_APPROVAL` |
| 任意环境 | 华为云、阿里 SLS 查询 | `ALLOW` |
| 其他 | 任意动作 | `DENY` |

管理端「策略中心」提供 Rego 之上的可视化策略矩阵，可按项目、资源类型、动作、环境、风险等级与执行身份配置自动放行 / 人工审批 / 拒绝，并提供优先级、启停、复制、冲突与遮蔽检测、命中统计和策略模拟。**矩阵不能覆盖 Rego 的 `DENY`**，可视化配置不会绕过默认拒绝边界。

### 变更安全契约

变更动作使用版本化契约 `warden.change-safety@v1`：

- 生产变更不可被策略矩阵放宽为免审批；
- Connector 在执行前后读取资源状态并返回 `_warden_safety` 摘要；
- 可回滚动作标记 `MANUAL`，不可逆动作标记 `NOT_SUPPORTED`；
- `UNKNOWN` 只触发只读状态重读，不自动重放变更。

### 项目与环境归属

每个服务 / 中间件资源同时具有项目与环境两个治理维度：

- 普通资源按「单资源覆盖 → Integration 默认环境 → Connector 环境」计算；
- 数据库按「表覆盖 → Schema 覆盖 → 实例覆盖 → Integration → Connector」继承；
- Integration 项目归属与执行通道项目解耦：执行通道只决定任务在哪里执行；
- `consumer-projects` 可为同一资源追加其他项目的查看/触发权限，但不授予配置或删除治理权。
