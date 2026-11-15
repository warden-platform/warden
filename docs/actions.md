# 动作与策略参考

> **面向**：集成方、Agent 开发者、策略维护者。用于查「系统支持哪些动作、版本多少、默认是否需要审批」。
> 这是参考手册。概念见 [concepts.md](concepts.md)，接口见 [api.md](api.md)，接入目标系统见 [onboarding.md](onboarding.md)。

## 通用规则

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

## Kubernetes

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

## Linux 主机

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

主机查询与 systemd 只读动作由**非 root** Connector 用固定参数的 `exec.CommandContext` 执行；生命周期动作经 `root:warden-linux 0660` 的 Unix Socket 交给 root Helper。Docker Socket 不授予 `warden-linux` 用户。生命周期动作只接受固定枚举与已发现的稳定资源标识，执行前后实时读状态并写 `_pam_safety` 审计摘要。不提供任意命令、Shell、`docker run/exec/remove`、镜像拉取或参数透传。

> 已知限制：`docker.container.list` 当前只返回运行中容器，不等同于 `docker ps -a`；日志结果目前只有行数/大小限制，尚未做敏感信息检测。

## Jenkins

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

## Grafana

Folder、Dashboard 的结构化查询与 CRUD，以及 Datasource 列表、查询和无 Secret 的 Prometheus/Loki Datasource CRUD；所有变更进入审批。Folder/Dashboard/Datasource 更新会先用只读动作取当前配置回填表单，更新目标 UID 固定为所选资源；Folder 更新携带当前 `version`，由 Grafana 乐观锁阻止并发覆盖。`secureJsonData`、密码、Token、API Key 与 Authorization 字段不会进入普通动作、Execution 参数、审计或数据库。

## 数据库

支持实例、Schema、Table/View 元数据读取，以及 `database.table.rows.query` 结构化单表行查询：

- 必须显式选择不超过 20 个真实字段；
- 只支持 AND 条件、有限排序、最多 100 行、1 MiB 结果；
- Connector 根据实时表结构校验标识符并参数化所有值；
- **不接受原始 SQL、JOIN、跨表查询、DML、DDL 或 dump**；生产仍应使用数据库原生只读账号。

该动作默认直接执行并完整审计，不进人工审批；原始行结果仅保留 10 分钟，Warden 长期保留的是身份、目标、参数、终态与审计，不作为业务数据副本。

## 华为云 / 阿里云 SLS

- **华为云**：账号级 Integration，AK/SK 只保存在 Connector 目标侧。Endpoint 作为种子 Region，默认发现 IAM 用户可访问的全部 Region，也可指定 Region 或排除列表。资源目录将 ECS/RDS/ELB/OBS/LTS 平级展示；LTS 自动发现日志组和日志流，查询动作挂载日志流，单次最多 500 条 / 4 MiB。ECS 支持创建、开机、软关机、软重启、治理标签/备注更新与删除，所有变更固定账号/Region/资源 ID 并进入审批；删除需完整 ECS 名称二次确认。RDS/ELB/OBS 创建暂不开放。
- **阿里云 SLS**：RAM 账号级 Integration，只需一个种子 Region。`scope.regionPolicy` 支持 `single` / `all` / `allowlist`；`all` 会并发探测官方 Region Endpoint 并用 `ListProject` 只保留 RAM 有权访问的 Project。资源目录按「账号 → Region → Project → Logstore」展示，查询动作挂载 Logstore。查询单次最多 500 条 / 4 MiB。不开放任意阿里云 API 或 CLI。

云日志原始结果同样只保留 10 分钟，长期日志仍保存在云平台。

## H3C 网络设备

`warden-network-connector` 提供设备、接口、VLAN 发现；设备健康、LLDP、链路聚合、STP、端口、MAC/ARP 查询。`network.device.audit` 支持按设备角色执行固定的 `standard` / `security` / `core` / `router` / `ac` / `aggregation` / `switch` / `poe` 只读审计 Profile，返回敏感行净化后的命令分段、风险提示与角色摘要。

交换机接口描述、Access VLAN、Trunk VLAN 变更**默认关闭**；开启后仍须经过实时预检、审批、固定命令执行与写后复核，不开放任意 CLI。凭据引用只能指向 `pam:` 托管凭据或 Connector 本地凭据目录中的文件名，并校验预登记的 Host Key SHA256 指纹。

## macOS / Windows 主机

`warden-host-connector` 的 `host.command.run` 允许调用方提供 program + args，但：

- program 必须落在 `allowedCommands` 白名单，路径与 Shell 元字符被拒绝；
- 参数有数量与长度限制，环境变量裁剪为白名单；
- 用 `exec.CommandContext(program, args...)` 直接执行，**没有 `/bin/sh -c`**；
- Rego 对该动作强制 `REQUIRE_APPROVAL`。

这是「不提供任意 Shell」口径下唯一需要特别说明的特例。

## 默认策略行为（内嵌 Rego）

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

## 变更安全契约

变更动作使用版本化契约 `pam.change-safety@v1`：

- 生产变更不可被策略矩阵放宽为免审批；
- Connector 在执行前后读取资源状态并返回 `_pam_safety` 摘要；
- 可回滚动作标记 `MANUAL`，不可逆动作标记 `NOT_SUPPORTED`；
- `UNKNOWN` 只触发只读状态重读，不自动重放变更。

## 项目与环境归属

每个服务 / 中间件资源同时具有项目与环境两个治理维度：

- 普通资源按「单资源覆盖 → Integration 默认环境 → Connector 环境」计算；
- 数据库按「表覆盖 → Schema 覆盖 → 实例覆盖 → Integration → Connector」继承；
- Integration 项目归属与执行通道项目解耦：执行通道只决定任务在哪里执行；
- `consumer-projects` 可为同一资源追加其他项目的查看/触发权限，但不授予配置或删除治理权。

## 下一步

- [api.md](api.md)：创建 Execution 的请求体与错误码
- [onboarding.md](onboarding.md)：接入 Connector 与 Integration
- [architecture.md](architecture.md)：策略决策在链路中的位置
- [configuration.md](configuration.md)：`WARDEN_DECISION_MODE` 与远程 OPA 配置
