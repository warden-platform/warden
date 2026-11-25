# 贡献指南

> **面向**：要给 Warden 提交代码或文档的人。
> 第一次上手先跑一遍 [docs/quickstart.md](docs/quickstart.md)，改架构相关的代码前读 [docs/architecture.md](docs/architecture.md)。

## 基本要求

- 基于最新的主开发分支，提交前先同步。
- 提交中**不得包含**：凭据、kubeconfig、内网主机名、生成的二进制、`node_modules`、构建产物、`.env`、`var/warden/secrets.json`、本地测试报告或截图。
- 命令示例使用 `<placeholder>`；不要把可直接工作的弱默认凭据写进代码或文档。

## 本地验证

在仓库根目录执行：

```bash
make verify          # = server-test + server-vet + server-build + web-build
git diff --check
```

等价的分步命令：

```bash
cd server
go test ./...
go vet ./...
go build ./...

cd ../web
npm ci
npm run build        # tsc -b && vite build

cd ..
helm lint deploy/helm/warden-operator --set controlPlaneURL=https://warden.example.com
```

补充约定：

- `go test ./...` 基线为绿；CI（`.github/workflows/ci.yml`，GitHub 镜像生效）会带 PostgreSQL service 跑全部 DB 门控集成测试，提交前请确保没有新增失败。
- 依赖 PostgreSQL 的集成测试是 opt-in 的：设置 `WARDEN_TEST_DATABASE_URL` 才会执行，否则 `t.Skip`。**control 包的测试会 `DROP SCHEMA public CASCADE` 重建库，务必指向一次性测试库（如本地 `warden_test`），绝不能指向开发库**；多个包共用同一测试库时用 `go test -p 1` 串行。这类测试的 cleanup 用 `t.Cleanup`（LIFO），不要用 `defer pool.Close()`。
- 前端没有独立的 `typecheck` script；需要纯类型检查时用 `npx tsc --noEmit`，完整构建用 `npm run build`。纯逻辑单测用 `npm run test`（vitest，覆盖状态映射/路由匹配/拓扑构建等无 DOM 依赖的函数），已挂进 `make verify` 与 CI。
- 改了数据库迁移必须验证：`deploy/release/migrations.txt` 包含全部编号 ≥ 035 的迁移，且清单内迁移可重复重放（`internal/control/migration_files_test.go` 会强制校验）。

## 发布与镜像同步

双仓关系：**GitHub `warden-platform/warden`（master）是主仓库与合并门禁**，PR 必须通过 `server` / `web` 两项 Actions 检查（含 PostgreSQL service 的 DB 门控集成测试）方可合并；**Gitee `warden-platform/warden`（master）是国内镜像**，由 [.github/workflows/sync-gitee.yml](.github/workflows/sync-gitee.yml) 在 master 前进后自动快进同步 master，并在 GitHub Release 发布后同步 tag 与 Gitee 发行版（只普通 push，绝不 force；保护分支拦截会报出同步 PR；不搬 GitHub Release 附件）。Gitee 原有的 `ci-gate` 与镜像发布流水线已停用（Gitee Go 额度耗尽）。

### 代码合入 master（日常）

master 是保护分支，禁止直接 push，一律走 PR：

1. 按主题拆 commit，建分支 `git push github <分支>`。
2. `gh pr create --repo warden-platform/warden --base master --head <分支>` 建 PR。
3. `server` / `web` 检查通过后可合并；PR 开启 auto-merge 时 CI 绿了会自动 Squash 合并。
4. 合并后上游仓库的 Actions 会自动同步 Gitee 镜像（fork 不触发）；如失败或需人工处理，`make sync-gitee` 仍是兜底。

### 发版

