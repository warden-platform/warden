// Display label helpers for resource types and action parameters.

export function parameterLabel(key: string, actionName = ''): string {
  if (actionName.startsWith('network.interface.')) {
    const label = ({
      description: '新接口描述', vlan: 'VLAN ID', allowDisruptive: '确认业务中断风险',
      allowCritical: '确认保护端口风险', save: '保存设备当前全部运行配置', operation: '操作', previewHash: '预检哈希',
    } as Record<string, string>)[key]
    if (label) return label
  }
  if (actionName === 'network.mac.lookup' && key === 'mac') return 'MAC 地址'
  if (actionName === 'network.arp.lookup' && key === 'ip') return 'IPv4 地址'
  if (actionName === 'network.device.audit' && key === 'profile') return '审计 Profile'
  return ({ container: '容器名称', image: '新镜像', replicas: '目标副本数', expectedGeneration: '预期资源代际', expectedResourceVersion: '预期 resourceVersion', apiVersion: 'API Version', kind: 'Kind', manifest: 'Manifest（YAML / JSON）', previewHash: '预检哈希', confirmResource: '二次确认：输入 Kind/Namespace/Name', propagationPolicy: '删除传播策略', labelSelector: 'Label Selector', fieldSelector: 'Field Selector', tailLines: '日志行数', description: '备注', quietPeriod: '静默期（秒）', parameters: '构建参数（JSON）', mode: 'Pipeline 来源', pipelineScript: 'Pipeline Script', scm: 'SCM 配置（JSON）', confirmJobName: '二次确认：输入完整 Job 名称', confirmServerName: '二次确认：输入完整 ECS 名称', deletePublicIP: '同时删除弹性公网 IP', deleteVolumes: '同时删除数据盘', offline: '临时离线', message: '变更说明', projectId: 'PAM 项目', region: 'Region', name: '资源名称', title: 'Folder 名称', flavor: '规格 ID', imageId: '镜像 ID', vpcId: 'VPC ID', subnetId: 'Subnet ID', securityGroupIds: '安全组 ID（JSON 数组）', systemDiskSize: '系统盘大小（GiB）', tags: '业务标签（JSON）', logGroupId: '日志组 ID', logStreamId: '日志流 ID', startTime: '开始时间', endTime: '结束时间', keywords: '关键词', limit: '返回条数', offset: '偏移行数', columns: '查询字段（JSON 数组）', filters: '过滤条件（JSON 数组）', orderBy: '排序规则（JSON 数组）', from: '开始时间', to: '结束时间', query: '查询表达式', line: '返回行数', datasourceName: 'Datasource 名称', datasourceUid: 'Datasource UID', type: 'Datasource 类型', url: 'Datasource URL', access: '访问模式', isDefault: '设为默认 Datasource', jsonData: '非敏感 JSON 配置', dashboardKey: 'Dashboard JSON 文件名', configMapName: 'ConfigMap 名称', dashboard: 'Dashboard JSON' } as Record<string, string>)[key] ?? key
}

export function parameterPlaceholder(key: string): string {
  return ({ container: '例如 backend', image: '例如 registry.example.com/team/app:v2', replicas: '0-1000', tailLines: '默认 100', parameters: '{"ENVIRONMENT":"testing"}', description: '用途、负责人或工单说明', quietPeriod: '0-3600', pipelineScript: 'pipeline { agent any; stages { ... } }', scm: '{"repoUrl":"ssh://git@example.com/team/app.git","credentialsId":"deploy-key","branch":"*/main","scriptPath":"Jenkinsfile"}', confirmJobName: '必须与上方目标 Job 名称完全一致', confirmServerName: '必须与上方目标 ECS 名称完全一致', message: '例如维护窗口', flavor: '例如 s6.large.2', imageId: '镜像 UUID', vpcId: 'VPC UUID', subnetId: '子网 UUID', tags: '{"service":"order-api"}', columns: '["uid", "amount", "cst_created"]', filters: '[{"column":"uid","operator":"eq","value":16035793}]', orderBy: '[{"column":"cst_created","direction":"desc"}]' } as Record<string, string>)[key] ?? ''
}

