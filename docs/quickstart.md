# 教程：15 分钟在本地跑起来

> **面向**：第一次接触 Warden Warden 的开发者。
> **读完你会得到**：一个能登录的管理端、一个能响应 API 的控制面、一套可用的本地密钥，以及明确的下一步。
> 这是教程，只给一条能走通的路。配置项含义查 [configuration.md](configuration.md)，出问题查 [troubleshooting.md](troubleshooting.md)。

## 你会搭出什么

```text
浏览器 http://localhost:4173  ──/api 代理──>  warden-control http://127.0.0.1:8080  ──>  PostgreSQL :5433
                                                      │
                                                      └── 内嵌 Rego 策略 / 审计
```

本地默认不启用任何外部 IdP、OPA 或 Connector；先跑通控制面，再按需接目标系统。

## 0. 前置条件

| 需要 | 说明 |
|---|---|
| Docker + Compose | 跑 PostgreSQL（`deploy/docker-compose.yaml` 把它映射到本机 `5433`） |
| Go | 版本见 `server/go.mod` |
| Node.js + npm | 版本见 `web/Dockerfile` |
| `openssl`、`curl` | 生成密钥、验证接口 |
| 两个终端 | 控制面与前端各占一个 |

以下所有命令都在仓库根目录执行。

## 1. 生成一套本地密钥

控制面启动时强制校验 4 个 Secret：**至少 32 字节、两两不同、不可复用**（`server/internal/control/handler.go`）。生产环境请用独立的密钥管理方案；这里只为本地生成。

```bash
mkdir -p .secrets
chmod 700 .secrets

# 4 个必需 Secret（无换行，避免文件内容比对出现偏差）
for f in task_signing_key jwt_signing_key credential_encryption_key agent_api_key; do
  printf '%s' "$(openssl rand -hex 32)" > ".secrets/$f"
done

# 内置 admin 登录密码：8–72 字节
printf '%s' "$(openssl rand -hex 24)" > .secrets/admin_password

# PostgreSQL 密码 + 连接串，两者必须一致
printf '%s' "$(openssl rand -hex 24)" > .secrets/postgres_password
printf 'postgres://pam:%s@127.0.0.1:5433/pam?sslmode=disable' \
  "$(cat .secrets/postgres_password)" > .secrets/database_url

chmod 600 .secrets/*
```

`.secrets/` 已被 `.gitignore` 排除，不要提交。文件对应关系：

| 文件 | 对应变量 |
|---|---|
| `database_url` | `WARDEN_DATABASE_URL_FILE` |
| `task_signing_key` | `WARDEN_TASK_SIGNING_KEY_FILE` |
| `jwt_signing_key` | `WARDEN_JWT_SIGNING_KEY_FILE` |
| `credential_encryption_key` | `WARDEN_CREDENTIAL_ENCRYPTION_KEY_FILE` |
| `agent_api_key` | `WARDEN_ADMIN_API_KEY_FILE`（引导用管理 Key） |
| `admin_password` | `WARDEN_ADMIN_PASSWORD_FILE` |
| `postgres_password` | Compose 的 `postgres_password` Docker secret |

## 2. 启动数据库

```bash
docker compose -f deploy/docker-compose.yaml up -d
docker compose -f deploy/docker-compose.yaml ps
```

`postgresql` 健康后即可继续。可选组件（独立 OPA / OpenBao）本教程不需要；如需启用，用 `--profile optional up -d`。

## 3. 执行数据库迁移

> **重要**：`warden-control` **不会在启动时自动跑迁移**，内置 `admin` 账号也不是运行时创建的，而是 `002_seed.sql` 写入的。必须先手工执行迁移。

全新空库按文件名顺序执行全部迁移：

```bash
for f in server/migrations/*.sql; do
  echo "==> $(basename "$f")"
  docker compose -f deploy/docker-compose.yaml exec -T postgresql \
    psql -v ON_ERROR_STOP=1 -U pam -d pam -f "/migrations/$(basename "$f")" >/dev/null || break
done
```

验证：

```bash
docker compose -f deploy/docker-compose.yaml exec -T postgresql \
  psql -U pam -d pam -tAc "select count(*) from information_schema.tables where table_schema='public';"
```

