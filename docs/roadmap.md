# 现状、边界与路线

> **面向**：维护者与做技术决策的人。
> **目标**：一眼看清哪些已经实现、哪些只是设计、哪些是明知道有债但暂时不动。
> 能力细节见 [architecture.md](architecture.md) 与 [actions.md](actions.md)，变更记录见 [CHANGELOG.md](../CHANGELOG.md)。

**状态口径**（全项目文档统一）：`已实现` 指代码里有且可用；`设计中 / 规划` 指文档里写了但没落地；`未实现` 指明确不做或尚未开始；`已知限制` 指能用但有边界。不要把规划写成现状。

---

## 已实现

| 领域 | 能力 |
|---|---|
| 控制面 | Resource / Action 查询、Execution 创建与查询、幂等键、内嵌 Rego、远程 OPA、不可变 PolicyDecision、AccessRequest 审批、一次性 AccessGrant、原子任务领取、HMAC TaskEnvelope |
| Connector Runtime | 注册 / 心跳 / 轮询、信封验签、多 Runner 路由、Namespace / Registry / Job Scope Guard、TokenRequest、非幂等 `UNKNOWN` 处理、60s 资源同步 |
| Linux / Helper | 主机、systemd、运行中 Docker 容器发现；结构化只读与生命周期动作；非 root Connector + root Helper 分权；配对、热更新、原子升级、安全卸载 |
| H3C 网络设备 | 设备 / 接口 / VLAN 发现；健康、LLDP、链路聚合、STP、端口、MAC/ARP 查询；按角色的固定只读审计 Profile 与敏感行净化 |
| 集群接入 | Web 表单 onboarding（兼容）、Operator + Helm Chart 配对向导、短期配对码、k3s Audience 自动识别、独立任务 Key、自动 Secret/SA/RBAC/Deployment 调谐 |
| Connector 生命周期 | 健康检查（90s 心跳）、Namespace 热更新、安全下线（`DELETE_REQUESTED → 清理 → DELETED → 归档`）、拒绝已下线 Connector 重新激活 |
| 凭据安全 | 每 Connector 独立 32 字节任务 Key、AES-GCM 加密存储、配对码与 bootstrap token 仅存 SHA-256、10 分钟过期 |
| 资源治理 | `sync_generation` + `last_seen_at`、ORPHANED 历史视图与保留期、归档 Connector 资源隐藏、执行目录按活跃心跳过滤、项目与环境继承链 |
| 部署与发布 | 本地 Docker Compose、K8s 基线清单、多阶段 Dockerfile / distroless 发布镜像、Helm Chart、Linux / Host / Network 安装脚本、发布迁移清单 |
| Admin Web | 管理员密码登录与 HttpOnly 会话、全量页面接真实 API（无 Mock）、拓扑图谱、分类资源目录、结构化 Action 面板、配对向导 |
| Agent 接入 | 每 Agent 独立 Hash 存储 API Key、Scope 最小权限、Execution 所有权隔离、撤销 / 轮换、`scripts/warden-agent.sh` 只读巡检脚本、Agent 身份管理页 |
| 多租户 | 租户 slug 隔离、`tenant_id` 上下文注入、租户管理接口与配额、平台级接口的 `RequirePlatformTenant` 守卫 |
| **OIDC / SSO** | 授权码 + PKCE S256、state/nonce 校验、联邦身份自动建档（稳定内部标识 + IdP 展示名 / 邮箱 / 头像）、RP-initiated logout；未配置 issuer 时行为与旧版完全一致 |
| **统一权限模型** | 角色 / Scope / `capabilities` 集中在 `authorization.go`；后端守卫与前端菜单读同一份能力清单；OIDC 默认角色禁止配成 `platform_admin` |
| **账号与权限** | 页面指派 `user` / `tenant_admin` / `platform_admin`；内置应急超管不可改、保留至少一个 SSO 超管、审计与变更同事务、立即撤销目标会话、账号 SQL 带 `tenant_id` |

## 未完成（按优先级）

1. **MFA 与 IdP claim 细粒度角色映射**：当前默认角色由 `WARDEN_OIDC_DEFAULT_ROLE` 静态配置（生产保持 `user`），尚未按 IdP claim 动态授权，MFA 未接入。
2. **Connector mTLS / SPIFFE 强认证**：当前只信任 Connector ID + bootstrap token。
3. **完整 Rego 策略版本管理**：可视化矩阵、冲突检测与模拟已完成；Rego Bundle 的草稿、发布、回滚未实现。
4. **Execution Attempt、`RUNNING`、超时回收与取消协议**：状态常量已在领域模型中，但完整协议未落地。
5. **结果脱敏与日志敏感信息检测**：Docker / systemd 日志目前只有行数与大小限制。
6. **Docker 已停止容器发现**：`docker.container.list` 当前只返回运行中容器。
7. **Outbox Publisher 后台循环**：Outbox 写入可开关，发布消费者未实现。

