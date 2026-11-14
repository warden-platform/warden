# 安全策略

> 本文说明受支持的版本、漏洞报告渠道与生产部署的安全边界。实现细节见 [docs/architecture.md](docs/architecture.md)，权限规则见 [docs/authorization.md](docs/authorization.md)，配置红线见 [docs/configuration.md](docs/configuration.md)。

## 受支持的版本

安全修复只应用到主开发分支的最新版本。当前项目**尚无正式 Release / tag**，请以部署时使用的镜像 tag 与迁移清单为准（见 [CHANGELOG.md](CHANGELOG.md)）。使用旧镜像或旧 Connector 二进制的环境应先升级再报告问题。

## 报告漏洞

**不要**在公开 Issue 里包含利用代码、凭据、内网地址或客户数据。请通过代码托管平台的私下渠道联系维护者，并提供：

- 受影响的版本或 commit；
- 受影响的组件与部署方式；
- 最小复现步骤；
- 预期影响与已知的缓解措施。

提交证据前删除真实凭据与个人数据。维护者会确认收悉、评估严重性、准备修复，并在受影响部署有合理升级窗口后协调公开通告。

## 部署边界

HighTower 代理的是特权基础设施操作。生产部署必须满足：

- 全站 TLS，`PAM_COOKIE_SECURE=true`；
- 每个 Secret 独立随机生成、通过 `*_FILE` 挂载，权限 600，不进镜像层、日志或文档；
- 云与 Kubernetes 身份遵循最小权限，网络出口受限；
- 反向代理后才启用 `PAM_TRUST_PROXY_HEADERS`，并收紧 `PAM_TRUSTED_PROXY_CIDRS`；
- `PAM_OIDC_DEFAULT_ROLE=user`，超级管理员只通过页面手工指派；
- 保留内置 `admin` 密码登录作为 IdP 故障时的 break-glass 通道，但日常不使用；
- 升级前做数据库备份，保留可回滚的镜像 ID 与前端产物。

**开发默认值不是生产安全基线**：本地教程为了跑通 HTTP 会关闭 Secure Cookie，切勿照搬到生产。完整上线清单见 [docs/installation.md](docs/installation.md)。

## 平台自身的安全设计

| 机制 | 说明 |
|---|---|
| 默认拒绝 | 内嵌 Rego 显式动作白名单；策略服务异常同样 DENY |
| 凭据不扩散 | Agent 永远拿不到目标系统长期凭据；凭据 AES-GCM 加密存储，明文不序列化、不写日志 |
| 任务签名与防重放 | 每 Connector 独立任务 Key、HMAC-SHA256 TaskEnvelope、时间窗、nonce 与参数哈希校验 |
| 会话保护 | HttpOnly + Secure + SameSite=Strict Cookie，登录响应不下发可用 JWT，写操作校验 Origin |
| SSO | 授权码 + PKCE S256 + state/nonce 校验，拒绝无 PKCE 降级 |
| 最小权限 | 默认 `user` 仅 4 个 Scope；OIDC 默认角色不能配成 `platform_admin` |
| 审计 | append-only 审计串联身份、决策、授权、执行；角色变更与审计在同一事务 |

## 已知风险登记

- 静态 `PAM_ADMIN_API_KEY` 是全局共享、当前无法在线轮换的超管凭据，应严格限制存放范围。
- 内置 `admin` 按固定 ID 判定特权，是刻意保留的应急入口。
- 租户过滤依赖各仓储 SQL，尚无统一 RLS；开启第二个租户前应做覆盖审计。
- 更多已知限制见 [docs/roadmap.md](docs/roadmap.md)。
