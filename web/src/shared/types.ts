export type Page = "overview" | "resources" | "policies" | "approvals" | "executions" | "connectors" | "integrations" | "audit"

export type Resource = {
  id: string
  name: string
  type: string
  environment: '开发' | '测试' | '生产'
  owner: string
  connector: string
  status: '正常' | '注意' | '历史'
  actions: string[]
}

export type ExecutionStatus = '等待审批' | '排队中' | '执行中' | '成功' | '拒绝' | '失败' | '结果未知'

export type Execution = {
  id: string
  taskId: string
  resource: string
  action: string
  actor: string
  status: ExecutionStatus
  createdAt: string
  duration: string
  risk: '低' | '中' | '高'
  parameters: string
  result?: Record<string, unknown>
  errorMessage?: string
}

export type Approval = {
  id: string
  executionId: string
  requestor: string
  resource: string
  action: string
  reason: string
  risk: '低' | '中' | '高'
  expires: string
  parameters: string
}

export type AuditItem = {
  id: string
  time: string
  category: string
  event: string
  actor: string
  result: '允许' | '拒绝' | '完成' | '待处理' | '系统'
  traceId: string
}

// Connector types are persisted by the API as strings so newer connector
// implementations remain forward-compatible. Keep their presentation in one
// place so network connectors are never mistaken for Kubernetes targets.
export function connectorTypeLabel(type: string): string {
  switch (type.toLowerCase()) {
    case 'network': return 'H3C 网络设备'
    case 'linux': return 'Linux 主机'
    case 'kubernetes': return 'Kubernetes 集群'
    default: return type
  }
}

export function connectorChannelPrefix(type: string): string {
  switch (type.toLowerCase()) {
    case 'network': return 'pam-network-connector'
    case 'linux': return 'pam-linux-connector'
    case 'kubernetes': return 'pam-connector'
    default: return 'pam-connector'
  }
}

// --- API status mapping helpers ---

// Maps backend ExecutionStatus (uppercase snake) to the Chinese label the UI
// already uses. Unknown statuses fall back to the raw string so new backend
// states are still visible.
export function mapExecutionStatus(s: string): ExecutionStatus {
  switch (s) {
    case 'WAITING_APPROVAL': return '等待审批'
    case 'QUEUED': return '排队中'
    case 'DISPATCHED':
    case 'RUNNING': return '执行中'
    case 'SUCCEEDED': return '成功'
    case 'FAILED': return '失败'
    case 'CANCELLED':
    case 'CANCELLING':
    case 'EXPIRED': return '失败'
    case 'UNKNOWN': return '结果未知'
    default: return '排队中'
  }
}

export function mapConnectorStatus(s: string): string {
  switch (s) {
    case 'ACTIVE': return '健康'
    case 'DRAFT': return '草稿'
    case 'TESTING': return '测试中'
    case 'DEGRADED': return '降级'
    case 'QUARANTINED': return '隔离'
    case 'DISABLED': return '已停用'
    default: return s
  }
}

export function mapResourceStatus(s: string): '正常' | '注意' | '历史' {
  switch (s) {
    case 'ACTIVE': return '正常'
    case 'ORPHANED': return '历史'
    case 'DISCOVERED':
    case 'DISABLED': return '注意'
    default: return '注意'
  }
}

export function mapEnvironment(env: string): '开发' | '测试' | '生产' {
	return env === 'production' ? '生产' : env === 'development' ? '开发' : '测试'
}

