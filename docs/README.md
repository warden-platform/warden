# 文档地图

这里是 Warden Warden 的**文档入口**。目标很简单：新人看完能回答三个问题——这个项目是干什么的、我怎么把它跑起来、它为什么这样设计。

## 组织方式

文档按 [Diátaxis](https://diataxis.fr/) 的四类读者需求分开写，互不混杂。这也是 Teleport、Backstage、Grafana 等成熟开源项目的通用做法：README 只做短门户，真正的入门、操作、参考、解释各自成篇。

| 类型 | 回答的问题 | 写法要求 |
|---|---|---|
| **教程 Tutorial** | "教我，我是新来的" | 线性步骤、每步都有可验证结果、不给选择题 |
| **操作指南 How-to** | "帮我完成 X" | 目标先行、假设读者已懂基础、只讲动作不讲原理 |
| **参考 Reference** | "Y 的准确取值是什么" | 表格化、完整、可检索、不夹叙夹议 |
| **解释 Explanation** | "为什么是这样设计的" | 讲背景、取舍与边界，不放操作步骤 |

一篇文档只做一件事。教程中途不列举全部配置项（那是参考），操作指南不展开原理（那是解释），参考不写"让我们一步步来"（那是教程）——需要时用链接串起来。

## 按角色选路径

| 你是…… | 建议顺序 |
|---|---|
| 第一天接手项目的开发者 | [quickstart](quickstart.md) → [concepts](concepts.md) → [architecture](architecture.md) → [api](api.md) |
| 要部署到生产的人 | [installation](installation.md) → [configuration](configuration.md) → [operations](operations.md) |
| 要接 SSO / 配账号权限的人 | [oidc-setup](oidc-setup.md) → [authorization](authorization.md) |
| 要接入新目标系统 / Agent 的人 | [onboarding](onboarding.md) → [actions](actions.md) → [api](api.md) |
| 值班排障 / 运维 | [troubleshooting](troubleshooting.md) → [operations](operations.md) → [configuration](configuration.md) |
| 想了解现状与未来 | [roadmap](roadmap.md) → [architecture](architecture.md) |
| 想贡献代码 | [`../CONTRIBUTING.md`](../CONTRIBUTING.md) → [architecture](architecture.md) |

## 完整目录

### 教程

| 文档 | 面向谁 | 何时维护 |
|---|---|---|
| [quickstart.md](quickstart.md) | 第一次在本地运行项目的开发者 | 启动方式、端口、密钥文件或迁移流程变化时 |

### 操作指南

| 文档 | 面向谁 | 何时维护 |
|---|---|---|
| [installation.md](installation.md) | 部署者 | 镜像、反向代理、TLS 或上线清单变化时 |
| [oidc-setup.md](oidc-setup.md) | 接入企业 IdP 的管理员 | OIDC 配置项、回调、角色规则或常见错误变化时 |
| [onboarding.md](onboarding.md) | 接入目标系统的管理员 | Connector 类型、配对流程或 Agent 接入变化时 |
| [operations.md](operations.md) | 运维 / 值班 | 迁移、备份、升级、密钥轮换流程变化时 |
| [troubleshooting.md](troubleshooting.md) | 排障者 | 出现新的高频故障或错误码时 |

### 参考

| 文档 | 面向谁 | 何时维护 |
|---|---|---|
| [configuration.md](configuration.md) | 所有人 | 新增 / 修改 / 删除环境变量时 |
| [api.md](api.md) | 前后端与集成方 | 路由、请求结构、权限守卫或错误码变化时 |
| [actions.md](actions.md) | 集成方与策略维护者 | 新增动作、动作版本或默认策略行为变化时 |
| [glossary.md](glossary.md) | 所有人 | 引入新术语或术语含义变化时 |

### 解释

| 文档 | 面向谁 | 何时维护 |
|---|---|---|
| [concepts.md](concepts.md) | 所有新人 | 领域模型、租户/项目层级变化时 |
| [architecture.md](architecture.md) | 开发者与评审者 | 组件、执行链路、状态机或分层约定变化时 |
| [authorization.md](authorization.md) | 使用者、管理员与开发者 | 角色、能力清单或授权边界变化时 |
| [federated-authorization.md](federated-authorization.md) | 外部平台集成方 | 联邦绑定、断言登录或委托链路变化时 |
| [roadmap.md](roadmap.md) | 维护者与决策者 | 功能完成状态、优先级或已知设计债变化时 |

### 仓库根目录

| 文档 | 用途 |
|---|---|
| [`../README.md`](../README.md) | 项目门户：定位、30 秒理解、最短上手路径、文档导航 |
| [`../CONTRIBUTING.md`](../CONTRIBUTING.md) | 开发流程、验证命令、提交规范 |
| [`../SECURITY.md`](../SECURITY.md) | 安全策略与漏洞私下报告渠道 |
| [`../CHANGELOG.md`](../CHANGELOG.md) | 变更记录与发布状态 |
| [`../server/README.md`](../server/README.md) | Go 服务的入口与运行速查 |
| [`../web/README.md`](../web/README.md) | 管理端结构与命令速查 |

---

## 唯一权威来源

文档与代码冲突时，**以代码为准**，并顺手修正文档：

| 事实 | 权威位置 |
|---|---|
| REST 路由与权限守卫 | `server/internal/control/handler.go` 的 `RegisterRoutes` |
| 角色、Scope、能力清单 | `server/internal/control/authorization.go` |
| 环境变量与默认值 | `server/internal/config/`、`server/internal/control/`、`server/cmd/`，汇总在 [configuration.md](configuration.md) |
| 数据库表与枚举 | `server/migrations/*.sql` |
| 发布迁移清单 | `deploy/release/migrations.txt`（新增编号 ≥ 035 的迁移必须登记，有单测强制） |
| 前端菜单能力门控 | `web/src/app/AppSidebar.tsx` 的 `requires` 声明 |
| Connector 能力 / 动作 | `server/internal/policy/policy.rego`、`server/internal/domain/capability.go` |

**特别注意**：`audit_events.action` 是 PostgreSQL 枚举。新增审计动作名必须同时补一条 `ALTER TYPE audit_action ADD VALUE` 迁移，否则审计会静默丢失。

## 写作规范

1. **语言与术语**：中文正文，保留 Principal / Connector / Execution 等代码中的对象名；首次出现的术语给一句话解释并链接 [glossary.md](glossary.md)。
2. **区分状态**：任何专题必须标明「已实现 / 设计中 / 未实现 / 已知限制」，不要把规划写成现状。
3. **示例可复制但不泄密**：命令用 `<placeholder>`，不得出现真实密码、Token、Cookie、私钥、DSN、内网 IP、用户名、个人绝对路径或生产主机名。`example.com` 与 `127.0.0.1` 是允许的占位。
4. **每篇开头写清面向谁、读完能做什么**；结尾给「下一步」链接。
5. **链接用相对路径**，新增或改名文档时同步更新本页与 `README.md` 的导航。
6. **不重复**：配置只写进 configuration，动作只写进 actions，接口只写进 api；其他文档用链接引用，避免多处副本各自漂移。
7. **迁移编号、命令、路径必须与实际文件一致**；跑不通的命令比没有命令更糟。

## 版本控制与私有文档

本仓库 `.gitignore` 采用 **`docs/*` 默认排除 + 白名单放行** 的策略：只有已脱敏、可复用、需要随代码演进的文档才提交。历史报告、e2e 现场记录、环境接入笔记等**仅保留在本地**，不进入仓库。

新增文档后必须在 `.gitignore` 里加一行 `!docs/<文件名>.md`，否则它不会被提交。提交前确认：

1. 没有密码、Token、Cookie、私钥、数据库 DSN 或可用的示例密钥。
2. 没有真实内网地址、用户名、个人路径或 kubeconfig 路径。
3. 描述与当前代码、迁移编号和页面能力一致。
4. 临时计划、测试报告、截图没有被误加入暂存区。
5. 本页与根 `README.md` 的导航已包含新文档，且没有指向仅本地文件的链接。
