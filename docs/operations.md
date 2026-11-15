# 操作指南：日常运维

> **面向**：负责运行 Warden Warden 的运维 / 值班人员。
> **目标**：能安全地做备份、升级、迁移、密钥轮换与回滚。
> 首次部署见 [installation.md](installation.md)，故障定位见 [troubleshooting.md](troubleshooting.md)，变量见 [configuration.md](configuration.md)。

## 健康检查与观测

| 检查 | 命令 / 路径 | 期望 |
|---|---|---|
| 进程存活 | `GET /livez` | 200 |
| 数据库就绪 | `GET /readyz` 或 `/api/v1/health` | 200 |
| 前端容器存活 | `GET /healthz`（nginx 容器内） | `ok` |
| OIDC 开关 | `GET /api/v1/auth/oidc/config` | 预期的 `enabled` 与绝对 `login_url` / `logout_url` |
| 当前身份 | `GET /api/v1/auth/me` | 角色、`capabilities`、`auth_source` 正确 |
| Connector | `POST /api/v1/connectors/{id}/check` | ACTIVE 且心跳在 90 秒内 |

> `/healthz` 只在前端 nginx 容器里存在；后端探针是 `/livez`、`/readyz`、`/api/v1/health`。

建议监控：控制面日志里的启动错误、审计写入失败、决策 Provider 异常（会默认 DENY）、数据库连接失败、Connector 心跳缺失、过期审批与排队任务数量。

## 数据库备份

**任何升级、迁移、批量改数据之前必须先备份。**

```bash
# 独立 PostgreSQL
pg_dump "<dsn>" | gzip > warden-db-$(date +%Y%m%d-%H%M%S).sql.gz
gzip -t warden-db-*.sql.gz    # 校验归档完整

# 容器化 PostgreSQL（示意）
docker exec <postgres-container> pg_dump -U <user> -d <db> \
  | gzip > warden-db-$(date +%Y%m%d-%H%M%S).sql.gz
```

恢复前先停控制面，确认目标库可丢弃或已另行备份，再导入并重新跑迁移校验。备份文件含业务数据与审计，按敏感数据管理，加密存放并限制访问。

同时备份的非数据库资产：控制面镜像 ID / tag、前端 `dist` 目录、生产编排文件、IdP 侧 RP 配置、`/etc` 下的 Secret 文件清单（**内容单独离线保管**）。

## 迁移规范

| 规则 | 说明 |
|---|---|
| 启动不跑迁移 | `warden-control` 不会自动执行 SQL；必须显式执行 |
| 全新库 | 按 `server/migrations/*.sql` 文件名顺序执行全部迁移 |
| 已有库 | 只按 `deploy/release/migrations.txt` 顺序重放幂等迁移（编号 ≥ 035） |
| 新增迁移 | 编号 ≥ 035 的文件**必须登记到 `migrations.txt`**，否则 `internal/control` 单测失败 |
| 幂等 | 发布清单里的迁移会被反复重放，必须用 `IF NOT EXISTS` / 条件更新等写法 |
| 枚举 | `audit_events.action` 是 PostgreSQL 枚举；新增审计动作名必须配套 `ALTER TYPE audit_action ADD VALUE` 迁移，否则审计静默丢失 |
| 编号唯一 | 由 `migration_files_test.go` 强制校验 |

不要用"删库重建"来修迁移问题；生产库有真实业务数据与审计链。

## 标准升级流程

1. **备份**：数据库 `pg_dump`、记录当前镜像 ID、保存当前前端 `dist` 与编排文件。
2. **本地验证**：`make verify`（Go 测试 + vet + 构建 + 前端构建），见 [CONTRIBUTING.md](../CONTRIBUTING.md)。
3. **执行迁移**：按上表区分全新库 / 已有库。
4. **构建镜像**：`docker build -f server/Dockerfile --build-arg TARGET=warden-control -t <registry>/warden-control:<new-tag> .`；确认镜像内含 `deploy/` 与 `server/migrations/`。
5. **替换控制面**：滚动或短暂停机切换，等待 `/readyz` 200。
6. **替换前端**：保留旧 `dist` 目录（例如 `dist.prev-<timestamp>`）后再覆盖。
7. **验收**：健康检查 → 密码登录 → SSO 登录 → `/auth/me` 能力清单 → 一个只读 Execution → 审计可见。
8. **观察**：至少一轮审计写入与 Connector 心跳周期，确认无异常日志。
9. **失败回滚**：切回旧镜像 ID、移回旧 `dist`；若迁移不兼容，用备份恢复数据库。密码登录始终可用，不会把人锁在外面。

