# 操作指南：部署到正式环境

> **面向**：要把 Warden Warden 部署到生产 / 预发布环境的工程师。
> **目标**：搭出一套 HTTPS、最小权限、可回滚的运行环境。
> 变量含义查 [configuration.md](configuration.md)，SSO 接入查 [oidc-setup.md](oidc-setup.md)，日常运维查 [operations.md](operations.md)。

> **先读这一句**：仓库里的 `deploy/docker-compose.yaml` **只用于本地开发**（PostgreSQL + 可选 OPA/OpenBao）。仓库**不包含**生产编排文件；生产用的 compose / Kubernetes 清单、反向代理配置和 Secret 挂载由部署环境自行维护。不要拿本地 compose 直接上生产。

## 1. 准备制品

| 制品 | 构建方式 | 说明 |
|---|---|---|
| 控制面镜像 | `docker build -f server/Dockerfile --build-arg TARGET=warden-control -t <registry>/warden-control:<tag> .` | 多阶段构建，运行阶段为 distroless nonroot |
| 预编译发布镜像 | `server/Dockerfile.release` | 需先 `make server-build-linux-amd64` 产出 `server/bin/linux-amd64/` |
| 管理端 | `web/Dockerfile`（构建 `web/dist` 并用 nginx 托管） | 也可自行用任意静态服务器托管 `web/dist` |
| 随镜像打包的目录 | `deploy/`、`server/migrations/` | 控制面的 `/downloads/*`、Helm Chart、安装脚本都从 `WARDEN_ARTIFACT_ROOT` 读取，缺了会导致接入向导不可用 |
| Connector / Helper 二进制 | `make server-build-linux-amd64` / `server-build-linux-arm64` / `server-build-hosts` | 同上，供目标机器下载 |

镜像必须自包含：`server/Dockerfile` 会把 `deploy/` 与 `server/migrations/` 复制到 `/app-artifacts` 并设置 `WARDEN_ARTIFACT_ROOT`。

## 2. 准备 Secret

生产环境**全部使用 `*_FILE`**，每个值独立随机生成：

```bash
# 示意：在受保护的 Secret 管理系统中生成，不要写进仓库或命令历史
openssl rand -hex 32   # 分别用于 task_signing_key / jwt_signing_key /
                       # credential_encryption_key / admin_api_key，四次各不复用
openssl rand -hex 24   # admin_password（8–72 字节）
```

红线：

- 4 个必需 Secret 两两不得相同，否则控制面启动失败。
- 文件权限 `600`，仅控制面进程可读。
- `WARDEN_ADMIN_USERNAME` 与 `WARDEN_ADMIN_PASSWORD_FILE` 是启动管理员的同步源；改文件并重启会轮换哈希，妥善保管。
- OIDC client secret 单独存放，见 [oidc-setup.md](oidc-setup.md)。

## 3. 准备 PostgreSQL

- 使用独立实例，开启 TLS 或置于可信网络；创建专用数据库与账号。
- 用 `WARDEN_DATABASE_URL_FILE` 提供 DSN，不要把 DSN 写进 compose 的 `environment`。
- **先备份、后变更**：任何升级前执行一次 `pg_dump`，见 [operations.md](operations.md)。

## 4. 执行数据库迁移

**控制面启动不会自动执行迁移**，必须先跑：

```bash
# 全新空库：按文件名顺序执行全部迁移
for f in server/migrations/*.sql; do
  psql -v ON_ERROR_STOP=1 "<dsn>" -f "$f" >/dev/null || break
done

# 已有数据的库：只按发布清单重放幂等迁移（不要重放 001–034）
while read -r f; do
  psql -v ON_ERROR_STOP=1 "<dsn>" -f "server/migrations/$f" >/dev/null || break
done < deploy/release/migrations.txt
```

要点：

- `002_seed.sql` 保留固定权限锚点，运行时同步用户提供的管理员登录名和密码哈希。
- `deploy/release/migrations.txt` 收录编号 ≥ 035 的幂等迁移；**新增此类迁移必须登记**，否则 `internal/control` 的单测会失败。
- `audit_events.action` 是 PostgreSQL 枚举，新增审计动作名必须配套 `ALTER TYPE audit_action ADD VALUE` 迁移。

## 5. 反向代理与 TLS

前端是 SPA，需要把 `/api` 反代到控制面，静态资源走 fallback 到 `index.html`。参考 `web/nginx.conf`（容器版，监听 8080）的写法，外层反代示意：

```nginx
server {
    listen 443 ssl http2;
    server_name <warden.example.com>;

    # ssl_certificate / ssl_certificate_key 指向真实证书

    location /api/ {
        proxy_pass http://<warden-control-upstream>:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location / {
        root /var/www/warden/dist;
        try_files $uri $uri/ /index.html;
    }
}
```

