import type { ApiAction, ApiConnector, ApiExecution, ApiResource } from '../../../shared/api'
import { getExecution } from '../../../shared/api'
import { DATABASE_QUERY_AST_VERSION, DATABASE_QUERY_SOURCE_ALIAS, GENERIC_KUBERNETES_KINDS, type ExecutionStarter } from './constants'

// ActionPanel 纯函数与预填充加载器：无 React 依赖，便于各编辑器复用。

export function normalizedDatabaseQueryAst(value: unknown, resource: ApiResource): Record<string, unknown> {
  const ast = asRecord(value)
  return {
    ...ast,
    astVersion: DATABASE_QUERY_AST_VERSION,
    sources: [{ alias: DATABASE_QUERY_SOURCE_ALIAS, resourceId: resource.external_id }],
    select: Array.isArray(ast.select) ? ast.select : [],
    orderBy: Array.isArray(ast.orderBy) ? ast.orderBy : [],
    bindings: asRecord(ast.bindings),
    limit: typeof ast.limit === 'number' ? ast.limit : 50,
  }
}

export function connectorSupportsAction(connector: ApiConnector, action: ApiAction): boolean {
  const required = `${action.name}@v${action.version}`
  return connector.capabilities?.some((capability) => capability === required || capability === action.name) ?? false
}

export const actionNeedsUpgrade = (action: ApiAction) =>
  action.available === false && action.availability_code === 'CONNECTOR_UPGRADE_REQUIRED'

export function withTimeout<T>(promise: Promise<T>, milliseconds = 10_000): Promise<T> {
  return Promise.race([
    promise,
    new Promise<T>((_, reject) => window.setTimeout(() => reject(new Error('加载执行信息超时，请重试')), milliseconds)),
  ])
}

export function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : {}
}

export function genericPreviewableSignature(parameters: Record<string, unknown>): string {
  return JSON.stringify({
    apiVersion: parameters.apiVersion,
    kind: parameters.kind,
    namespace: parameters.namespace,
    name: parameters.name,
    expectedResourceVersion: parameters.expectedResourceVersion,
    manifest: parameters.manifest,
  })
}

export function networkInterfacePreviewSignature(actionName: string, resourceId: string, parameters: Record<string, unknown>): string {
  return JSON.stringify({
    actionName,
    resourceId,
    description: parameters.description,
    vlan: parameters.vlan,
    allowDisruptive: parameters.allowDisruptive,
    allowCritical: parameters.allowCritical,
    save: parameters.save,
  })
}

export async function waitForTerminalExecution(initial: ApiExecution): Promise<ApiExecution> {
  let current = initial
  const deadline = Date.now() + 30_000
  while (!['SUCCEEDED', 'FAILED', 'CANCELLED', 'EXPIRED', 'UNKNOWN'].includes(current.status)) {
    if (Date.now() >= deadline) throw new Error('读取当前 Grafana 配置超时，请重试')
    await new Promise((resolve) => window.setTimeout(resolve, 600))
    current = await getExecution(current.id)
  }
  return current
}

export async function loadGenericKubernetesParameters(
  resource: ApiResource,
  mutationAction: string,
  startExecution: ExecutionStarter,
): Promise<Record<string, unknown>> {
  const gvk = GENERIC_KUBERNETES_KINDS[resource.resource_type]
  if (!gvk) throw new Error(`资源类型 ${resource.resource_type} 不支持通用 Kubernetes 变更`)
  const execution = await startExecution({
    taskId: `ui-k8s-resource-prefill-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
    resourceId: resource.id,
    action: 'kubernetes.resource.get',
    actionVersion: 1,
    parameters: { apiVersion: gvk.apiVersion, kind: gvk.kind, name: resource.name, namespace: resource.namespace ?? '' },
    reason: '通用 Kubernetes 变更前读取当前资源与 resourceVersion',
  })
  const completed = await waitForTerminalExecution(execution)
  if (completed.status !== 'SUCCEEDED') throw new Error(completed.errorMessage || completed.errorCode || '读取 Kubernetes 当前资源失败')
  const resourceVersion = String(completed.result?.resourceVersion ?? '')
  if (!resourceVersion) throw new Error('Kubernetes 未返回 resourceVersion，无法安全变更')
  const base = {
    apiVersion: gvk.apiVersion,
    kind: gvk.kind,
    name: resource.name,
    namespace: resource.namespace ?? '',
    expectedResourceVersion: resourceVersion,
  }
  if (mutationAction === 'kubernetes.resource.delete') {
    return { ...base, propagationPolicy: 'Background', confirmResource: '' }
  }
  const manifest = asRecord(completed.result?.manifest)
  if (!Object.keys(manifest).length) throw new Error('Kubernetes 未返回可编辑 Manifest')
  return { ...base, manifest }
}

export async function loadKubernetesMutationParameters(
  resource: ApiResource,
  readAction: string,
  mutationAction: string,
  startExecution: ExecutionStarter,
): Promise<Record<string, unknown>> {
  const execution = await startExecution({
    taskId: `ui-k8s-prefill-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
    resourceId: resource.id,
    action: readAction,
    actionVersion: 1,
    parameters: { name: resource.name, namespace: resource.namespace ?? '' },
    reason: '变更前读取 Kubernetes 当前状态',
  })
  const completed = await waitForTerminalExecution(execution)
  if (completed.status !== 'SUCCEEDED') throw new Error(completed.errorMessage || completed.errorCode || '读取 Kubernetes 当前状态失败')
  const expectedGeneration = Number(completed.result?.generation)
  if (!Number.isInteger(expectedGeneration) || expectedGeneration < 1) throw new Error('Kubernetes 未返回有效 generation，无法安全执行变更')
  if (mutationAction.endsWith('.scale')) {
    const replicas = Number(completed.result?.desiredReplicas)
    if (!Number.isInteger(replicas) || replicas < 0) throw new Error('Kubernetes 未返回有效副本数，无法安全扩缩容')
    return { expectedGeneration, replicas }
  }
  return { expectedGeneration }
}

