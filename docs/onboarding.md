# 操作指南：接入目标系统与 Agent

> **面向**：要把 Kubernetes / Linux / 网络设备接入 PAM，或让外部 Agent 调用 PAM 的管理员。
> **目标**：Connector 状态变为 `ACTIVE`，资源目录出现目标资源，Agent 能创建只读 Execution。
> 动作清单见 [actions.md](actions.md)，接口见 [api.md](api.md)，变量见 [configuration.md](configuration.md)。

## 通用原则

- **Connector 主动出站**：心跳 15s、任务轮询 2s、资源同步 5min；控制面不需要打通到客户内网的入站路径。
- **配对码 + bootstrap token 只存 SHA-256，10 分钟有效**；每个 Connector 拿到独立的 32 字节任务 Key，控制面用 AES-GCM 加密存储。
- **凭据不进控制面**：kubeconfig、Kubernetes Token、云 AK/SK 等留在目标侧；控制面只保存引用。
- 接入后资源由 Connector 发现并同步；`ORPHANED` 资源进入历史视图，默认不参与统计、不能发起执行。

---

## 路径 A：Kubernetes 集群（Operator + Web 配对，推荐）

### 1. 安装 Operator

```bash
helm upgrade --install pam-operator ./deploy/helm/pam-operator \
  --namespace pam-system \
  --create-namespace \
  --set controlPlaneURL=https://<pam.example.com> \
  --set image.repository=<registry>/pam-operator \
  --set image.tag=<tag> \
  --set connectorImage=<registry>/pam-connector:<tag>
```

`controlPlaneURL` 是 Chart 的必填值（模板会 `fail` 校验）。Operator 容器读取 `PAM_CONTROL_URL`、`PAM_CONNECTOR_IMAGE`、`PAM_OPERATOR_NAMESPACE`、`PAM_OPERATOR_ID` 等变量。

### 2. 取配对码并在 Web 确认

```bash
kubectl logs -n pam-system deploy/pam-operator -f
# PAM pairing code: ABCD-2345
```

进入 Admin Web `Connector → 接入集群`，输入配对码、集群名、环境、允许 Namespace 和镜像前缀。确认后 Operator 自动创建 Connector Secret、ServiceAccount、最小权限 RBAC 与 Deployment，Web 等待状态从 `DRAFT` 变为 `ACTIVE`。

### 3. 安全行为

- 配对码与 bootstrap token 在数据库中只存哈希，10 分钟失效。
- 独立任务 Key 只通过带 bootstrap token 的配对通道交付，并写入目标集群 Secret。
- Operator 会自动识别集群版本与 k3s Token Audience。
- Executor / Discoverer 不拥有 `secrets:list`；平台不提供 `secret.list` 动作（K8s List Secret 会返回 Secret 数据）。
- 应用 `deploy/kubernetes/` 里的测试基线清单前，必须修改 Namespace、镜像地址、`PAM_CONTROL_URL`、`PAM_ALLOWED_IMAGE_PREFIXES`、Token Audience 与签名 Key Secret。

---

## 路径 B：Linux 主机

### 1. 安装（目标主机 root）

```bash
curl -fsSL https://<pam.example.com>/downloads/install-linux.sh \
  | bash -s -- --control-url https://<pam.example.com>
```

生产必须 HTTPS；仅可信私网临时用 HTTP 时需显式追加 `--allow-insecure-http`。安装脚本会：

1. 下载 `pam-linux-connector` 与 `pam-linux-helper`（自动识别 amd64/arm64）、卸载脚本和 `SHA256SUMS`，校验通过才安装；
2. 创建 `pam-linux` 系统用户与 `/var/lib/pam-linux` 状态目录；
3. 把 `pam-linux` 加入 `systemd-journal` 组以读 journal；
4. 注册并启动 `pam-linux-helper.service` 与 `pam-linux-connector.service`（`ProtectSystem=strict`、`NoNewPrivileges`、`PrivateTmp`）；
5. 重复安装时先完整下载临时文件，再停服并原子替换，保留原配对状态。

### 2. 配对

