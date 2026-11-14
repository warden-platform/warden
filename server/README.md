# HighTower Server

> **面向**：要改后端代码的开发者。本文是服务端目录、入口与运行速查。
> 完整上手见 [`../docs/quickstart.md`](../docs/quickstart.md)，架构与分层见 [`../docs/architecture.md`](../docs/architecture.md)，全量变量见 [`../docs/configuration.md`](../docs/configuration.md)，接口见 [`../docs/api.md`](../docs/api.md)，文档地图见 [`../docs/README.md`](../docs/README.md)。

Go 模块同时构建控制面、Kubernetes / Linux / Host / Network Connector、Operator 与 Helper。

## 可执行入口

```text
cmd/pam-control/           REST 控制面
cmd/pam-connector/         Kubernetes Connector Runtime
cmd/pam-operator/          Kubernetes 集群配对与 Connector 生命周期管理
cmd/pam-linux-connector/   Linux 主机 Connector Runtime（非 root）
cmd/pam-linux-helper/      Linux systemd / Docker 固定动作 root Helper
cmd/pam-host-connector/    macOS / Windows 主机 Connector（固定命令白名单）
cmd/pam-network-connector/ H3C 网络设备 Connector Runtime（内网 SSH 只读）
```

## 内部包

```text
internal/control/             HTTP 路由和控制面编排
internal/domain/              领域对象、Hash 和 TaskEnvelope
internal/policy/              Embedded/Remote OPA Provider
internal/connector/           Runtime、TokenRequest 和 K8s Action
internal/storage/postgres/    pgx 存储适配器和 Outbox
```

依赖方向和新代码放置规则见根目录 [`README.md`](../README.md)。

## 命令

从仓库根目录执行：

```bash
make server-test
make server-vet
make server-build
```

构建产物写入 `server/bin/`，包括 `pam-control`、`pam-connector`、`pam-operator`、`pam-linux-connector`、`pam-network-connector` 和 `pam-linux-helper` 共 6 个；macOS / Windows 的 `pam-host-connector` 由 `make server-build-hosts` 单独交叉编译。

本地运行控制面：

```bash
PAM_DATABASE_URL_FILE=../.secrets/database_url \
PAM_TASK_SIGNING_KEY_FILE=../.secrets/task_signing_key \
PAM_JWT_SIGNING_KEY_FILE=../.secrets/jwt_signing_key \
PAM_CREDENTIAL_ENCRYPTION_KEY_FILE=../.secrets/credential_encryption_key \
PAM_ADMIN_API_KEY_FILE=../.secrets/agent_api_key \
PAM_ADMIN_PASSWORD_FILE=../.secrets/admin_password \
go run ./cmd/pam-control
```

所有 Secret 文件必须为非空文件；任务签名、JWT 签名、凭据加密和 Admin API Key
至少 32 字节且必须分别生成，管理员密码必须为 8-72 字节并建议使用高熵长密码。同一配置不能同时设置
明文变量和对应的 `*_FILE` 变量。

Kubernetes Connector 只能在 Pod 内使用 InCluster 身份运行，不读取本机 kubeconfig。Linux Connector 以非 root 用户运行；systemd 生命周期及 Docker 查询/生命周期能力由 root Helper 通过 `/run/pam-linux/helper.sock` 提供固定动作，只接受合法单元名或完整容器 ID，不开放任意 Shell 或参数透传。

Network Connector（`pam-network-connector`）部署在能访问设备管理网的内网主机上，主动向 PAM 控制面建立出站 HTTPS 连接；控制面不需要打通到交换机的入站路径。它使用 `PAM_CONTROL_URL`、`PAM_CONNECTOR_NAME`、`PAM_CONNECTOR_ENVIRONMENT` 和 `PAM_TASK_SIGNING_KEY`（或更安全的 `PAM_TASK_SIGNING_KEY_FILE`）启动，并可用 `PAM_NETWORK_CREDENTIAL_DIR` 指定本地凭据目录（默认 `/var/lib/pam-network/credentials`）。H3C Integration 的凭据引用只能指向 `pam:` 托管凭据或该目录中的文件名，并校验预登记的 Host Key SHA256 指纹。设备发现和审计使用固定 SSH 只读命令；当前只读动作还包括设备健康、LLDP、链路聚合、STP、端口健康以及按 MAC/IPv4 的单设备查询摘要。`network.device.audit` 还支持按角色选择 `standard`、`security`、`core`、`router`、`ac`、`aggregation`、`switch` 和 `poe` 固定审计 Profile，返回经过敏感行净化的命令分段、风险提示和角色摘要。交换机接口描述、Access VLAN 和 Trunk VLAN 变更默认关闭，开启后仍必须经过实时预检、审批、固定命令执行和写后复核，不开放任意 CLI。
Network Connector 安装器可从 `/downloads/install-network.sh` 获取；安装前先通过 `POST /api/v1/connectors/network` 预置 Connector，并把一次性 `taskSigningKey` 写入受保护文件，然后在内网主机执行安装器。

## 补充阅读

- 本地从零启动：[`../docs/quickstart.md`](../docs/quickstart.md)（**控制面启动不会自动跑迁移**）
- 全量环境变量：[`../docs/configuration.md`](../docs/configuration.md)
- 迁移与发布清单：[`../docs/operations.md`](../docs/operations.md)
- 目标系统接入：[`../docs/onboarding.md`](../docs/onboarding.md)
- 贡献与验证命令：[`../CONTRIBUTING.md`](../CONTRIBUTING.md)