1. `CHANGELOG.md` 把 `[未发布]` 段转成版本化条目（破坏性变更、迁移、已知限制、部署注意写全），同步 README 发布状态与 Helm `Chart.yaml` 的 version/appVersion；对照 `[未发布]` 段的「下次发版待办」逐项勾销。
2. `make verify` 全绿后按主题 commit、push、走上面的 PR 流程合入。
3. 打 annotated tag 并 push 到 GitHub（如 `git tag -a v0.4.3 -m "..." && git push github v0.4.3`）：自动触发 [.github/workflows/publish.yml](.github/workflows/publish.yml)，构建 control/web/operator/connector 四镜像与内部 `warden-deploy` 脚本镜像并推送 ACR（version + latest 双 tag；仓库需配置 `ACR_USERNAME` / `ACR_PASSWORD` 两个 Secrets），镜像全部成功后自动创建 GitHub Release（正文取 `CHANGELOG.md` 对应版本小节，已存在则跳过）。
4. 兜底：构建机上仍可 `scripts/publish-acr.sh vX.Y.Z`（凭据 `.secrets/acr_*` 600）。compose 示例与 Helm Chart 默认指向 ACR 公开镜像，使用者免构建。
5. 生产部署（从 ACR 拉公开镜像，服务器不编译）：
   - **自动化**：tag 发布成功后自动触发 [.github/workflows/deploy.yml](.github/workflows/deploy.yml)，在自托管 runner（部署机）上执行 `sudo -n /usr/local/bin/warden-deploy vX.Y.Z`：
     0. 从内部 `warden-deploy` 脚本镜像（`scripts/Dockerfile.deployer`，与服务镜像无关）取出 `deploy-compose.sh`，校验后原子替换宿主机安装副本并重载执行（首次引导版本仍需人工安装，之后脚本更新随该内部镜像走）；
     1. 从新版本控制面镜像读取发布迁移清单，与 `.env` 中上一版本的清单做差量；
     2. 有新迁移时先 `pg_dump -Fc` 备份到 `BACKUP_DIR/db-*.dump`（保留最近 `KEEP_DB_BACKUPS` 份），再按清单顺序、以 `ON_ERROR_STOP=1` 应用；任一条失败即终止，**不切换镜像**；
     3. 写 `.env` 的 `WARDEN_VERSION` → `docker compose pull && up -d` → 探活 → 从 `warden-web` 镜像取静态产物同步到宿主机前端目录（先 tar 备份到 `deploy/backups/`）；容器拉起/探活失败自动切回上一镜像（已应用的迁移不会自动回退，需人工按备份恢复）。
     部署 job 绑定 `production` 环境，需人工 Approve 才执行；手动重发/回滚用 `workflow_dispatch` 指定版本。
     迁移默认 `MIGRATION_MODE=auto`（只跑新增差量，避免 106 每次重放导致全员重登）；外部托管数据库可设 `MIGRATION_MODE=skip` 自行迁移，或设 `all` 强制全量重放。
   - **部署机一次性配置**（runner 为非 root 用户，不能直接改 `/root` 与 `/var/www`）：
     1. `sudo install -m 755 scripts/deploy-compose.sh /usr/local/bin/warden-deploy`（root:root）；自更新逻辑落地后只需人工安装一次引导版本，后续脚本变更由内部 `warden-deploy` 镜像携带并在部署时自更新；
     2. `/etc/warden/deploy.env` 固化本机路径，如 `DEPLOY_DIR=/root/warden/deploy`、`COMPOSE_FILE=.../docker-compose.prod.yaml`、`WEB_ROOT=/var/www/warden/dist`、`BACKUP_DIR=/root/warden/backups`（权限 600）；数据库迁移目标默认 `PG_SERVICE=postgresql` / `PG_USER=warden` / `PG_DB=warden`，可按实际情况覆盖，或用 `MIGRATION_MODE=skip` / `SKIP_DB_BACKUP=1` 显式关闭；
     3. `/etc/sudoers.d/warden-deploy` 只放行这一条命令：`runner ALL=(root) NOPASSWD: /usr/local/bin/warden-deploy`，改完用 `visudo -cf` 校验；
     4. compose 里镜像写成 `${WARDEN_VERSION:-vX.Y.Z}`，部署脚本通过 `.env` 切换版本。
   - **手动兜底**：备份（pg_dump `-Fc` + compose/nginx 快照）→ 应用 `deploy/release/migrations.txt` 中的新迁移（`docker exec ... psql -v ON_ERROR_STOP=1 -1 < SQL`）→ 设 `WARDEN_VERSION=vX.Y.Z` 后 `docker compose up -d`（自动 pull）→ 公网验证 health/登录/SSO 未破坏。
   - 部署机需装一次 self-hosted runner，注册标签须包含 `self-hosted` 与 `Linux`（与 [deploy.yml](.github/workflows/deploy.yml) 的 `runs-on` 一致）。