export function resourceTypeLabel(resourceType?: string): string {
  return ({
    pod: 'Kubernetes Pod', deployment: 'Kubernetes Deployment', event: 'Kubernetes 事件',
    statefulset: 'Kubernetes StatefulSet', daemonset: 'Kubernetes DaemonSet', replicaset: 'Kubernetes ReplicaSet',
    job: 'Kubernetes Job', cronjob: 'Kubernetes CronJob',
    service: 'Kubernetes Service', endpoints: 'Kubernetes Endpoints', ingress: 'Kubernetes Ingress',
    configmap: 'Kubernetes ConfigMap', networkpolicy: 'Kubernetes NetworkPolicy', poddisruptionbudget: 'Kubernetes PodDisruptionBudget', secret: 'Kubernetes Secret',
    node: 'Kubernetes Node', namespace: 'Kubernetes Namespace', systemd_service: 'systemd 服务', linux_host: 'Linux 主机',
    docker_container: 'Docker 容器', jenkins_controller: 'Jenkins 控制器', jenkins_job: 'Jenkins Job',
    jenkins_build: 'Jenkins Build', jenkins_queue_item: 'Jenkins 队列项', jenkins_node: 'Jenkins Node',
    jenkins_credential_reference: 'Jenkins Credential 引用',
    grafana_instance: 'Grafana 实例', grafana_folder: 'Grafana Folder',
    grafana_dashboard: 'Grafana Dashboard', grafana_datasource: 'Grafana Datasource',
    database_instance: '数据库实例', database_schema: '数据库 Schema', database_table: '数据库 Table / View',
    huawei_cloud_account: '华为云账号', huawei_ecs_instance: '华为云 ECS 实例', huawei_rds_instance: '华为云 RDS 实例',
    huawei_elb_loadbalancer: '华为云 ELB 负载均衡', huawei_obs_bucket: '华为云 OBS 桶',
    huawei_lts_log_group: '华为云 LTS 日志组', huawei_lts_log_stream: '华为云 LTS 日志流',
    huawei_swr_instance: '华为云 SWR 企业版实例', huawei_swr_namespace: '华为云 SWR 命名空间',
    huawei_swr_repository: '华为云 SWR 镜像仓库', huawei_swr_tag: '华为云 SWR 镜像标签',
    aliyun_sls_account: '阿里云 SLS Project', aliyun_sls_logstore: '阿里云 SLS Logstore',
    network_device: '网络设备', network_interface: '网络接口', network_vlan: '网络 VLAN',
  } as Record<string, string>)[resourceType ?? ''] ?? '资源'
}

const CLOUD_REGION_NAMES: Record<string, string> = {
  'cn-north-1': '华北-北京一', 'cn-north-4': '华北-北京四', 'cn-north-9': '华北-乌兰察布一', 'cn-north-12': '华北-乌兰察布',
  'cn-east-2': '华东-上海二', 'cn-east-3': '华东-上海一', 'cn-east-4': '华东区域', 'cn-east-5': '华东-青岛',
  'cn-south-1': '华南-广州', 'cn-south-4': '华南区域', 'cn-southwest-2': '西南-贵阳一',
  'ap-southeast-1': '中国-香港', 'ap-southeast-2': '亚太-曼谷', 'ap-southeast-3': '亚太-新加坡',
  'af-south-1': '非洲-约翰内斯堡', 'la-north-2': '拉美-墨西哥城', 'la-south-2': '拉美-圣地亚哥', 'tr-west-1': '土耳其-伊斯坦布尔',
}

export function cloudRegionLabel(region: string): string {
  const code = region.trim()
  if (!code) return '-'
  return CLOUD_REGION_NAMES[code] ? `${CLOUD_REGION_NAMES[code]}（${code}）` : code
}

const ALIYUN_REGION_NAMES: Record<string, string> = {
  'cn-qingdao': '华北1（青岛）', 'cn-beijing': '华北2（北京）', 'cn-zhangjiakou': '华北3（张家口）',
  'cn-huhehaote': '华北5（呼和浩特）', 'cn-wulanchabu': '华北6（乌兰察布）',
  'cn-hangzhou': '华东1（杭州）', 'cn-shanghai': '华东2（上海）', 'cn-nanjing': '华东5（南京）', 'cn-fuzhou': '华东6（福州）',
  'cn-shenzhen': '华南1（深圳）', 'cn-heyuan': '华南2（河源）', 'cn-guangzhou': '华南3（广州）',
  'cn-chengdu': '西南1（成都）', 'cn-hongkong': '中国香港',
  'ap-northeast-1': '日本（东京）', 'ap-southeast-1': '新加坡', 'ap-southeast-2': '澳大利亚（悉尼）',
  'ap-southeast-3': '马来西亚（吉隆坡）', 'ap-southeast-5': '印度尼西亚（雅加达）',
  'ap-southeast-6': '菲律宾（马尼拉）', 'ap-southeast-7': '泰国（曼谷）',
  'us-west-1': '美国（硅谷）', 'us-east-1': '美国（弗吉尼亚）',
  'eu-central-1': '德国（法兰克福）', 'eu-west-1': '英国（伦敦）',
  'me-east-1': '阿联酋（迪拜）', 'me-central-1': '沙特（利雅得）',
}

export function aliyunRegionLabel(region: string): string {
  const code = region.trim()
  if (!code) return '-'
  return ALIYUN_REGION_NAMES[code] ? `${ALIYUN_REGION_NAMES[code]}（${code}）` : code
}