应返回几十张表；若报错，先看 `psql` 输出定位是哪一条迁移，不要重复执行部分成功的脚本。已有数据的库请按 [operations.md](operations.md) 的发布流程处理，不要重放全部迁移。

## 4. 构建并启动控制面

```bash
make server-build
WARDEN_FOREGROUND=1 scripts/start-local-control.sh
```

`start-local-control.sh` 会读取 `.secrets/` 并组装环境变量；`WARDEN_FOREGROUND=1` 让进程留在当前终端，方便看日志（`Ctrl-C` 结束）。不加这个变量时脚本后台运行，日志写到 `/tmp/warden-control-direct.log`。

注意两点：

- 脚本会**清理占用 `WARDEN_PORT`（默认 8080）的已有进程**；如该端口有别的服务，先设 `WARDEN_PORT=8090`。
- 脚本为本地 HTTP 自动设 `WARDEN_COOKIE_SECURE=false`。**生产环境永远不要关掉它**（见 [installation.md](installation.md)）。

保持这个终端运行，另开一个终端继续。

## 5. 验证控制面

```bash
# 健康检查（公开，不需要认证）
curl -sS -i http://127.0.0.1:8080/api/v1/health | head -5

# 未带凭据应返回 401
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/api/v1/auth/me

# 带管理 Key 应返回 200
curl -sS -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer $(cat .secrets/agent_api_key)" \
  http://127.0.0.1:8080/api/v1/resources
```

期望依次看到 `200`、`401`、`200`。第三个请求返回 `[]` 是**正常的**——此刻还没有接入任何 Connector，资源目录本来就是空的。

## 6. 启动管理端并登录

```bash
make web-install
make web-dev
```

Vite 默认监听 `http://localhost:4173`，并把 `/api` 代理到 `http://localhost:8080`（可用 `WARDEN_WEB_PORT` / `WARDEN_WEB_HOST` / `WARDEN_CONTROL_TARGET` 覆盖，见 [configuration.md](configuration.md)）。

两个登录入口，互不干扰：

- **平台运维入口** `http://localhost:4173/platform/login`：用户名 `admin`，密码为 `.secrets/admin_password` 的内容（`cat .secrets/admin_password`）。登录后落在租户管理页。
- **组织登录页** `http://localhost:4173/login`：租户用户用 RAM 式限定名 `用户名@组织标识`（例如 `zhangsan@acme`）+ 密码登录。组织标识由租户管理员创建用户时提供。

登录后能看到顶部用户信息与左侧菜单，说明密码校验、HttpOnly 会话 Cookie、`/auth/me` 能力清单下发这条链路都通了。

> 如果菜单只有 4 项，通常意味着当前会话不是超级管理员。内置 `admin` 按固定 ID 判定为超管；若你用的是 SSO 账号，请先读 [authorization.md](authorization.md)。

## 7. 收尾与清理

```bash
# 停止：控制面终端 Ctrl-C；前端终端 Ctrl-C
docker compose -f deploy/docker-compose.yaml down     # 保留数据卷
make clean                                           # 删除 server/bin、web/dist、缓存
```

`docker compose down -v` 会删除数据库数据卷，本地重建时才用。

---

## 你刚刚跑通了什么

- 文件型 Secret → 控制面启动校验 → PostgreSQL 连接池 → 路由注册 → 会话签发
- 内置 `admin` 的 bcrypt 密码哈希在启动时与 `WARDEN_ADMIN_PASSWORD_FILE` 同步
- 前端通过 `/api` 代理、HttpOnly Cookie 与 `capabilities` 渲染菜单

## 下一步

| 想做的事 | 去哪 |
|---|---|
| 接入第一个 Kubernetes / Linux / 网络目标 | [onboarding.md](onboarding.md) |
| 启用企业 SSO 并给同事配权限 | [oidc-setup.md](oidc-setup.md) |
| 理解租户、项目、执行、审批的关系 | [concepts.md](concepts.md) |
| 看懂组件与执行链路 | [architecture.md](architecture.md) |
| 查全量环境变量 | [configuration.md](configuration.md) |
| 查接口与错误码 | [api.md](api.md) |
| 部署到正式环境 | [installation.md](installation.md) |