注意：GitHub 是 tag 与 Release 的源头，发版 tag 直接 push 到 GitHub；Gitee 自动同步覆盖 master、tag 与 Gitee 发行版（正文取 GitHub Release，不搬附件）。自动同步需要在仓库配置 `GITEE_USERNAME` / `GITEE_TOKEN` 两个 Secrets；未配置时 workflow 会 warning 跳过，不阻塞 master。`systemd-run`（publish-acr / 后台构建）不继承工作目录，须绝对路径。

## 代码约定

- 依赖方向：`domain` 是叶子包；`policy` / `connector` / `storage/postgres` 依赖 `domain`；`control` 聚合上述包；`cmd` 只做装配。不要反向依赖。
- 新增 REST 路由：在 `server/internal/control/handler.go` 的 `RegisterRoutes` 注册，并同步更新 [docs/api.md](docs/api.md)。
- 新增权限点：只在 `server/internal/control/authorization.go` 定义，后端守卫与前端菜单的 `requires` 共用同一批字符串；同步 [docs/authorization.md](docs/authorization.md)。
- 新增环境变量：同步 [配置参考](docs/reference.md#configuration)；敏感项复用 `config.Secret` 的 `*_FILE` 约定。
- 新增审计动作名：**必须补一条 `ALTER TYPE audit_action ADD VALUE` 迁移**，否则审计会静默丢失。
- 新增迁移：编号 ≥ 035 的必须登记 `deploy/release/migrations.txt`，且写成可重复重放。编号 068–071 为历史空洞（刻意跳号），不是缺文件；001–034 不保证幂等，全量重放仅限全新空库（`make migrate`），已有库升级用 `make migrate-release`。
- 新增动作 / 版本：同步 [动作参考](docs/reference.md#actions) 与 Connector capabilities。

## 安全相关改动

涉及授权、Scope 校验、重放防护、请求体大小限制、凭据脱敏的改动，必须包含**负向测试**（越权应被拒绝、重放应失败、敏感字段不应出现在响应 / 审计里）。决策 Provider 异常必须保持默认 `DENY`，不要新增"失败即放行"的分支。

漏洞请按 [SECURITY.md](SECURITY.md) **私下报告**，不要开公开 Issue。

## 文档规范

完整规范见 [docs/README.md](docs/README.md)。要点：

- 文档分四类：教程 / 操作指南 / 参考 / 解释，一篇只做一件事。
- 必须区分「已实现 / 设计中 / 未实现 / 已知限制」。
- 路由以 `handler.go` 为准、变量以代码为准、迁移以 `migrations/` 与发布清单为准；发现冲突顺手修文档。
- `docs/*` 在 `.gitignore` 里**默认排除**，新增文档必须加一行 `!docs/<文件名>.md` 才会入库。
- 新增或改名文档时，同步根 `README.md` 的导航与 `docs/README.md` 的目录表。

## 提交前检查清单

- [ ] `make verify` 通过，或明确记录既有基线失败
- [ ] `git diff --check` 无空白错误
- [ ] 没有凭据 / 内网地址 / 个人路径 / 构建产物进入暂存区
- [ ] 新增路由、变量、动作、权限点、审计动作、迁移都同步了对应文档
- [ ] 新增文档已加入 `.gitignore` 白名单，导航链接可达
- [ ] 安全改动有负向测试
- [ ] 描述里说明「改了什么、为什么、怎么验证、如何回滚」