// Maps API action name (e.g. deployment.update_image) to the Chinese label
// the UI uses in the action selector.
export function mapActionName(name: string): string {
  switch (name) {
    case 'deployment.get': return '查看 Deployment'
    case 'deployment.update_image': return '更新镜像'
    case 'deployment.restart': return '重启 Deployment'
    case 'deployment.scale': return '扩缩容 Deployment'
    case 'deployment.rollout_status': return '查看发布状态'
    case 'pod.logs.read': return '读取 Pod 日志'
    case 'pod.get': return '查看 Pod'
    case 'pod.events': return '查看 Pod 事件'
    case 'event.list': return '列出 Kubernetes 事件'
    case 'statefulset.get': return '查看 StatefulSet'
    case 'statefulset.restart': return '重启 StatefulSet'
    case 'statefulset.scale': return '扩缩容 StatefulSet'
    case 'statefulset.update_image': return '更新 StatefulSet 镜像'
    case 'daemonset.get': return '查看 DaemonSet'
    case 'replicaset.get': return '查看 ReplicaSet'
    case 'job.get': return '查看 Kubernetes Job'
    case 'cronjob.get': return '查看 CronJob'
    case 'cronjob.suspend': return '暂停 CronJob'
    case 'cronjob.resume': return '恢复 CronJob'
    case 'cronjob.trigger': return '手动触发 CronJob'
    case 'service.get': return '查看 Service'
    case 'endpoints.get': return '查看 Endpoints'
    case 'ingress.get': return '查看 Ingress'
    case 'configmap.get': return '查看 ConfigMap'
    case 'kubernetes.resource.get': return '读取通用 Kubernetes 资源'
    case 'kubernetes.resource.list': return '列出通用 Kubernetes 资源'
    case 'kubernetes.resource.preview': return '预检 Kubernetes 变更'
    case 'kubernetes.resource.create': return '创建 Kubernetes 资源'
    case 'kubernetes.resource.update': return '更新 Kubernetes 资源'
    case 'kubernetes.resource.delete': return '删除 Kubernetes 资源'
    case 'node.get': return '查看 Node'
    case 'namespace.list': return '列出 Namespace'
    case 'linux.host.info': return '查看主机信息'
    case 'linux.process.list': return '查看主机进程'
    case 'linux.disk.usage': return '查看磁盘使用量'
    case 'systemd.service.status': return '查看 systemd 服务状态'
    case 'systemd.service.logs': return '读取 systemd 服务日志'
    case 'docker.container.list': return '列出 Docker 容器'
    case 'docker.container.inspect': return '查看 Docker 容器详情'
    case 'docker.container.logs': return '读取 Docker 容器日志'
    case 'jenkins.controller.get': return '查看 Jenkins 控制器'
    case 'jenkins.job.get': return '查看 Jenkins Job'
    case 'jenkins.job.config.get': return '读取 Jenkins Job 配置'
    case 'jenkins.job.create': return '创建 Jenkins Job'
    case 'jenkins.job.update': return '更新 Jenkins Pipeline'
    case 'jenkins.job.configure': return '轻量配置 Jenkins Job'
    case 'jenkins.job.delete': return '删除 Jenkins Job'
    case 'jenkins.job.enable': return '启用 Jenkins Job'
    case 'jenkins.job.disable': return '禁用 Jenkins Job'
    case 'jenkins.job.build': return '触发 Jenkins Build'
    case 'jenkins.build.get': return '查看 Jenkins Build'
    case 'jenkins.build.logs': return '读取 Jenkins Build 日志'
    case 'jenkins.build.stop': return '停止 Jenkins Build'
    case 'jenkins.build.retry': return '重试 Jenkins Build'
    case 'jenkins.queue.get': return '查看 Jenkins 队列项'
    case 'jenkins.queue.cancel': return '取消 Jenkins 队列项'
    case 'jenkins.node.get': return '查看 Jenkins Node'
    case 'jenkins.node.set_offline': return '设置 Jenkins Node 上下线'
    case 'jenkins.credential_reference.get': return '查看 Jenkins Credential 引用'
    case 'grafana.instance.get': return '查看 Grafana 实例'
    case 'grafana.folder.list': return '列出 Grafana Folder'
    case 'grafana.folder.get': return '查看 Grafana Folder'
    case 'grafana.folder.create': return '创建 Grafana Folder'
    case 'grafana.folder.update': return '更新 Grafana Folder'
    case 'grafana.folder.delete': return '删除 Grafana Folder'
    case 'grafana.dashboard.list': return '列出 Grafana Dashboard'
    case 'grafana.dashboard.get': return '查看 Grafana Dashboard'
    case 'grafana.dashboard.export': return '导出 Grafana Dashboard'
    case 'grafana.dashboard.create': return '创建 Grafana Dashboard'
    case 'grafana.dashboard.update': return '更新 Grafana Dashboard'
    case 'grafana.dashboard.delete': return '删除 Grafana Dashboard'
    case 'grafana.datasource.list': return '列出 Grafana Datasource'
    case 'grafana.datasource.get': return '查看 Grafana Datasource 配置'
    case 'grafana.datasource.query': return '查询 Grafana Datasource'
    case 'grafana.datasource.create': return '创建 Grafana Datasource'
    case 'grafana.datasource.update': return '更新 Grafana Datasource'
    case 'grafana.datasource.delete': return '删除 Grafana Datasource'
    case 'pod.metrics': return '查看 Pod 实时指标'
    case 'node.metrics': return '查看 Node 实时指标'
    case 'namespace.dashboard_configmap.apply': return '发布 Grafana Dashboard ConfigMap'
    case 'configmap.dashboard.delete': return '删除 Grafana Dashboard ConfigMap'
    case 'database.instance.get': return '查看数据库实例'
    case 'database.schema.list': return '列出数据库 Schema'
    case 'database.schema.get': return '查看 Schema / Database'
    case 'database.table.list': return '列出数据库表'
    case 'database.table.describe': return '查看表结构'
    case 'database.table.rows.query': return '查询表数据（单表）'
    case 'database.query.preview': return '生成复杂查询预览'
    case 'database.query.execute': return '执行多表查询'
    case 'database.query_template.execute': return '执行查询模板'
    case 'database.schema.create.preview': return '预览创建 Schema'
    case 'database.schema.create': return '创建 Schema'
    case 'database.table.create.preview': return '预览创建数据表'
    case 'database.table.create': return '创建数据表'
    case 'huawei.account.get': return '查看华为云账号服务权限'
    case 'huawei.ecs.create': return '创建 ECS 云服务器'
    case 'huawei.ecs.get': return '查看 ECS 实例详情'
    case 'huawei.ecs.metrics': return '查看 ECS 监控指标'
    case 'huawei.ecs.start': return '启动 ECS'
    case 'huawei.ecs.stop': return '关闭 ECS'
    case 'huawei.ecs.reboot': return '重启 ECS'
    case 'huawei.ecs.update_metadata': return '更新 ECS 治理标签与备注'
    case 'huawei.ecs.delete': return '删除 ECS'
    case 'huawei.rds.get': return '查看 RDS 实例详情'
    case 'huawei.elb.get': return '查看 ELB 负载均衡详情'
    case 'huawei.obs.bucket.get': return '查看 OBS 桶用量'
    case 'huawei.lts.logs.query': return '查询华为云 LTS 日志'
    case 'huawei.swr.instance.get': return '查看 SWR 企业版实例详情'
    case 'huawei.swr.namespace.get': return '查看 SWR 命名空间详情'
    case 'huawei.swr.repository.get': return '查看 SWR 镜像仓库详情'
    case 'huawei.swr.tag.get': return '查看 SWR 镜像标签详情'
    case 'aliyun.sls.account.get': return '查看阿里云 SLS Project'
    case 'aliyun.sls.logs.query': return '查询阿里云 SLS 日志'
    case 'network.device.get': return '查看网络设备信息'
    case 'network.device.audit': return '审计网络设备'
    case 'network.device.health': return '检查网络设备健康状态'
    case 'network.interface.list': return '列出网络接口'
    case 'network.vlan.list': return '列出网络 VLAN'
    case 'network.topology.lldp': return '查看 LLDP 拓扑邻居'
    case 'network.link_aggregation.status': return '查看链路聚合状态'
    case 'network.stp.status': return '查看 STP 状态'
    case 'network.port.health': return '检查端口健康状态'
    case 'network.mac.lookup': return '查询设备 MAC 表'
    case 'network.arp.lookup': return '查询设备 ARP 表'
    case 'network.interface.description.preview': return '预览接口描述变更'
    case 'network.interface.description.update': return '修改接口描述'
    case 'network.interface.access_vlan.preview': return '预览 Access VLAN 变更'
    case 'network.interface.access_vlan.update': return '修改 Access VLAN'
    case 'network.interface.trunk_vlan.preview': return '预览 Trunk VLAN 变更'
    case 'network.interface.trunk_vlan.add': return '允许 Trunk VLAN'
    case 'network.interface.trunk_vlan.remove': return '移除 Trunk VLAN'
    default: return name
  }
}

