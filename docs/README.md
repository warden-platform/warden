# 文档地图

这里是 Warden 的**文档入口**。目标很简单：新人看完能回答三个问题——这个项目是干什么的、我怎么把它跑起来、它为什么这样设计。

> 当前文档体系：**9 篇用户文档**，按读者意图分类。操作指南、配置、动作等同类内容已合并为带章节锚点的单篇 hub 文档，减少散落与重复。

## 组织方式

文档按 [Diátaxis](https://diataxis.fr/) 的四类读者需求分开写，互不混杂。这也是 Teleport、Kubernetes、OPA 等成熟开源项目的通用做法：一篇文档只承担一种意图，长主题用**一篇带锚点的 hub 页**，而不是拆成一堆互相链接的短页。

| 类型 | 回答的问题 | 本项目的文档 |
|---|---|---|
| **教程 Tutorial** | "教我，我是新来的" | quickstart |
| **操作指南 How-to** | "帮我完成 X" | guides（含部署/接入/SSO/运维四部分）、troubleshooting |
| **参考 Reference** | "Y 的准确取值是什么" | reference（配置+动作）、api |
| **解释 Explanation** | "为什么是这样设计的" | concepts（含术语表）、architecture（含现状路线）、authorization（含联邦授权） |

一篇文档只做一件事。教程中途不列举全部配置项（那是参考），操作指南不展开原理（那是解释），参考不写"让我们一步步来"（那是教程）——需要时用链接串起来。

## 按角色选路径

| 你是…… | 建议顺序 |
|---|---|
| 第一天接手项目的开发者 | [quickstart](quickstart.md) → [concepts](concepts.md) → [architecture](architecture.md) → [api](api.md) |
| 要部署到生产的人 | [guides · 部署](guides.md#installation) → [reference · 配置](reference.md#configuration) → [guides · 运维](guides.md#operations) |
| 要接 SSO / 配账号权限的人 | [guides · SSO](guides.md#oidc) → [authorization](authorization.md) |
| 要接入新目标系统 / Agent 的人 | [guides · 接入](guides.md#onboarding) → [reference · 动作](reference.md#actions) → [api](api.md) |
| 值班排障 / 运维 | [troubleshooting](troubleshooting.md) → [guides · 运维](guides.md#operations) → [reference · 配置](reference.md#configuration) |
| 想了解现状与未来 | [architecture · 现状路线](architecture.md#roadmap) → [architecture](architecture.md) |
| 想贡献代码 | [`../CONTRIBUTING.md`](../CONTRIBUTING.md) → [architecture](architecture.md) |

## 完整目录

| 文档 | 类型 | 面向谁 | 何时维护 |
|---|---|---|---|
| [quickstart.md](quickstart.md) | 教程 | 第一次在本地运行项目的开发者 | 启动方式、端口、密钥文件或迁移流程变化时 |
| [guides.md](guides.md) | 操作指南 | 部署者、接入管理员、运维 | 部署 / 接入 / SSO / 运维任一流程变化时，改对应章节锚点 |
| [troubleshooting.md](troubleshooting.md) | 操作指南 | 排障者 | 出现新的高频故障或错误码时 |
| [reference.md](reference.md) | 参考 | 所有人、集成方、策略维护者 | 新增/修改环境变量或动作时，改对应章节锚点 |
| [api.md](api.md) | 参考 | 前后端与集成方 | 路由、请求结构、权限守卫或错误码变化时 |
| [concepts.md](concepts.md) | 解释 | 所有新人、需要查术语的人 | 领域模型、租户/项目层级或术语含义变化时 |
| [architecture.md](architecture.md) | 解释 | 开发者、评审者、决策者 | 组件、链路、状态机或功能完成状态变化时 |
| [authorization.md](authorization.md) | 解释 | 使用者、管理员、集成方 | 角色、能力清单或联邦授权边界变化时 |

### 根目录文档

| 文档 | 用途 |
|---|---|
| [`../README.md`](../README.md) | 项目门户：定位、30 秒理解、最短上手路径、文档导航 |
| [`../CONTRIBUTING.md`](../CONTRIBUTING.md) | 开发流程、验证命令、提交规范 |
| [`../SECURITY.md`](../SECURITY.md) | 安全策略与漏洞私下报告渠道 |
| [`../CHANGELOG.md`](../CHANGELOG.md) | 变更记录与发布状态 |
| [`../server/README.md`](../server/README.md) | Go 服务的入口与运行速查 |
| [`../web/README.md`](../web/README.md) | 管理端结构与命令速查 |

---

## hub 文档的章节锚点

合并后的长文档用锚点定位，链接格式为 `guides.md#installation`：

| 文档 | 锚点 |
|---|---|
| guides.md | `#installation` `#onboarding` `#oidc` `#operations` |
| reference.md | `#configuration` `#actions` |
| concepts.md | `#core-concepts` `#glossary` |
| architecture.md | `#architecture` `#roadmap` |
| authorization.md | `#authorization` `#federation` |

## 唯一权威来源

文档与代码冲突时，**以代码为准**，并顺手修正文档：

| 事实 | 权威位置 |
|---|---|
| REST 路由与权限守卫 | `server/internal/control/handler.go` 的 `RegisterRoutes` |
| 角色、Scope、能力清单 | `server/internal/control/authorization.go` |
| 环境变量与默认值 | `server/internal/config/`、`server/internal/control/`、`server/cmd/`，汇总在 [reference.md#configuration](reference.md#configuration) |
| 数据库表与枚举 | `server/migrations/*.sql` |
| 发布迁移清单 | `deploy/release/migrations.txt`（新增编号 ≥ 035 的迁移必须登记，有单测强制） |
| 前端菜单能力门控 | `web/src/app/AppSidebar.tsx` 的 `requires` 声明 |
| Connector 能力 / 动作 | `server/internal/policy/policy.rego`、`server/internal/domain/capability.go` |

**特别注意**：`audit_events.action` 是 PostgreSQL 枚举。新增审计动作名必须同时补一条 `ALTER TYPE audit_action ADD VALUE` 迁移，否则审计会静默丢失。

## 写作规范

1. **语言与术语**：中文正文，保留 Principal / Connector / Execution 等代码中的对象名；首次出现的术语给一句话解释并链接 [concepts.md#glossary](concepts.md#glossary)。
2. **区分状态**：任何专题必须标明「已实现 / 设计中 / 未实现 / 已知限制」，不要把规划写成现状。
3. **示例可复制但不泄密**：命令用 `<placeholder>`，不得出现真实密码、Token、Cookie、私钥、DSN、内网 IP、用户名、个人绝对路径或生产主机名。`example.com` 与 `127.0.0.1` 是允许的占位。
4. **每篇开头写清面向谁、读完能做什么**；合并文档在对应章节内做同样标注。
5. **链接用相对路径**，跨章节用 `文件.md#锚点`；新增或改名文档时同步更新本页与根 `README.md` 的导航。
6. **不重复**：配置只写进 reference 的配置章节，动作只写进动作章节，接口只写进 api；其他文档用链接引用，避免多处副本各自漂移。
7. **迁移编号、命令、路径必须与实际文件一致**；跑不通的命令比没有命令更糟。

## 版本控制与私有文档

本仓库 `.gitignore` 采用 **`docs/*` 默认排除 + 白名单放行** 的策略：只有已脱敏、可复用、需要随代码演进的文档才提交。历史报告、e2e 现场记录、环境接入笔记等**仅保留在本地**，不进入仓库。

新增文档后必须在 `.gitignore` 里加一行 `!docs/<文件名>.md`，否则它不会被提交。提交前确认：

1. 没有密码、Token、Cookie、私钥、数据库 DSN 或可用的示例密钥。
2. 没有真实内网地址、用户名、个人路径或 kubeconfig 路径。
3. 描述与当前代码、迁移编号和页面能力一致。
4. 临时计划、测试报告、截图没有被误加入暂存区。
5. 本页与根 `README.md` 的导航已包含新文档，且没有指向仅本地文件的链接。
