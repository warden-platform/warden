# Agent Browser 执行手册

## 输入

- 产品文档：`README.md`
- 用例：`docs/e2e/02-test.md`
- Auth：优先复用认证文件声明的 state/session；本地密码登录使用权限为 `0600` 的 `.secrets/admin_password`，任何 Secret 不写入命令输出、测试报告或仓库
- Target：本地真实 control/Web 用于完整回归；生产 `https://pam-admin.example.com` 用于当前已发布版本冒烟与发布后验收

## 顺序

1. 检查 target 和 `/readyz` 可访问。
2. 尝试恢复认证文件声明的 state；无效时按认证文件登录。
3. 保存登录状态并验证刷新。
4. 按 `AUTH → NAV → OVERVIEW → TARGET → CONNECTOR → RESOURCE → K8S-MUTATION → LINUX/DOCKER → HUAWEI-ECS → JENKINS → GRAFANA → PROJECT → INTEGRATION → AGENT → EXECUTION → APPROVAL → POLICY → AUDIT → COMPAT → SESSION` 执行。
5. 记录页面 URL、可见文本、页面错误和失败请求。
6. 重要失败保存到仓库外的临时证据目录。
7. 退出登录并确认受保护路由失效。
8. 关闭 browser session。

## 约束

- 只执行明确无副作用的只读 Execution；无法确认时不提交。
- Agent/API Key 生命周期用例只使用 `e2e-` 前缀的隔离数据，并在结束时撤销、禁用。
- 项目生命周期用例只使用 `e2e-` 前缀的隔离数据；不修改真实 Connector 或命名空间归属。没有历史引用的项目在结束时删除；产生 Execution、审批、策略决策或审计引用的项目不能硬删除，应保留 `e2e-` 名称并在报告中记录原因和关联 Execution ID。
- 跨项目共享验收只能使用明确标识的测试资源。变更资源范围时必须始终保留原项目访问，记录原始 `mode` 和项目列表，验收结束后恢复原范围并回读确认。
- 第二租户验收使用唯一的 `e2e-tenant-` slug；测试管理员密码只通过受限 Secret 或进程内变量传递，不写入命令、报告、浏览器状态或仓库。验收结束后停用租户，并确认旧会话和重新登录都失效。
- 不批准或拒绝 Access Request。
- 不修改 Connector Scope、Integration 或策略规则。
- 通用浏览器回归中的 Jenkins 生命周期只验证动作区分、inline/SCM 表单、固定 Job 身份和删除二次确认，不修改现有 Job；`FULL-REAL-01` 另行使用 `pam-e2e-7213-*` 隔离 Job 执行 create/update/configure/delete 与清理闭环。
- Kubernetes 变更只针对 `pam-e2e-7213-*` 隔离 Namespace 中的临时 ConfigMap/Deployment/StatefulSet/CronJob 等白名单资源；通用 create/update 必须先执行 server-side dry-run 并保存 before/after 与 `previewHash`，update/delete 固定 `resourceVersion`，所有可逆动作恢复原值，临时资源通过受控 delete 清理，最后删除整个 Namespace。
- Linux systemd、Docker、Jenkins 与 Grafana 真实变更只使用预先确认的测试资源；先记录原状态/配置，执行后回读，再以显式反向动作恢复，禁止使用同名生产资源临时充当测试目标。
- 华为云 ECS 真实变更只针对本轮新建的 `pam-e2e-7213-*` 实例，固定 Region 和实例 ID；依次验证治理标签/备注、关机、开机、重启，最后使用受控删除动作同时清理公网 IP 和数据盘。删除为异步云操作：Execution 必须记录合法 Job ID，并以 `VERIFIED_ABSENT` 或 `ACCEPTED_ASYNC` 诚实表示即时回读结果；随后继续观察资源目录直至测试实例消失。创建失败或删除结果为 UNKNOWN 时记录资源 ID、禁止重试同一请求，并等待云端与目录收敛后再决定是否新增测试资源。
- 华为云 OBS 查询只针对 `pam-e2e-7213-*` 隔离桶；对象删除只针对预先确认的测试对象键，要求输入完整 objectKey 二次确认，审批通过后执行并回读确认，不得删除业务对象。跨 Region、错误 Bucket 身份和 IAM 权限不足返回明确错误且不对其他桶产生动作。
- 旧 Connector 兼容测试必须使用隔离 Connector 或可回退的版本化测试部署；先证明变更动作隐藏，再升级并证明动作出现，不修改现有生产 Connector 来制造旧版本状态。
- 主机/集群接入向导只验证打开、类型切换和取消，不提交配对码或安装命令。

## 输出

- 覆盖率、通过率、失败与阻塞用例。
- 每个失败项给出首个失败步骤和证据。
- 报告结尾固定给出 `test coverage = executed / planned`、`pass rate = passed / (passed + failed)` 和逐项失败清单；阻塞项不计入通过率。