1. Connector 首次启动读取 `/etc/machine-id`，发现 enabled 的 systemd 服务，向 `POST /api/v1/linux/pairings` 发起配对；
2. 控制面生成 10 分钟配对码与独立任务 Key，Connector 日志输出配对码；
3. 管理员在 Web `Connector → 接入 Linux 主机` 输入配对码，查看主机信息与可管理服务列表，确认名称、环境和允许的服务子集；
4. `POST /api/v1/linux-pairings/confirm` 创建 `connector_type='linux'` 的记录（`DRAFT`）；
5. Connector 轮询确认结果，取回任务 Key 写入 `/var/lib/pam-linux/state.json`，标记 `READY`，开始注册与资源同步；
6. 发现三类资源：`linux_host`、允许管理的 `systemd_service`、Helper 返回的运行中 `docker_container`。

### 3. 卸载

```bash
sudo pam-linux-uninstall
```

---

## 路径 C：H3C 网络设备（Network Connector）

先在控制面预置 Connector（`POST /api/v1/connectors/network`），把一次性 `taskSigningKey` 写入受保护文件，然后在能访问设备管理网的内网主机执行安装器：

```bash
curl -fsSL https://<pam.example.com>/downloads/install-network.sh \
  | bash -s -- --control-url https://<pam.example.com> \
             --name <connector-name> \
             --environment production \
             --task-key-file /etc/pam-network/task-signing-key
```

systemd 注入 `PAM_CONTROL_URL`、`PAM_CONNECTOR_NAME`、`PAM_CONNECTOR_ENVIRONMENT`、`PAM_TASK_SIGNING_KEY_FILE`、`PAM_NETWORK_CREDENTIAL_DIR`。凭据引用只能指向 `pam:` 托管凭据或本地凭据目录中的文件名，并校验预登记的 Host Key SHA256 指纹。设备发现与审计使用固定 SSH 只读命令，不开放任意 CLI。

## 路径 D：macOS / Windows 主机

`deploy/host/install.sh` 强制 HTTPS，分别用 LaunchDaemon（macOS）与计划任务（Windows）注入 `PAM_CONTROL_URL` / `PAM_HOST_STATE_FILE`。二进制从 `/downloads/pam-host-connector/{platform}/{arch}` 获取。该 Connector 的 `host.command.run` 有严格白名单与强制审批，见 [actions.md](actions.md)。

## 旧版清单接入（仅迁移兼容）

`POST /api/v1/connectors/onboard` 默认返回 `410 LEGACY_ONBOARDING_DISABLED`。只有显式设置 `PAM_ENABLE_LEGACY_CONNECTOR_KEY=true` 才启用共享签名 Key 的旧流程；**仅用于迁移已有部署**，完成后必须关闭并轮换全局任务 Key。新集群一律用 Operator 配对。

---

## 注册 Integration（以 Jenkins 为例）

1. 先按上面任一路径接入 K8s 或 Linux Connector。
2. 在 Connector 详情的 `Integrations` 区域填写 Jenkins Endpoint、服务账号、Credential Reference 与 Job Scope。
3. 控制面以一等公民 `integrations` 表保存配置；Connector 每 10s 拉取并 diff、构建/移除 Runner，资源打上 `integration_id` 标签。
4. Token 必须提前放在目标侧，不通过前端或 pam-control 数据库：

```bash
# K8s Connector：Secret 与 pam-connector 同 Namespace，固定 key 为 token
kubectl create secret generic jenkins-api-token -n pam-system \
  --from-literal=token='<jenkins-api-token>'

# Linux Connector：Credential Reference 对应固定凭据目录中的文件
printf '%s' '<jenkins-api-token>' | sudo install -o root -g pam-linux -m 0640 \
  /dev/stdin /etc/pam-linux/credentials/jenkins-api-token
```

Grafana、数据库、华为云、阿里云 SLS 等同理：Integration 挂在已有 Connector 上，复用配对、身份、心跳、TaskEnvelope、审计与下线，不为每种服务重复创建 Connector。

## Connector 生命周期