### 授权与身份的第二阶段（规划）

- 优先级约定：**手工指派 > IdP claim 同步 > 默认 `user`**。目前只实现了首尾两级。
- 按飞书 / IdP 组织架构自动分配角色与租户：等上游能稳定提供部门 / 组信息再接。
- **SCIM 2.0**（入职自动开通、离职即时停用）、细粒度 RLS、静态 `WARDEN_ADMIN_API_KEY` 在线轮换。
- 项目层 `PROJECT_ADMIN / OPERATOR / VIEWER` 合并进同一份能力清单，消除三套并行判定。

## 已知设计债

| 债 | 影响 | 现状 |
|---|---|---|
| 钉钉审批链路缺少租户上下文 | 多租户下审批可能 404、审计 `tenant_id` 为空 | 单租户下不触发 |
| 联邦断言登录签发的短 JWT 未写 `admin_sessions` | 该 token 暂不能调用 `/api/v1/` | 已在文档登记，见 [federated-authorization.md](federated-authorization.md) |
| 项目权限存在 `projectAccess` / `principalProjectAccess` / `project_scope` 三套并行判定 | 同一账号在不同页面可能被当成不同级别；成员强制开关默认关闭 | 第二阶段合并 |
| `admin_sessions` 无 `tenant_id` 列 | 会话维度不是租户感知的 | 已知，未改 |
| 租户过滤靠各仓储 SQL，没有统一 RLS | 新增接口容易漏 `WHERE tenant_id` | 开第二个租户前应做覆盖审计 |
| `audit_events.action` 是 PostgreSQL 枚举 | 每个新审计动作名都必须配套迁移，否则审计静默丢失 | 已有单测约束迁移清单，但枚举仍需手工补 |
| 静态 `WARDEN_ADMIN_API_KEY` 是全局共享、不可在线轮换的超管凭据 | 泄露影响面大 | 限制存放范围，登记风险；内置 `admin` 按约定保持不变 |

## 与外部组件的关系

| 组件 | 角色 | 状态 |
|---|---|---|
| PostgreSQL | 唯一必需外部服务，业务主数据与审计 | 已实现 |
| 独立 OPA | 远程策略决策 | Provider 已实现，Bundle 发布未实现 |
| OpenBao | 静态 / 动态凭据签发 | 未集成 |
| Teleport | 基础设施身份与代理 | 未集成 |
| Jenkins | 构建执行 | 已实现结构化 Integration，待真实环境生产验收 |
| 通用 MCP Plugin / Remote MCP | 目标架构中的插件化接入 | 尚未实现（属目标架构文档） |

> 历史架构文档（如 `pam-target-plugin-identity-risk-orchestration-architecture.md`）描述的是**目标架构**，不代表当前实现；阅读时务必看文档里的基线日期与状态标注。

## 推荐扩展路线

```text
现有 K8s / Linux Connector 多 Runner Runtime（Scope / Credential / 限流 / 脱敏）
  -> Grafana 全量结构化管理 Integration
  -> Jenkins Integration 生产环境联调与故障注入
  -> PostgreSQL 参数化只读查询
  -> 云厂商范围与权限决策
  -> MySQL 只读
  -> 其他云厂商只读
  -> 各系统受控变更动作
  -> 生产化安全与故障验收
```

口径提醒：Jenkins / Grafana 提供全量结构化管理动作，数据库仅提供参数化只读查询。**全量管理不等于原始代理**：仍禁止任意脚本、任意 URL、任意 SQL、任意 API 和凭据外泄，变更动作继续受策略、审批与审计约束。

## 发布状态

当前仓库**尚无正式 Release / tag**，`CHANGELOG.md` 记录阶段性变更。生产部署以具体镜像 tag 与迁移清单为准，不要假设某个版本号对应哪次提交。生产编排文件不在本仓库内，见 [installation.md](installation.md)。

## 下一步

- [architecture.md](architecture.md)：设计取舍与完整链路
- [authorization.md](authorization.md)：当前权限规则（通俗版 + 技术附录）
- [operations.md](operations.md)：升级与回滚
- [troubleshooting.md](troubleshooting.md)：已知限制引发的症状
