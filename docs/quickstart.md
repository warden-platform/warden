# 教程：15 分钟在本地跑起来

> **面向**：第一次接触 Warden 的开发者。
> **读完你会得到**：一个能登录的管理端、一个能响应 API 的控制面、一套可用的本地密钥，以及明确的下一步。
> 这是教程，只给一条能走通的路。配置项含义查 [配置参考](reference.md#configuration)，出问题查 [troubleshooting.md](troubleshooting.md)。

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

## 1. 初始化本地配置

内部签名与加密密钥由显式初始化命令生成，不属于用户配置。命令只在文件不存在时创建，
拒绝覆盖已有密钥；控制面启动只读取，不会生成或改写它们。

```bash
scripts/init-local-secrets.sh
cp .env.example .env
chmod 600 .env

# 启动管理员密码：8–72 字节
sed -i '' "s|^WARDEN_ADMIN_PASSWORD=.*|WARDEN_ADMIN_PASSWORD=$(openssl rand -hex 24)|" .env

# PostgreSQL 密码；config.yaml 通过 ${WARDEN_DATABASE_PASSWORD} 引用
sed -i '' "s|^WARDEN_DATABASE_PASSWORD=.*|WARDEN_DATABASE_PASSWORD=$(openssl rand -hex 24)|" .env
```

`.env` 已被 `.gitignore` 排除，不要提交。普通配置统一放 `config.yaml`，包括端口、数据库地址、
管理员用户名、OIDC 地址和 AI 模型参数；管理员密码、数据库密码和外部服务凭据放 `.env`。
`var/warden/secrets.json` 保存初始化生成的四个内部密钥，同样被 Git 忽略且权限为 `0600`，
升级时复用，备份时必须包含。生产环境请用独立的密钥管理方案。
`config.yaml` 中 `database.url` 不包含账号密码，`database.username` 和 `database.password`
分别负责连接身份；密码用 `${WARDEN_DATABASE_PASSWORD}` 指向 `.env`。使用本教程的 Compose
数据库时，把 `database.url` 改为 `postgres://127.0.0.1:5433/warden`。

| 用途 | `.env` 变量 |
|---|---|
| 数据库密码 | `WARDEN_DATABASE_PASSWORD` |
| 启动管理员密码 | `WARDEN_ADMIN_PASSWORD` |

## 2. 启动数据库

```bash
docker compose --env-file .env -f deploy/docker-compose.yaml up -d
docker compose --env-file .env -f deploy/docker-compose.yaml ps
```

`postgresql` 健康后即可继续。可选组件（独立 OPA / OpenBao）本教程不需要；如需启用，用 `--profile optional up -d`。

## 3. 执行数据库迁移

> **重要**：`warden-control` **不会在启动时自动跑迁移**，必须先手工执行迁移。`002_seed.sql` 只保留固定权限锚点，实际登录名由启动时的 `admin.username` 同步。

全新空库按文件名顺序执行全部迁移：

```bash
for f in server/migrations/*.sql; do
  echo "==> $(basename "$f")"
  docker compose --env-file .env -f deploy/docker-compose.yaml exec -T postgresql \
    psql -v ON_ERROR_STOP=1 -U warden -d warden -f "/migrations/$(basename "$f")" >/dev/null || break
done
```

验证：

```bash
docker compose --env-file .env -f deploy/docker-compose.yaml exec -T postgresql \
  psql -U warden -d warden -tAc "select count(*) from information_schema.tables where table_schema='public';"
```

应返回几十张表；若报错，先看 `psql` 输出定位是哪一条迁移，不要重复执行部分成功的脚本。已有数据的库请按 [日常运维](guides.md#operations) 的发布流程处理，不要重放全部迁移。

## 4. 构建并启动控制面

本地普通配置统一放在根目录 `config.yaml`，用户凭据放在 `.env`，不要修改启动脚本：

```bash
make server-build
WARDEN_FOREGROUND=1 scripts/start-local-control.sh
```

`config.yaml` 可覆盖整个本地项目的非敏感设置，包括控制面、前端开发服务器、API 代理、OIDC 和 AI，
完整清单见 [配置参考](reference.md#configuration)。AI 的模型名、地址放 `config.yaml`，
`WARDEN_AI_API_KEY` 放 `.env`。启动管理员用户名放 `config.yaml` 的 `admin.username`，
密码在 YAML 中同样有字段，但值是 `${WARDEN_ADMIN_PASSWORD}`，真实密码只放在 `.env`。
管理员密码是启动必需项；用户名未设置时回退内置 `admin`，没有默认密码或隐式密码文件兜底。

`warden-control` 启动时由 `server/internal/config` 读取 `config.yaml`、`.env` 和
`var/warden/secrets.json`；显式传入的进程环境变量优先级最高。
`WARDEN_FOREGROUND=1` 让进程留在当前终端，方便看日志（`Ctrl-C` 结束）。不加这个变量时脚本后台运行，日志写到 `/tmp/warden-control-direct.log`。

注意两点：

- 脚本会**清理占用 `server.port`（默认 8080）的已有进程**；如该端口有别的服务，先在 `config.yaml` 中改成 `8090`。
- 脚本为本地 HTTP 自动设 `WARDEN_COOKIE_SECURE=false`。**生产环境永远不要关掉它**（见 [生产部署](guides.md#installation)）。

保持这个终端运行，另开一个终端继续。

## 5. 验证控制面

```bash
# 健康检查（公开，不需要认证）
curl -sS -i http://127.0.0.1:8080/api/v1/health | head -5

# 未带凭据应返回 401
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/api/v1/auth/me

# 带管理 Key 应返回 200
curl -sS -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer $(python3 -c "import json;print(json.load(open('var/warden/secrets.json'))['WARDEN_ADMIN_API_KEY'])")" \
  http://127.0.0.1:8080/api/v1/resources
```

期望依次看到 `200`、`401`、`200`。第三个请求返回 `[]` 是**正常的**——此刻还没有接入任何 Connector，资源目录本来就是空的。

## 6. 启动管理端并登录

```bash
make web-install
make web-dev
```

Vite 同样读取根目录 `config.yaml`，默认监听 `http://localhost:4173`，并把 `/api` 代理到 `http://localhost:8080`（可用 `web.host` / `web.port` / `web.control_target` 覆盖，见 [配置参考](reference.md#configuration)）。

两个登录入口，互不干扰：

- **平台运维入口** `http://localhost:4173/platform/login`：用户名为 `config.yaml` 的 `admin.username`，密码为 `.env` 的 `WARDEN_ADMIN_PASSWORD`。登录后落在租户管理页。
- **组织登录页** `http://localhost:4173/login`：租户用户用 RAM 式限定名 `用户名@组织标识`（例如 `zhangsan@acme`）+ 密码登录。组织标识由租户管理员创建用户时提供。

登录后能看到顶部用户信息与左侧菜单，说明密码校验、HttpOnly 会话 Cookie、`/auth/me` 能力清单下发这条链路都通了。

> 如果菜单只有 4 项，通常意味着当前会话不是超级管理员。启动管理员按固定 ID 判定为超管；若你用的是 SSO 账号，请先读 [authorization.md](authorization.md)。

## 7. 收尾与清理

```bash
# 停止：控制面终端 Ctrl-C；前端终端 Ctrl-C
docker compose --env-file .env -f deploy/docker-compose.yaml down     # 保留数据卷
make clean                                           # 删除 server/bin、web/dist、缓存
```

`docker compose down -v` 会删除数据库数据卷，本地重建时才用。

---

## 你刚刚跑通了什么

- 初始化生成内部密钥 → 控制面启动校验 → PostgreSQL 连接池 → 路由注册 → 会话签发
- 启动管理员的用户名与 bcrypt 密码哈希在启动时与显式配置同步
- 前端通过 `/api` 代理、HttpOnly Cookie 与 `capabilities` 渲染菜单

## 下一步

| 想做的事 | 去哪 |
|---|---|
| 接入第一个 Kubernetes / Linux / 网络目标 | [接入指南](guides.md#onboarding) |
| 启用企业 SSO 并给同事配权限 | [SSO 接入](guides.md#oidc) |
| 理解租户、项目、执行、审批的关系 | [concepts.md](concepts.md) |
| 看懂组件与执行链路 | [architecture.md](architecture.md) |
| 查全量环境变量 | [配置参考](reference.md#configuration) |
| 查接口与错误码 | [api.md](api.md) |
| 部署到正式环境 | [生产部署](guides.md#installation) |