| 操作 | API | 说明 |
|---|---|---|
| 健康检查 | `POST /api/v1/connectors/{id}/check` | 状态 ACTIVE 且心跳在 90 秒内 |
| 更新 Namespace | `PUT /api/v1/connectors/{id}/namespaces` | Operator 下次调谐自动增删 RBAC |
| 安全下线 | `DELETE /api/v1/connectors/{id}` | 标记 `DISABLED` + `DELETE_REQUESTED`，目标侧清理资源 |
| 归档 | `POST /api/v1/connectors/{id}/archive` | 目标资源清理完成后从列表隐藏，审计保留 |

下线流程：管理员发起删除 → Connector 禁用、关联资源禁用 → Operator / Linux Connector 轮询到 `DELETE_REQUESTED` 后清理目标侧 Deployment/Secret/SA/RoleBinding 并标记 `DELETED` → 管理员确认后归档。注册与心跳若发现 `DELETE_REQUESTED` 会拒绝激活，防止已下线 Connector 重新上线。

---

## 接入外部 Agent

### 1. 创建身份与凭据

```bash
# 管理员 Key 创建 Agent Principal
POST /api/v1/principals
POST /api/v1/principals/{id}/api-keys     # 明文只返回一次，DB 只存 SHA-256
```

每个 Agent 用自己独立的 Key；`PAM_ADMIN_API_KEY` 只用于平台初始化与应急，**不要注入 Agent**。资源注册机器人使用 audience 绑定、可轮换撤销的 Registration Key（`pam_rgk_` 前缀），由「Agent 身份 → 新增注册机器人」创建。

向 Multica 注入配置用 `scripts/configure-multica-agent-pam.sh`，脚本用 `****` 保留已有 `custom_env`，不会把 Key 放进命令参数或打印到终端。

### 2. 配置客户端

```text
PAM_API_ENDPOINT=https://<pam.example.com>/api/v1
PAM_AGENT_API_KEY=<该 Agent 的独立 Key>
```

Registration Key 的 audience 必须与当前 PAM API Endpoint 一致，复制本地 Key 到线上会稳定返回 401；有效但 Scope 不足返回 403 并记审计。

### 3. 常用调用（`scripts/pam-agent.sh`，依赖 `curl` + `jq`）

```bash
# 生成紧凑资源摘要
scripts/pam-agent.sh resource-summary [project] [environment] [resource_type]

# 按名称精确查找资源
scripts/pam-agent.sh find-resources nginx [project] [environment] [resource_type]

# 查某资源允许的动作与参数 Schema
scripts/pam-agent.sh list-actions <resource_id>

# 创建只读执行（客户端侧还有一层允许表保护）
scripts/pam-agent.sh create-readonly-execution deployment.get <resource_id> \
  '{"name":"nginx","namespace":"default"}' "巡检"

# 查询 / 轮询结果（意外进入审批会停止，不自动继续）
scripts/pam-agent.sh get-execution <execution_id>
scripts/pam-agent.sh wait-readonly-result <execution_id> 60
```

Agent Key 默认只能读资源、创建 / 读取 / 取消自己的 Execution；审批、全局审计、Connector 与 Principal 管理需要管理员会话或管理 Key。`/api/v1/health` 不需要认证。

## 验收清单

- [ ] Connector 状态 `ACTIVE`，心跳在 90 秒内
- [ ] 资源目录出现目标资源，且项目 / 环境归属正确
- [ ] `list-actions <resource_id>` 返回预期动作与参数 Schema
- [ ] 只读动作能创建 Execution 并进入终态 `SUCCEEDED`
- [ ] 生产变更动作按预期进入 `WAITING_APPROVAL`
- [ ] 执行与审计列表能看到 Principal / Credential 归属与 `trace_id`
- [ ] Agent Key 权限收敛：越权调用返回 403 且留下拒绝审计
- [ ] 下线 / 归档流程验证过，已下线 Connector 无法自行重新激活

## 下一步

- [actions.md](actions.md)：可用动作与默认策略
- [api.md](api.md)：Connector 与 Execution 接口
- [configuration.md](configuration.md)：Connector 端环境变量
- [troubleshooting.md](troubleshooting.md)：Connector 掉线 / 资源不出现