配套控制面配置：

| 变量 | 生产值 |
|---|---|
| `WARDEN_COOKIE_SECURE` | `true`（默认，不要改） |
| `WARDEN_PUBLIC_CONTROL_URL` | `https://<warden.example.com>` |
| `WARDEN_CORS_ORIGIN` | 与前端同源的绝对 origin（若前后端分离） |
| `WARDEN_TRUST_PROXY_HEADERS` | `true` |
| `WARDEN_TRUSTED_PROXY_CIDRS` | 反向代理所在 CIDR，逗号分隔 |
| `WARDEN_REGISTRATION_AUDIENCE` | 与 `WARDEN_PUBLIC_CONTROL_URL` 一致（`.../api/v1`），或省略让它自动推导 |

> 全站 HTTPS 是硬性要求：非本地 HTTP 会被安装脚本、注册脚本与会话 Cookie 策略拒绝。

## 6. 启动控制面

最小必需变量（全部用 `_FILE`）：

```text
WARDEN_DATABASE_URL_FILE
WARDEN_TASK_SIGNING_KEY_FILE
WARDEN_JWT_SIGNING_KEY_FILE
WARDEN_CREDENTIAL_ENCRYPTION_KEY_FILE
WARDEN_ADMIN_API_KEY_FILE
WARDEN_ADMIN_USERNAME
WARDEN_ADMIN_PASSWORD_FILE
WARDEN_PUBLIC_CONTROL_URL
WARDEN_COOKIE_SECURE=true
```

其余按需配置，见 [configuration.md](configuration.md)。启动顺序：

本地开发用的 `config.yaml` 与 `.env` 不参与生产启动。生产部署应在容器 / Kubernetes 的环境配置中设置
`WARDEN_AI_MODEL`、`WARDEN_AI_BASE_URL` 等值，并通过 Secret 挂载设置 `WARDEN_AI_API_KEY_FILE`。

1. 校验所有 Secret 文件存在且非空、权限正确；
2. 启动控制面容器；
3. 确认 `GET /api/v1/health`、`/livez`、`/readyz` 返回 200；
4. 部署前端静态产物；
5. 用配置的启动管理员登录一次，确认会话与 `/auth/me` 正常。

> **注意**：`/healthz` 是前端 nginx 容器的探针，后端没有这个路由。健康检查请用 `/api/v1/health` 或 `/readyz`。

## 7. 可选：启用 SSO

按 [oidc-setup.md](oidc-setup.md) 配置 `WARDEN_OIDC_*`。生产务必保持 `WARDEN_OIDC_DEFAULT_ROLE=user`，超级管理员只能通过页面手工指派。SSO 未配置时登录页自动隐藏 SSO 入口，密码登录仍可用。

## 8. 上线前检查清单

- [ ] HTTPS 证书有效，`http` 正确跳转到 `https`
- [ ] `WARDEN_COOKIE_SECURE=true`，浏览器会话 Cookie 带 `Secure; HttpOnly; SameSite=Strict`
- [ ] 4 个必需 Secret 独立且互不相同，权限 600，未进入镜像层或日志
- [ ] 数据库已备份、迁移已按正确清单执行
- [ ] `/api/v1/health`、`/livez`、`/readyz` 返回 200
- [ ] `WARDEN_PUBLIC_CONTROL_URL` 与实际访问地址一致；安装命令里的地址可被目标机器访问
- [ ] `/downloads/SHA256SUMS`、Helm Chart、各平台 Connector 二进制可下载
- [ ] `GET /api/v1/auth/oidc/config` 返回预期的 `enabled` 与绝对 URL
- [ ] 启动管理员密码可用（break-glass），但日常不使用
- [ ] `WARDEN_OIDC_DEFAULT_ROLE=user`，`WARDEN_ENABLE_LEGACY_*` 保持关闭
- [ ] 反向代理 CIDR 白名单已收紧，未对公网开放数据库与 OPA
- [ ] 保留上一版镜像 ID、前端产物目录、数据库备份，随时可回滚

## 9. 回滚

1. 停止并切回上一版控制面镜像 ID（无需重建）。
2. 把上一版前端 `dist` 目录移回静态站点根路径。
3. 若本次包含数据库迁移，先确认迁移是幂等的、不需要反向 DDL；确需回退时用备份恢复。
4. 用启动管理员密码登录验证，密码登录链路独立于 IdP，不会把人锁在外面。

详细运维流程见 [operations.md](operations.md)。

## 下一步

- [oidc-setup.md](oidc-setup.md)：企业单点登录
- [onboarding.md](onboarding.md)：接入目标系统
- [operations.md](operations.md)：备份、升级、密钥轮换
- [troubleshooting.md](troubleshooting.md)：部署后异常排查