> 如果升级涉及 OIDC，先确认 IdP 的 discovery 与 RP 配置未变；`OIDC_KEY_ENCRYPTION_KEY` 一类 IdP 侧密钥一旦丢失，已签发的签名私钥可能无法解密。

## 密钥轮换

| 密钥 | 轮换影响 | 建议 |
|---|---|---|
| `WARDEN_ADMIN_PASSWORD_FILE` | 重启时同步内置 `admin` 的 bcrypt 哈希，旧密码立即失效 | 改文件 → 重启 → 用新密码验证 break-glass |
| `WARDEN_ADMIN_API_KEY_FILE` | 静态超管 Key；所有使用旧 Key 的自动化脚本会 401 | 先更新调用方再切，或安排停机窗口；当前版本不支持在线轮换 |
| `WARDEN_JWT_SIGNING_KEY_FILE` | 所有 Web 会话 JWT 失效，用户需重新登录 | 低峰期执行，提前通知 |
| `WARDEN_CREDENTIAL_ENCRYPTION_KEY_FILE` | 已加密的 Connector 任务 Key 无法解密，Connector 会失联 | **不要单独轮换**，除非有完整的重新配对方案 |
| `WARDEN_TASK_SIGNING_KEY_FILE` | 影响旧兼容 Connector 签名 | 完成每 Connector 独立 Key 迁移后再处理 |
| OIDC client secret | IdP 侧与 Warden 侧必须同步更新 | 先在 IdP 生成新 secret，再更新 Secret 文件并重启 |

四个必需 Secret 必须保持独立且互不相同，轮换后重新校验启动。

## 会话与权限

- JWT 默认 8 小时（`WARDEN_JWT_TTL_HOURS`），权限冻结在签发的 `scopes` 里。
- **改角色必须让目标重新登录**。通过「账号与权限」页变更时，系统会在同一事务里 `UPDATE admin_sessions SET revoked_at=now()` 并写审计，强制重新签发。
- 直接改数据库不会触发会话撤销，旧权限最多残留 8 小时——**不要绕过页面改库**。
- 应急时可在数据库层撤销某账号的全部活跃会话，但事后必须补审计记录。

## 审计与 Outbox

- 审计是 append-only；角色变更审计 `account.role_updated` 与角色变更在同一事务提交，写入失败则整体回滚。
- 排查"审计页看不到登录事件"时，检查事件是否带 `tenant_id`：早期数据或部分链路可能为空。
- `WARDEN_OUTBOX_ENABLED=true` 才会写 Outbox 事件；Publisher 后台循环尚未实现，开启前确认已有消费者。

## 定时与后台任务

控制面自带后台循环：过期审批 / 排队任务 / 租约 / 清理（约 30s 一轮），无需额外 cron。Connector 侧心跳 15s、任务轮询 2s、资源同步 5min。数据库备份需要部署环境自己配置定时任务——**仓库不含备份脚本或 cron 配置**。

## 常用命令速查

```bash
# 构建与验证
make verify                 # Go 测试 + vet + 后端构建 + 前端构建
make server-build-linux-amd64
make web-build

# 服务状态
curl -sS https://<warden.example.com>/api/v1/health
curl -sS https://<warden.example.com>/api/v1/auth/oidc/config

# 审计抽查（示例）
psql "<dsn>" -c "select action, tenant_id, created_at from audit_events order by created_at desc limit 20;"

# 迁移登记检查
tail -5 deploy/release/migrations.txt
```

## 下一步

- [troubleshooting.md](troubleshooting.md)：按症状定位问题
- [installation.md](installation.md)：上线检查清单与回滚
- [oidc-setup.md](oidc-setup.md)：SSO 配置变更
- [authorization.md](authorization.md)：角色与会话生效规则