export async function loadGrafanaUpdateParameters(
  resource: ApiResource,
  readAction: string,
  updateAction: string,
  startExecution: ExecutionStarter,
): Promise<Record<string, unknown>> {
  const execution = await startExecution({
    taskId: `ui-prefill-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
    resourceId: resource.id,
    action: readAction,
    actionVersion: 1,
    parameters: { name: resource.name, namespace: resource.namespace ?? '' },
    reason: '更新表单读取当前 Grafana 配置',
  })
  const completed = await waitForTerminalExecution(execution)
  if (completed.status !== 'SUCCEEDED') throw new Error(completed.errorMessage || completed.errorCode || '读取当前 Grafana 配置失败')
  return grafanaPrefillParameters(updateAction, completed.result ?? {}, resource)
}

function grafanaPrefillParameters(actionName: string, result: Record<string, unknown>, resource: ApiResource): Record<string, unknown> {
  if (actionName === 'grafana.folder.update') {
    const version = Number(result.version)
    if (!Number.isInteger(version) || version < 0) throw new Error('Grafana 未返回有效的 Folder version，无法安全更新')
    return { title: String(result.title ?? resource.metadata?.title ?? resource.name), version }
  }
  if (actionName === 'grafana.dashboard.update') {
    const dashboard = stripGrafanaSensitiveFields(asRecord(result.dashboard))
    const meta = asRecord(result.meta)
    const folderUid = String(meta.folderUid ?? resource.metadata?.folderUid ?? '')
    return { dashboard, ...(folderUid ? { folderUid } : {}), message: '通过 Warden 更新 Grafana Dashboard', overwrite: true }
  }
  const datasource = stripGrafanaSensitiveFields(asRecord(result.datasource))
  return {
    datasourceName: String(datasource.name ?? resource.name),
    type: String(datasource.type ?? resource.metadata?.type ?? ''),
    url: String(datasource.url ?? ''),
    access: String(datasource.access ?? 'proxy'),
    isDefault: Boolean(datasource.isDefault),
    ...(Object.keys(asRecord(datasource.jsonData)).length ? { jsonData: asRecord(datasource.jsonData) } : {}),
  }
}

function stripGrafanaSensitiveFields(value: Record<string, unknown>): Record<string, unknown> {
  const safe: Record<string, unknown> = {}
  for (const [key, item] of Object.entries(value)) {
    const normalized = key.toLowerCase().replaceAll('_', '').replaceAll('-', '')
    if (['securejsondata', 'securejsonfields', 'password', 'basicauthpassword', 'token', 'apikey', 'authorization'].includes(normalized))
      continue
    safe[key] = stripGrafanaSensitiveValue(item)
  }
  return safe
}

function stripGrafanaSensitiveValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stripGrafanaSensitiveValue)
  if (value && typeof value === 'object') return stripGrafanaSensitiveFields(asRecord(value))
  return value
}

export function isCloudLogAction(actionName: string) {
  return actionName === 'huawei.lts.logs.query' || actionName === 'aliyun.sls.logs.query'
}

export function isCloudLogTimeParameter(actionName: string, key: string) {
  return (
    (actionName === 'huawei.lts.logs.query' && (key === 'startTime' || key === 'endTime')) ||
    (actionName === 'aliyun.sls.logs.query' && (key === 'from' || key === 'to'))
  )
}

export function epochToLocalInput(epoch: number) {
  if (!Number.isFinite(epoch) || epoch <= 0) return ''
  const date = new Date(epoch)
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60 * 1000)
  return local.toISOString().slice(0, 19)
}