export function mapRiskLevel(level: string): '低' | '中' | '高' {
  switch (level) {
    case 'low': return '低'
    case 'medium': return '中'
    case 'high': return '高'
    case 'critical': return '高'
    default: return '中'
  }
}

export function mapRiskHint(level: string): string {
  switch (level) {
    case 'low': return '只读操作，不修改目标资源'
    case 'medium': return '可能改变运行状态，请确认目标与参数'
    case 'high': return '高风险变更，通常需要审批并完整审计'
    case 'critical': return '关键变更，必须谨慎审批并准备回退方案'
    default: return '动作将按策略评估并记录审计'
  }
}

export function mapIntegrationStatus(s: string): string {
  switch (s) {
    case 'ACTIVE': return '运行中'
    case 'DISABLED': return '已停用'
    case 'FAILED': return '异常'
    default: return s
  }
}

export function mapProtocolLabel(protocol: string): string {
  switch (protocol.toLowerCase()) {
    case 'jenkins': return 'Jenkins'
    case 'h3c_comware_ssh': return 'H3C 网络设备'
    case 'grafana': return 'Grafana'
    case 'postgres':
    case 'postgresql': return 'PostgreSQL'
    case 'mysql': return 'MySQL'
    case 'huaweicloud': return '华为云'
    case 'aliyun_sls': return '阿里云 SLS'
    default: return protocol
  }
}
