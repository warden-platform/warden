import { useCallback, useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from 'react'
import { parse as parseYaml, stringify as stringifyYaml } from 'yaml'
import {
  Activity, Bot, ChevronDown, FolderKanban, KeyRound, Loader2, Play, ShieldAlert, ShieldCheck, X,
} from 'lucide-react'
import type { ApiAction, ApiConnector, ApiError, ApiExecution, ApiQueryTemplate, ApiResource, CreateExecutionRequest } from '../../shared/api'
import {
  createExecution, getExecution, listAvailableActions, listAvailableResources, listConnectors, listProjects, listQueryTemplates, listResourceActions, listResources,
} from '../../shared/api'
import type { ApiProject } from '../../shared/api'
import { mapActionName, mapRiskHint, mapRiskLevel } from '../../shared/types'
import { useApp } from './Toast'
import { apiResourceToView } from '../lib/mappers'
import { cloudRegionLabel, parameterLabel, parameterPlaceholder, resourceTypeLabel } from '../lib/labels'
import { Badge } from './Badge'
import { ErrorState } from './States'
import { ExecutionResult } from './ExecutionResult'

const resourceNamespace = (resource: ApiResource) => resource.namespace ?? ''
type ParameterSchema = { type?: string; default?: unknown; description?: string; enum?: string[]; minimum?: number; maximum?: number }
type NetworkInterfacePreview = { before?: unknown; after?: unknown; commands?: unknown; save?: boolean; noOp?: boolean; previewHash: string; signature: string }
const GRAFANA_UPDATE_READ_ACTIONS: Record<string, string> = {
  'grafana.folder.update': 'grafana.folder.get',
  'grafana.dashboard.update': 'grafana.dashboard.get',
  'grafana.datasource.update': 'grafana.datasource.get',
}
const KUBERNETES_MUTATION_READ_ACTIONS: Record<string, string> = {
  'deployment.update_image': 'deployment.get',
  'deployment.restart': 'deployment.get',
  'deployment.scale': 'deployment.get',
  'statefulset.update_image': 'statefulset.get',
  'statefulset.restart': 'statefulset.get',
  'statefulset.scale': 'statefulset.get',
  'cronjob.suspend': 'cronjob.get',
  'cronjob.resume': 'cronjob.get',
  'cronjob.trigger': 'cronjob.get',
}
const GENERIC_KUBERNETES_KINDS: Record<string, { apiVersion: string; kind: string }> = {
  configmap: { apiVersion: 'v1', kind: 'ConfigMap' }, service: { apiVersion: 'v1', kind: 'Service' },
  deployment: { apiVersion: 'apps/v1', kind: 'Deployment' }, statefulset: { apiVersion: 'apps/v1', kind: 'StatefulSet' },
  daemonset: { apiVersion: 'apps/v1', kind: 'DaemonSet' }, job: { apiVersion: 'batch/v1', kind: 'Job' },
  cronjob: { apiVersion: 'batch/v1', kind: 'CronJob' }, ingress: { apiVersion: 'networking.k8s.io/v1', kind: 'Ingress' },
  networkpolicy: { apiVersion: 'networking.k8s.io/v1', kind: 'NetworkPolicy' }, poddisruptionbudget: { apiVersion: 'policy/v1', kind: 'PodDisruptionBudget' },
}
const GENERIC_KUBERNETES_KIND_OPTIONS = Object.values(GENERIC_KUBERNETES_KINDS)
const NETWORK_MUTATION_PREVIEW_ACTIONS: Record<string, string> = {
  'network.interface.description.update': 'network.interface.description.preview',
  'network.interface.access_vlan.update': 'network.interface.access_vlan.preview',
  'network.interface.trunk_vlan.add': 'network.interface.trunk_vlan.preview',
  'network.interface.trunk_vlan.remove': 'network.interface.trunk_vlan.preview',
}
const INTERNAL_PREVIEW_ACTIONS = new Set([
  'kubernetes.resource.preview',
  'database.query.preview',
  ...Object.values(NETWORK_MUTATION_PREVIEW_ACTIONS),
])
const DATABASE_QUERY_AST_VERSION = 1
const DATABASE_QUERY_SOURCE_ALIAS = 't'
type ExecutionStarter = (request: CreateExecutionRequest) => Promise<ApiExecution>

function normalizedDatabaseQueryAst(value: unknown, resource: ApiResource): Record<string, unknown> {
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

function connectorSupportsAction(connector: ApiConnector, action: ApiAction): boolean {
  const required = `${action.name}@v${action.version}`
  return connector.capabilities?.some((capability) => capability === required || capability === action.name) ?? false
}

const actionNeedsUpgrade = (action: ApiAction) => action.available === false && (action.availability_code === 'CONNECTOR_UPGRADE_REQUIRED' || action.availability_reason === '需要升级执行通道')

function withTimeout<T>(promise: Promise<T>, milliseconds = 10_000): Promise<T> {
  return Promise.race([promise, new Promise<T>((_, reject) => window.setTimeout(() => reject(new Error('加载执行信息超时，请重试')), milliseconds))])
}

// Lightweight action drawer triggered from resource context. Replaces the
// old global ActionDrawer: same execution form, but opened with a preselected
// resource instead of a top-level cascade.
export function ActionPanel({ initialResource, initialAction, initialActingProjectId, onClose }: { initialResource: ApiResource | null; initialAction?: string; initialActingProjectId?: string; onClose: () => void }) {
  const { notify, auth } = useApp()
  const isTenantAdmin = Boolean(auth.user?.is_platform_admin || auth.user?.role === 'tenant_admin')
  const [resources, setResources] = useState<ApiResource[]>([])
  const [connectors, setConnectors] = useState<ApiConnector[]>([])
  const [actions, setActions] = useState<ApiAction[]>([])
  const [projects, setProjects] = useState<ApiProject[]>([])
  const [actionId, setActionId] = useState('')
  const [connectorId, setConnectorId] = useState('')
  const [namespace, setNamespace] = useState('')
  const [resourceId, setResourceId] = useState(initialResource?.id ?? '')
  const [actingProjectId, setActingProjectId] = useState(initialActingProjectId ?? '')
  const [taskId] = useState(() => `ui-${Date.now().toString(36)}`)
  const [parameters, setParameters] = useState<Record<string, unknown>>({})
  const [reason, setReason] = useState('通过 PAM 管理界面发起')
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [execution, setExecution] = useState<ApiExecution | null>(null)
  // Resources are lazily loaded per resource_type (the catalog can be huge),
  // cached so switching between actions of the same type doesn't refetch.
  const typeCache = useRef<Record<string, ApiResource[]>>({})
  const [typeLoading, setTypeLoading] = useState(false)
  const [prefillLoading, setPrefillLoading] = useState(false)
  const [prefillError, setPrefillError] = useState('')
  const [prefillNonce, setPrefillNonce] = useState(0)
  const [manifestText, setManifestText] = useState('')
  const [manifestError, setManifestError] = useState('')
  const [genericPreview, setGenericPreview] = useState<{ before?: unknown; after?: unknown; previewHash: string; signature: string } | null>(null)
  const [networkPreview, setNetworkPreview] = useState<NetworkInterfacePreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  const [databasePreviewRequest, setDatabasePreviewRequest] = useState(0)
  const [databasePreviewLoading, setDatabasePreviewLoading] = useState(false)
  const [databasePreviewVerdict, setDatabasePreviewVerdict] = useState('')

  const loadType = (type: string): Promise<ApiResource[]> => {
    const cached = typeCache.current[type]
    if (cached) return Promise.resolve(cached)
    return withTimeout(listAvailableResources(type)).then((res) => {
      typeCache.current[type] = res ?? []
      return typeCache.current[type]
    })
  }

  useEffect(() => {
    withTimeout(Promise.all([isTenantAdmin ? listConnectors() : Promise.resolve([]), initialResource ? listResourceActions(initialResource.id, true) : listAvailableActions(), listProjects()]))
      .then(async ([conns, acts, projectItems]) => {
        setConnectors(conns ?? [])
        setActions(acts ?? [])
        setProjects(projectItems ?? [])
        const initialType = initialResource?.resource_type ?? (acts ?? [])[0]?.resource_type
        if (!initialType) return
        const res = await loadType(initialType)
        setResources(res)
        const first = res.find((r) => r.status === 'ACTIVE') ?? res[0]
        const initial = res.find((r) => r.id === initialResource?.id) ?? first
        if (initial) {
          setResourceId(initial.id)
          setConnectorId(initial.connector_id)
          setNamespace(initial.namespace ?? '')
          const requested = (acts ?? []).find((action) => action.name === initialAction && action.resource_type === initial.resource_type && action.available !== false && !INTERNAL_PREVIEW_ACTIONS.has(action.name))
          const availableForResource = (acts ?? []).filter((action) => action.resource_type === initial.resource_type && action.available !== false && !INTERNAL_PREVIEW_ACTIONS.has(action.name))
          const safestDefault = availableForResource.find((action) => action.risk_level === 'low') ?? availableForResource[0]
          setActionId(requested?.name ?? safestDefault?.name ?? '')
        }
      })
      .catch((e: ApiError) => setLoadError(e.message || '加载资源失败'))
      .finally(() => setLoading(false))
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const resource = useMemo(() => resources.find((r) => r.id === resourceId), [resources, resourceId])
  const resourceProjectIds = resource?.project_ids ?? []
  const createScopedExecution: ExecutionStarter = useCallback((request) => createExecution({ ...request, actingProjectId }), [actingProjectId])
  const connectorName = (id: string) => connectors.find((c) => c.id === id)?.name ?? ''

  useEffect(() => {
    setActingProjectId((current) => {
      if (initialActingProjectId && resourceProjectIds.includes(initialActingProjectId)) return initialActingProjectId
      if (current && resourceProjectIds.includes(current)) return current
      return resourceProjectIds.length === 1 ? resourceProjectIds[0] : ''
    })
  }, [resource?.id, initialActingProjectId]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    setGenericPreview(null)
    setNetworkPreview(null)
    setDatabasePreviewVerdict('')
    setPreviewError('')
    setParameters((current) => {
      if (!('previewHash' in current)) return current
      const next = { ...current }
      delete next.previewHash
      return next
    })
  }, [actingProjectId])
  // When opened with a preselected resource (from the resources page), lock the
  // action list to that resource's type so a Jenkins resource doesn't surface
  // K8s actions. Without initialResource (from ExecutionsPage "新建执行"), show all.
  const visibleActions = initialResource
    ? actions.filter((a) => a.resource_type === initialResource.resource_type && !INTERNAL_PREVIEW_ACTIONS.has(a.name) && a.name !== 'provider.request' && a.name !== 'provider.write')
    : actions.filter((a) => !INTERNAL_PREVIEW_ACTIONS.has(a.name) && a.name !== 'provider.request' && a.name !== 'provider.write')
  const selectedAction = visibleActions.find((a) => a.name === actionId)
  const controlPlaneAction = selectedAction?.executor_type === 'control_plane'
  const noAvailableAction = visibleActions.length > 0 && visibleActions.every((action) => action.available === false)
  const actionResources = resources.filter((item) => item.resource_type === selectedAction?.resource_type)
  // The server performs the authoritative 90-second heartbeat check when an
  // execution is created. Do not permanently disable a long-lived form from
  // the connector heartbeat snapshot captured when the drawer first opened.
  const actionConnectors: Array<Pick<ApiConnector, 'id' | 'name'>> = isTenantAdmin
    ? connectors.filter((connector) => connector.status === 'ACTIVE' && selectedAction && connectorSupportsAction(connector, selectedAction) && actionResources.some((item) => item.connector_id === connector.id))
    : [...new Set(actionResources.map((item) => item.connector_id).filter(Boolean))].map((id) => ({ id, name: '项目执行通道' }))
  const noExecutionTarget = Boolean(selectedAction && !controlPlaneAction && actionConnectors.length === 0)
  const namespaces = [...new Set(actionResources.filter((item) => controlPlaneAction || item.connector_id === connectorId).map(resourceNamespace))].sort()
  const targetResources = actionResources.filter((item) => controlPlaneAction || (item.connector_id === connectorId && resourceNamespace(item) === namespace))
  const actionName = selectedAction?.name ?? ''
  // Action definitions normally expose the base name while connector
  // capabilities use @vN. Normalize defensively so database-specific editors
  // remain visible if an API response carries the versioned capability key.
  const normalizedActionName = actionName.replace(/@v\d+$/, '')
  const jenkinsContextTargetLocked = Boolean(initialResource && actionName.startsWith('jenkins.'))
  const approvalExemptChange = normalizedActionName === 'jenkins.job.create'
  const protocolChange = !approvalExemptChange && (actionName.startsWith('jenkins.') || actionName.startsWith('grafana.')) && selectedAction?.risk_level !== 'low' && !actionName.endsWith('.query')
  const dashboardConfigChange = actionName === 'namespace.dashboard_configmap.apply' || actionName === 'configmap.dashboard.delete'
  const cloudChange = actionName.startsWith('huawei.ecs.') && selectedAction?.risk_level !== 'low'
  const genericKubernetesMutation = actionName === 'kubernetes.resource.create' || actionName === 'kubernetes.resource.update' || actionName === 'kubernetes.resource.delete'
  const genericKubernetesPreviewable = actionName === 'kubernetes.resource.create' || actionName === 'kubernetes.resource.update'
  const kubernetesChange = Boolean(KUBERNETES_MUTATION_READ_ACTIONS[actionName]) || genericKubernetesMutation
  const networkPreviewAction = NETWORK_MUTATION_PREVIEW_ACTIONS[normalizedActionName]
  const networkInterfaceMutation = Boolean(networkPreviewAction)
  const highRiskChange = !approvalExemptChange && (selectedAction?.risk_level === 'high' || selectedAction?.risk_level === 'critical')
  const databaseCostNeedsApproval = normalizedActionName === 'database.query.execute' && databasePreviewVerdict === 'require_approval'
  const needsApproval = Boolean(databaseCostNeedsApproval || highRiskChange || protocolChange || dashboardConfigChange || cloudChange || (!approvalExemptChange && resource?.environment === 'production' && selectedAction?.risk_level !== 'low'))
  const actionProperties = (selectedAction?.parameter_schema?.properties ?? {}) as Record<string, ParameterSchema>
  const requiredParameters = (selectedAction?.parameter_schema?.required ?? []) as string[]
  const nameIsEditable = actionName === 'jenkins.job.create' || actionName === 'huawei.ecs.create'
  const genericManagedFields = new Set(['apiVersion', 'kind', 'manifest', 'previewHash', 'expectedResourceVersion', 'operation'])
  const genericConcreteRead = actionName === 'kubernetes.resource.get' && resource?.resource_type !== 'namespace'
  const editableParameters = Object.entries(actionProperties).filter(([key]) => (key !== 'name' || nameIsEditable) && key !== 'namespace' && key !== 'expectedGeneration' && !(genericKubernetesMutation && genericManagedFields.has(key)) && !(genericConcreteRead && (key === 'apiVersion' || key === 'kind')) && !(selectedAction?.name === 'grafana.folder.update' && key === 'version') && !(normalizedActionName === 'database.table.rows.query' && (key === 'columns' || key === 'filters' || key === 'orderBy')) && !((normalizedActionName === 'database.query.execute' || normalizedActionName === 'database.query.preview') && (key === 'ast' || key === 'previewHash')) && !(networkInterfaceMutation && key === 'previewHash') && !(normalizedActionName === 'database.query_template.execute' && (key === 'templateId' || key === 'templateVersion' || key === 'bindings')))
  const resourceContainers = (Array.isArray(resource?.metadata?.containers) ? resource.metadata.containers : []) as { name: string; image: string }[]
  const selectedContainer = resourceContainers.find((item) => item.name === parameters.container)
  const deleteConfirmationMismatch = actionName === 'jenkins.job.delete' && String(parameters.confirmJobName ?? '') !== (resource?.name ?? '')
  const ecsDeleteConfirmationMismatch = actionName === 'huawei.ecs.delete' && String(parameters.confirmServerName ?? '') !== (resource?.name ?? '')
  const genericDeleteConfirmationMismatch = actionName === 'kubernetes.resource.delete' && String(parameters.confirmResource ?? '') !== `${String(parameters.kind ?? '')}/${String(parameters.namespace ?? '')}/${resource?.name ?? ''}`
  const databaseExecuteNeedsPreview = normalizedActionName === 'database.query.execute' && !/^[a-f0-9]{64}$/.test(String(parameters.previewHash ?? ''))
  const databaseSelects = asRecord(parameters.ast).select
  const databaseExecuteMissingFields = normalizedActionName === 'database.query.execute' && (!Array.isArray(databaseSelects) || databaseSelects.length === 0)
  const previewSignature = genericPreviewableSignature(parameters)
  const genericPreviewCurrent = Boolean(genericPreview && genericPreview.signature === previewSignature && parameters.previewHash === genericPreview.previewHash)
  const currentNetworkPreviewSignature = networkInterfacePreviewSignature(normalizedActionName, resource?.id ?? '', parameters)
  const networkPreviewCurrent = Boolean(networkPreview && networkPreview.signature === currentNetworkPreviewSignature && parameters.previewHash === networkPreview.previewHash)

  useEffect(() => {
    if (!resource || !selectedAction || !actingProjectId) return
    let cancelled = false
    const schemaProperties = (selectedAction.parameter_schema?.properties ?? {}) as Record<string, { default?: unknown }>
    const next: Record<string, unknown> = {}
    if ('name' in schemaProperties) next.name = resource.name
    if ('namespace' in schemaProperties) next.namespace = resource.resource_type === 'namespace' ? resource.name : resource.namespace ?? ''
    for (const [key, schema] of Object.entries(schemaProperties)) {
      if (schema.default !== undefined) next[key] = schema.default
      if (key === 'container' && resourceContainers.length > 0) next[key] = resourceContainers[0].name
    }
    if (selectedAction.name === 'jenkins.job.create') next.name = ''
    if (selectedAction.name === 'huawei.ecs.create') {
      next.name = ''
      next.region = String(resource.metadata?.region ?? '')
    }
    if (selectedAction.name === 'huawei.lts.logs.query') {
      next.startTime = Date.now() - 60 * 60 * 1000
      next.endTime = Date.now()
    }
    if (selectedAction.name === 'aliyun.sls.logs.query') {
      next.from = Math.floor(Date.now() / 1000) - 60 * 60
      next.to = Math.floor(Date.now() / 1000)
    }
    if (selectedAction.name.replace(/@v\d+$/, '') === 'database.query.execute') {
      next.ast = normalizedDatabaseQueryAst(next.ast, resource)
    }
    if (selectedAction.name.startsWith('kubernetes.resource.')) {
      const selectedKind = GENERIC_KUBERNETES_KINDS[resource.resource_type] ?? GENERIC_KUBERNETES_KIND_OPTIONS[0]
      next.apiVersion = selectedKind.apiVersion
      next.kind = selectedKind.kind
      if (selectedAction.name === 'kubernetes.resource.create' || (selectedAction.name === 'kubernetes.resource.preview' && resource.resource_type === 'namespace')) {
        if (selectedAction.name === 'kubernetes.resource.preview') next.operation = 'create'
        next.manifest = { apiVersion: selectedKind.apiVersion, kind: selectedKind.kind, metadata: { name: 'pam-e2e-7213-resource', namespace: resource.name } }
      }
    }
    setParameters(next)
    setGenericPreview(null)
    setNetworkPreview(null)
    setDatabasePreviewVerdict('')
    setPreviewError('')
    setManifestError('')
    setManifestText(next.manifest ? stringifyYaml(next.manifest) : '')
    const genericRead = selectedAction.name === 'kubernetes.resource.update' || selectedAction.name === 'kubernetes.resource.delete'
    const readAction = GRAFANA_UPDATE_READ_ACTIONS[selectedAction.name] || KUBERNETES_MUTATION_READ_ACTIONS[selectedAction.name] || (genericRead ? 'kubernetes.resource.get' : '')
    if (!readAction) {
      setPrefillLoading(false)
      setPrefillError('')
      return
    }
    setPrefillLoading(true)
    setPrefillError('')
    const loadParameters = genericRead
      ? loadGenericKubernetesParameters(resource, selectedAction.name, createScopedExecution)
      : KUBERNETES_MUTATION_READ_ACTIONS[selectedAction.name]
      ? loadKubernetesMutationParameters(resource, readAction, selectedAction.name, createScopedExecution)
      : loadGrafanaUpdateParameters(resource, readAction, selectedAction.name, createScopedExecution)
    loadParameters
      .then((loaded) => { if (!cancelled) { const merged = { ...next, ...loaded }; setParameters(merged); setManifestText(merged.manifest ? stringifyYaml(merged.manifest) : '') } })
      .catch((error: Error) => { if (!cancelled) setPrefillError(error.message || (KUBERNETES_MUTATION_READ_ACTIONS[selectedAction.name] ? '读取 Kubernetes 当前状态失败' : '读取当前 Grafana 配置失败')) })
      .finally(() => { if (!cancelled) setPrefillLoading(false) })
    return () => { cancelled = true }
  }, [resourceId, actionId, selectedAction?.id, prefillNonce, actingProjectId])

  useEffect(() => {
    if (!execution || ['SUCCEEDED', 'FAILED', 'CANCELLED', 'EXPIRED', 'UNKNOWN'].includes(execution.status)) return
    const timer = window.setInterval(() => {
      getExecution(execution.id).then(setExecution).catch(() => undefined)
    }, 1500)
    return () => window.clearInterval(timer)
  }, [execution])

  const chooseAction = (name: string) => {
    const action = actions.find((item) => item.name === name)
    if (action?.available === false) return
    setActionId(name)
    if (!action) return
    setTypeLoading(true)
    loadType(action.resource_type).then((res) => {
      setResources(res)
      // Switching between actions for the same resource type must keep the
      // resource that opened the drawer. Falling back to the first catalog
      // item can silently retarget a Kubernetes mutation to another
      // namespace/workload while the user is only choosing an action.
      const target = res.find((item) => item.id === resourceId && item.resource_type === action.resource_type)
        ?? res.find((item) => item.id === initialResource?.id && item.resource_type === action.resource_type)
        ?? res.find((item) => item.resource_type === action.resource_type)
      setConnectorId(target?.connector_id ?? ''); setNamespace(target?.namespace ?? ''); setResourceId(target?.id ?? '')
    }).catch(() => undefined).finally(() => setTypeLoading(false))
  }

  const chooseConnector = (id: string) => {
    const first = actionResources.find((item) => item.connector_id === id)
    setConnectorId(id); setNamespace(first?.namespace ?? ''); setResourceId(first?.id ?? '')
  }

  const chooseNamespace = (value: string) => {
    const first = actionResources.find((item) => item.connector_id === connectorId && resourceNamespace(item) === value)
    setNamespace(value); setResourceId(first?.id ?? '')
  }

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!resource || !actionId) {
      notify('当前动作没有可用的 ACTIVE 资源或执行通道')
      return
    }
    if (databaseExecuteNeedsPreview) {
      setDatabasePreviewRequest((request) => request + 1)
      return
    }
    if (networkInterfaceMutation && !networkPreviewCurrent) {
      notify('请先完成与当前参数一致的接口变更预检')
      return
    }
    setSubmitting(true)
    const submittedParameters = normalizedActionName === 'database.query.execute'
      ? { ...parameters, ast: normalizedDatabaseQueryAst(parameters.ast, resource) }
      : parameters
    createScopedExecution({
      resourceId: resource.id,
      actingProjectId,
      action: actionId,
      actionVersion: selectedAction?.version,
      parameters: submittedParameters,
      reason,
      taskId,
    }).then((exec) => {
      setExecution(exec)
    }).catch((e: ApiError) => notify(`提交失败：${e.message}`))
      .finally(() => setSubmitting(false))
  }

  const updateManifest = (value: string) => {
    setManifestText(value)
    setGenericPreview(null)
    setPreviewError('')
    try {
      const parsed = parseYaml(value)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('Manifest 顶层必须是对象')
      setManifestError('')
      setParameters((current) => { const next: Record<string, unknown> = { ...current, manifest: parsed }; delete next.previewHash; return next })
    } catch (error) {
      setManifestError(error instanceof Error ? error.message : 'YAML/JSON 格式无效')
      setParameters((current) => { const next = { ...current }; delete next.previewHash; return next })
    }
  }

  const chooseGenericKind = (value: string) => {
    const selected = GENERIC_KUBERNETES_KIND_OPTIONS.find((item) => `${item.apiVersion}/${item.kind}` === value)
    if (!selected) return
    const manifest = asRecord(parameters.manifest)
    const nextManifest = { ...manifest, apiVersion: selected.apiVersion, kind: selected.kind, metadata: { ...asRecord(manifest.metadata), namespace: parameters.namespace } }
    setParameters((current) => { const next: Record<string, unknown> = { ...current, apiVersion: selected.apiVersion, kind: selected.kind, manifest: nextManifest }; delete next.previewHash; return next })
    setManifestText(stringifyYaml(nextManifest)); setGenericPreview(null); setPreviewError('')
  }

  const runGenericPreview = async () => {
    if (!resource || manifestError || !parameters.manifest) return
    setPreviewLoading(true); setPreviewError(''); setGenericPreview(null)
    try {
      const previewParameters: Record<string, unknown> = {
        operation: actionName.endsWith('.create') ? 'create' : 'update', apiVersion: parameters.apiVersion, kind: parameters.kind,
        namespace: parameters.namespace, manifest: parameters.manifest,
      }
      if (actionName.endsWith('.update')) {
        previewParameters.name = resource.name
        previewParameters.expectedResourceVersion = parameters.expectedResourceVersion
      }
      const started = await createScopedExecution({ taskId: `ui-k8s-preview-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`, resourceId: resource.id, action: 'kubernetes.resource.preview', actionVersion: 1, parameters: previewParameters, reason: 'Kubernetes 变更 server-side dry-run 预检' })
      const completed = await waitForTerminalExecution(started)
      if (completed.status !== 'SUCCEEDED') throw new Error(completed.errorMessage || completed.errorCode || 'Kubernetes 预检失败')
      const previewHash = String(completed.result?.previewHash ?? '')
      if (!/^[a-f0-9]{64}$/.test(previewHash)) throw new Error('预检未返回有效 previewHash')
      const signature = genericPreviewableSignature(parameters)
      setGenericPreview({ before: completed.result?.before, after: completed.result?.after, previewHash, signature })
      setParameters((current) => ({ ...current, previewHash }))
    } catch (error) {
      setPreviewError(error instanceof Error ? error.message : 'Kubernetes 预检失败')
    } finally { setPreviewLoading(false) }
  }

  const runNetworkPreview = async () => {
    if (!resource || !networkPreviewAction) return
    setPreviewLoading(true); setPreviewError(''); setNetworkPreview(null)
    try {
      const previewParameters: Record<string, unknown> = { ...parameters }
      delete previewParameters.previewHash
      if (normalizedActionName === 'network.interface.trunk_vlan.add') previewParameters.operation = 'add'
      if (normalizedActionName === 'network.interface.trunk_vlan.remove') previewParameters.operation = 'remove'
      const started = await createScopedExecution({
        taskId: `ui-network-preview-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
        resourceId: resource.id,
        action: networkPreviewAction,
        actionVersion: 1,
        parameters: previewParameters,
        reason: 'H3C 接口变更预检',
      })
      const completed = await waitForTerminalExecution(started)
      if (completed.status !== 'SUCCEEDED') throw new Error(completed.errorMessage || completed.errorCode || 'H3C 接口变更预检失败')
      const previewHash = String(completed.result?.previewHash ?? '')
      if (!/^[a-f0-9]{64}$/.test(previewHash)) throw new Error('预检未返回有效 previewHash')
      const signature = networkInterfacePreviewSignature(normalizedActionName, resource.id, parameters)
      setNetworkPreview({
        before: completed.result?.before,
        after: completed.result?.after,
        commands: completed.result?.commands,
        save: completed.result?.save === true,
        noOp: completed.result?.noOp === true,
        previewHash,
        signature,
      })
      setParameters((current) => ({ ...current, previewHash }))
    } catch (error) {
      setPreviewError(error instanceof Error ? error.message : 'H3C 接口变更预检失败')
    } finally { setPreviewLoading(false) }
  }

  const resourceView = resource ? apiResourceToView(resource, connectorName(resource.connector_id)) : null

  if (execution) return <><div className="drawer-scrim" aria-hidden="true" /><aside className="drawer" aria-label="执行跟踪"><div className="drawer-header"><div><span className="eyebrow"><Activity size={15} />Execution Tracking</span><h2>{mapActionName(selectedAction?.name ?? actionId)}</h2><p>{resource?.namespace}/{resource?.name}</p></div><button className="icon-button" onClick={onClose} aria-label="关闭"><X size={20} /></button></div><ExecutionResult execution={execution} actionName={selectedAction?.name ?? actionId} onClose={onClose} /></aside></>

  return <><div className="drawer-scrim" aria-hidden="true" /><aside className="drawer" aria-label="请求受控动作">
    <div className="drawer-header"><div><span className="eyebrow"><Bot size={15} />Agent Action Request</span><h2>请求受控动作</h2><p>动作参数会经过 Schema 和策略双重校验。</p></div><button className="icon-button" onClick={onClose} aria-label="关闭"><X size={20} /></button></div>
    {loading ? <div style={{ padding: 30, textAlign: 'center' }}><Loader2 size={22} className="spin" /></div> : loadError ? <ErrorState message={loadError} onRetry={() => window.location.reload()} /> : actions.length === 0 ? <div style={{ padding: 30, color: '#85919a', fontSize: 11 }}>没有可用动作。请等待 Connector 同步或激活资源。</div> : (
      <form onSubmit={submit}>
        <div className="form-grid">
          <fieldset className="action-picker"><legend>选择动作</legend>{visibleActions.map((item) => <label key={item.id} className={`${actionId === item.name ? 'selected' : ''} ${item.available === false ? 'disabled' : ''}`} title={item.available === false ? `${item.availability_reason || '当前执行通道不支持'}${actionNeedsUpgrade(item) && item.required_capability ? `；需要 ${item.required_capability}` : ''}` : mapRiskHint(item.risk_level)}><input type="radio" name="action" value={item.name} checked={actionId === item.name} disabled={item.available === false} onChange={() => chooseAction(item.name)} /><span><strong>{mapActionName(item.name)}</strong><small>{item.available === false ? `${item.availability_reason || '不可用'}${item.connector_version ? `（当前 ${item.connector_version}）` : ''}` : item.description || mapRiskHint(item.risk_level)}</small>{item.available !== false && item.description && <small>{mapRiskHint(item.risk_level)}</small>}{actionNeedsUpgrade(item) && item.required_capability && <small>所需能力：{item.required_capability}</small>}</span><Badge tone={item.available === false ? 'neutral' : item.risk_level === 'high' || item.risk_level === 'critical' ? 'warning' : 'info'}>{item.available === false ? actionNeedsUpgrade(item) ? '需升级' : '不可用' : mapRiskLevel(item.risk_level)}</Badge></label>)}</fieldset>
          {noAvailableAction && <div className="form-error" role="alert">当前资源没有可执行动作。请根据动作提示恢复执行通道、启用 Integration 或升级 Connector。</div>}
          {noExecutionTarget && <div className="form-error" role="alert">没有可用执行通道。请检查资源是否为 ACTIVE、Connector 心跳是否正常，以及关联 Integration 是否已启用。</div>}
          {controlPlaneAction ? <><div className="onboarding-hint"><ShieldCheck size={17} /><span>该动作由 <strong>PAM 控制面</strong>执行，不经过 Connector，不依赖心跳。</span></div><label><span>{resourceTypeLabel(selectedAction?.resource_type)}</span><div className="select-wrap full"><select value={resourceId} disabled={typeLoading || targetResources.length === 0} onChange={(e) => setResourceId(e.target.value)}>{targetResources.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select><ChevronDown size={15} /></div></label></> : jenkinsContextTargetLocked ? <div className="onboarding-hint"><ShieldCheck size={17} /><span>目标已固定为 <strong>{resourceTypeLabel(resource?.resource_type)} · {resource?.name}</strong>，通过 {connectorName(resource?.connector_id ?? '')} 执行；不能切换到其他 Jenkins 资源。</span></div> : <>
            {actionConnectors.length === 1 ? <div className="onboarding-hint"><ShieldCheck size={17} /><span>将通过 <strong>{actionConnectors[0].name}</strong> 的执行代理执行 · 在线 · 推荐</span></div> : <label><span>执行代理</span><div className="select-wrap full"><select value={connectorId} disabled={noExecutionTarget} onChange={(e) => chooseConnector(e.target.value)}>{noExecutionTarget && <option value="">暂无可用执行代理</option>}{actionConnectors.map((item, index) => <option key={item.id} value={item.id}>{item.name} · {index === 0 ? '主通道 · 在线 · 推荐' : '备用通道 · 在线'}</option>)}</select><ChevronDown size={15} /></div></label>}
            <label><span>范围</span><div className="select-wrap full"><select value={namespace} onChange={(e) => chooseNamespace(e.target.value)}>{namespaces.map((item) => <option key={item || '__host'} value={item}>{item || '主机'}</option>)}</select><ChevronDown size={15} /></div></label>
            <label><span>{resourceTypeLabel(selectedAction?.resource_type)}</span><div className="select-wrap full"><select value={resourceId} disabled={typeLoading || targetResources.length === 0} onChange={(e) => setResourceId(e.target.value)}>{typeLoading ? <option>加载中…</option> : targetResources.length === 0 ? <option value="">当前范围暂无可用资源</option> : targetResources.map((item) => <option key={item.id} value={item.id}>{item.resource_type === 'huawei_cloud_account' ? cloudRegionLabel(String(item.metadata.region ?? item.name)) : item.name}</option>)}</select><ChevronDown size={15} /></div></label>
          </>}
          {resourceProjectIds.length === 0 ? <div className="form-error" role="alert">该资源暂未开放给任何项目，不能执行。</div> : resourceProjectIds.length === 1 ? <div className="acting-project-context"><FolderKanban size={17} /><span>当前执行项目</span><strong>{resource?.project_names?.[0] ?? projects.find((project) => project.id === actingProjectId)?.name ?? '已分配项目'}</strong></div> : <label><span>当前执行项目</span><div className="select-wrap full"><select value={actingProjectId} onChange={(event) => setActingProjectId(event.target.value)} required><option value="">请选择本次执行代表的项目</option>{resourceProjectIds.map((projectId) => <option key={projectId} value={projectId}>{projects.find((project) => project.id === projectId)?.name ?? projectId}</option>)}</select><ChevronDown size={15} /></div></label>}
          {GRAFANA_UPDATE_READ_ACTIONS[actionName] && <MutationPrefillState loading={prefillLoading} error={prefillError} readyText={`目标 UID 已固定为 ${String(resource?.metadata?.uid ?? resource?.external_id?.split('/').pop() ?? '当前资源')}；密码、Token 与 secureJsonData 不会进入表单。`} onRetry={() => setPrefillNonce((value) => value + 1)} />}
          {KUBERNETES_MUTATION_READ_ACTIONS[actionName] && <MutationPrefillState loading={prefillLoading} error={prefillError} readyText="已读取目标最新 generation；审批或提交后若资源 Spec 被其他客户端修改，Connector 将拒绝陈旧请求。" onRetry={() => setPrefillNonce((value) => value + 1)} />}
          {(actionName === 'kubernetes.resource.update' || actionName === 'kubernetes.resource.delete') && <MutationPrefillState loading={prefillLoading} error={prefillError} readyText={`已固定当前 resourceVersion ${String(parameters.expectedResourceVersion ?? '')}；对象发生并发变更时将拒绝执行。`} onRetry={() => setPrefillNonce((value) => value + 1)} />}
          {genericKubernetesPreviewable && <GenericKubernetesEditor actionName={actionName} parameters={parameters} manifestText={manifestText} manifestError={manifestError} preview={genericPreview} previewLoading={previewLoading} previewError={previewError} onManifestChange={updateManifest} onKindChange={chooseGenericKind} onPreview={runGenericPreview} />}
          {isCloudLogAction(actionName) && <LogTimePresets actionName={actionName} setParameters={setParameters} />}
          {normalizedActionName === 'database.table.rows.query' && actingProjectId && <DatabaseTableQueryBuilder resource={resource ?? null} parameters={parameters} setParameters={setParameters} startExecution={createScopedExecution} />}
          {normalizedActionName === 'database.query.execute' && actingProjectId && <DatabaseQueryAstBuilder resource={resource ?? null} parameters={parameters} setParameters={setParameters} previewRequest={databasePreviewRequest} onPreviewLoadingChange={setDatabasePreviewLoading} onPreviewVerdictChange={setDatabasePreviewVerdict} startExecution={createScopedExecution} actingProjectId={actingProjectId} />}
          {normalizedActionName === 'database.query_template.execute' && <DatabaseQueryTemplatePicker resource={resource ?? null} parameters={parameters} setParameters={setParameters} />}
          <ActionParameterFields entries={editableParameters} parameters={parameters} setParameters={setParameters} requiredParameters={requiredParameters} resourceContainers={resourceContainers} selectedContainer={selectedContainer} actionName={actionName} projects={projects} />
          {networkInterfaceMutation && <NetworkInterfacePreviewPanel preview={networkPreviewCurrent ? networkPreview : null} previewLoading={previewLoading} previewError={previewError} stale={Boolean(networkPreview && !networkPreviewCurrent)} onPreview={runNetworkPreview} />}
          {(needsApproval || (selectedAction?.risk_level === 'high' && !approvalExemptChange)) && <label><span>业务原因</span><textarea value={reason} onChange={(e) => setReason(e.target.value)} rows={2} required /></label>}
        </div>
        <div className={`decision-preview ${needsApproval ? 'needs-approval' : 'auto-allow'}`}>
          <div>{needsApproval ? <ShieldAlert size={20} /> : <ShieldCheck size={20} />}<div><strong>{needsApproval ? '预计需要租户审批' : '预计策略自动允许'}</strong><span>{databaseCostNeedsApproval ? '查询预检触发成本门禁 · 审批绑定 previewHash · 单次 Grant' : networkInterfaceMutation ? '固定接口动作 · 审批绑定 previewHash · 写后复核 · 禁止自动重试' : needsApproval ? '结构化变更 · 高风险 · 单次 Grant · 完整审计' : approvalExemptChange ? 'Jenkins Pipeline 创建 · 单次 Grant · 完整审计' : kubernetesChange ? '测试/开发环境结构化变更 · generation 并发保护 · 完整审计' : '只读动作 · 最小权限 Grant'}</span></div></div>
          <Badge tone={needsApproval ? 'warning' : 'success'}>{needsApproval ? '审批后执行' : '直接执行'}</Badge>
        </div>
        <div className="credential-note"><KeyRound size={17} /><span>{controlPlaneAction ? '凭据密文仅由 PAM 控制面处理，不会返回 Agent。' : '执行凭据仅在 Connector 内存中使用，不会返回 Agent。'}</span></div>
        <div className="drawer-actions">
          <button type="button" className="secondary-button" onClick={onClose}>取消</button>
          <button className="primary-button" type="submit" disabled={submitting || prefillLoading || previewLoading || databasePreviewLoading || Boolean(prefillError) || !actionId || !resource || !actingProjectId || noExecutionTarget || deleteConfirmationMismatch || ecsDeleteConfirmationMismatch || genericDeleteConfirmationMismatch || databaseExecuteMissingFields || (genericKubernetesPreviewable && !genericPreviewCurrent) || (networkInterfaceMutation && !networkPreviewCurrent)}>{submitting || prefillLoading || previewLoading || databasePreviewLoading ? <Loader2 size={16} className="spin" /> : databaseExecuteNeedsPreview ? <ShieldCheck size={16} /> : <Play size={16} fill="currentColor" />}{prefillLoading ? '读取当前配置' : previewLoading && networkInterfaceMutation ? '正在预检接口变更' : databasePreviewLoading ? '正在生成查询预览' : !actingProjectId ? '请选择执行项目' : databaseExecuteMissingFields ? '请先选择返回字段' : databaseExecuteNeedsPreview ? '生成查询预览' : (genericKubernetesPreviewable && !genericPreviewCurrent) || (networkInterfaceMutation && !networkPreviewCurrent) ? '请先完成预检' : '提交策略决策'}</button>
        </div>
        {resourceView && <div style={{ marginTop: 14, fontSize: 10, color: '#7a8992' }}>资源状态：{resourceView.status} · 服务目标：{resourceView.connector}</div>}
      </form>
    )}
  </aside></>
}

function MutationPrefillState({ loading, error, readyText, onRetry }: { loading: boolean; error: string; readyText: string; onRetry: () => void }) {
  return <div className={`grafana-prefill-state${error ? ' error' : ''}`} role={error ? 'alert' : 'status'}>
    {loading ? <Loader2 size={16} className="spin" /> : error ? <ShieldAlert size={16} /> : <ShieldCheck size={16} />}
    <span><strong>{loading ? '正在读取目标当前状态' : error ? '当前状态读取失败' : '已读取当前状态'}</strong><small>{error || readyText}</small></span>
    {error && <button type="button" className="quiet-button" onClick={onRetry}>重新读取</button>}
  </div>
}

function GenericKubernetesEditor({ actionName, parameters, manifestText, manifestError, preview, previewLoading, previewError, onManifestChange, onKindChange, onPreview }: {
  actionName: string
  parameters: Record<string, unknown>
  manifestText: string
  manifestError: string
  preview: { before?: unknown; after?: unknown; previewHash: string; signature: string } | null
  previewLoading: boolean
  previewError: string
  onManifestChange: (value: string) => void
  onKindChange: (value: string) => void
  onPreview: () => void
}) {
  const create = actionName === 'kubernetes.resource.create'
  return <fieldset className="kubernetes-manifest-editor">
    <legend>Kubernetes Manifest</legend>
    {create && <label><span>允许的资源类型</span><div className="select-wrap full"><select value={`${String(parameters.apiVersion ?? '')}/${String(parameters.kind ?? '')}`} onChange={(event) => onKindChange(event.target.value)}>{GENERIC_KUBERNETES_KIND_OPTIONS.map((item) => <option key={`${item.apiVersion}/${item.kind}`} value={`${item.apiVersion}/${item.kind}`}>{item.kind} · {item.apiVersion}</option>)}</select><ChevronDown size={15} /></div></label>}
    {!create && <div className="manifest-identity"><span>{String(parameters.apiVersion ?? '')}</span><strong>{String(parameters.kind ?? '')}/{String(parameters.namespace ?? '')}/{String(parameters.name ?? '')}</strong><small>身份字段由资源目录固定，不允许在编辑器中改为其他目标。</small></div>}
    <label><span>YAML / JSON</span><textarea className="manifest-textarea" rows={16} value={manifestText} onChange={(event) => onManifestChange(event.target.value)} spellCheck={false} aria-invalid={Boolean(manifestError)} /></label>
    {manifestError && <div className="form-error" role="alert">Manifest 格式错误：{manifestError}</div>}
    <div className="manifest-security-note">Secret、Token、密码、私钥和授权字段会被拒绝；status 与 managedFields 不会写回。</div>
    <button type="button" className="secondary-button preview-button" disabled={previewLoading || Boolean(manifestError) || !parameters.manifest} onClick={onPreview}>{previewLoading ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}{previewLoading ? '正在执行 server-side dry-run' : '预检变更并生成 Diff'}</button>
    {previewError && <div className="form-error" role="alert">{previewError}</div>}
    {preview && <div className="kubernetes-diff" role="status">
      <div className="diff-heading"><strong>Server-side dry-run 已通过</strong><code>{preview.previewHash.slice(0, 12)}</code></div>
      <div className="diff-columns">
        <section><span>Before</span><pre>{preview.before ? stringifyYaml(preview.before) : '# 新建资源，当前不存在'}</pre></section>
        <section><span>After</span><pre>{stringifyYaml(preview.after)}</pre></section>
      </div>
    </div>}
  </fieldset>
}

function NetworkInterfacePreviewPanel({ preview, previewLoading, previewError, stale, onPreview }: {
  preview: NetworkInterfacePreview | null
  previewLoading: boolean
  previewError: string
  stale: boolean
  onPreview: () => void
}) {
  return <fieldset className="kubernetes-manifest-editor">
    <legend>接口变更预检</legend>
    <div className="manifest-security-note">预检会重新读取目标接口，固定实际下发命令并绑定当前状态、目标参数与保存选项。参数或设备状态变化后必须重新预检。</div>
    <button type="button" className="secondary-button preview-button" disabled={previewLoading} onClick={onPreview}>{previewLoading ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}{previewLoading ? '正在读取接口状态' : preview ? '重新预检' : '预检接口变更'}</button>
    {stale && <div className="form-error" role="alert">参数已经变化，之前的预检已失效，请重新预检。</div>}
    {previewError && <div className="form-error" role="alert">{previewError}</div>}
    {preview && <div className="kubernetes-diff" role="status">
      <div className="diff-heading"><strong>{preview.noOp ? '目标状态已满足，无需下发命令' : '接口变更预检已通过'}</strong><code>{preview.previewHash.slice(0, 12)}</code></div>
      <div className="diff-columns">
        <section><span>Before</span><pre>{stringifyYaml(preview.before ?? {})}</pre></section>
        <section><span>After</span><pre>{stringifyYaml(preview.after ?? {})}</pre></section>
      </div>
      <section><span>完整设备命令计划</span><pre>{preview.noOp ? '# 无需下发命令' : Array.isArray(preview.commands) && preview.commands.length === 0 ? '# 无设备命令' : Array.isArray(preview.commands) ? preview.commands.map(String).join('\n') : stringifyYaml(preview.commands ?? [])}</pre></section>
	  <div className="manifest-security-note">{preview.noOp ? '当前配置已经满足目标状态，提交后只记录审计，不进入配置模式。' : preview.save ? '将执行 save force，保存设备当前全部运行配置，包括本次操作以外尚未保存的改动。' : '不执行 save force；本次变更仅写入运行配置，设备重启后可能丢失。'}</div>
    </div>}
  </fieldset>
}

function genericPreviewableSignature(parameters: Record<string, unknown>): string {
  return JSON.stringify({
    apiVersion: parameters.apiVersion, kind: parameters.kind, namespace: parameters.namespace, name: parameters.name,
    expectedResourceVersion: parameters.expectedResourceVersion, manifest: parameters.manifest,
  })
}

function networkInterfacePreviewSignature(actionName: string, resourceId: string, parameters: Record<string, unknown>): string {
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

async function loadGenericKubernetesParameters(resource: ApiResource, mutationAction: string, startExecution: ExecutionStarter): Promise<Record<string, unknown>> {
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
  const base = { apiVersion: gvk.apiVersion, kind: gvk.kind, name: resource.name, namespace: resource.namespace ?? '', expectedResourceVersion: resourceVersion }
  if (mutationAction === 'kubernetes.resource.delete') {
    return { ...base, propagationPolicy: 'Background', confirmResource: '' }
  }
  const manifest = asRecord(completed.result?.manifest)
  if (!Object.keys(manifest).length) throw new Error('Kubernetes 未返回可编辑 Manifest')
  return { ...base, manifest }
}

async function loadKubernetesMutationParameters(resource: ApiResource, readAction: string, mutationAction: string, startExecution: ExecutionStarter): Promise<Record<string, unknown>> {
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

async function loadGrafanaUpdateParameters(resource: ApiResource, readAction: string, updateAction: string, startExecution: ExecutionStarter): Promise<Record<string, unknown>> {
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

async function waitForTerminalExecution(initial: ApiExecution): Promise<ApiExecution> {
  let current = initial
  const deadline = Date.now() + 30_000
  while (!['SUCCEEDED', 'FAILED', 'CANCELLED', 'EXPIRED', 'UNKNOWN'].includes(current.status)) {
    if (Date.now() >= deadline) throw new Error('读取当前 Grafana 配置超时，请重试')
    await new Promise((resolve) => window.setTimeout(resolve, 600))
    current = await getExecution(current.id)
  }
  return current
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
    return { dashboard, ...(folderUid ? { folderUid } : {}), message: '通过 PAM 更新 Grafana Dashboard', overwrite: true }
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

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function stripGrafanaSensitiveFields(value: Record<string, unknown>): Record<string, unknown> {
  const safe: Record<string, unknown> = {}
  for (const [key, item] of Object.entries(value)) {
    const normalized = key.toLowerCase().replaceAll('_', '').replaceAll('-', '')
    if (['securejsondata', 'securejsonfields', 'password', 'basicauthpassword', 'token', 'apikey', 'authorization'].includes(normalized)) continue
    safe[key] = stripGrafanaSensitiveValue(item)
  }
  return safe
}

function stripGrafanaSensitiveValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stripGrafanaSensitiveValue)
  if (value && typeof value === 'object') return stripGrafanaSensitiveFields(asRecord(value))
  return value
}

function LogTimePresets({ actionName, setParameters }: { actionName: string; setParameters: Dispatch<SetStateAction<Record<string, unknown>>> }) {
  const applyRange = (minutes: number) => {
    const now = Date.now()
    setParameters((current) => actionName === 'huawei.lts.logs.query'
      ? { ...current, startTime: now - minutes * 60 * 1000, endTime: now }
      : { ...current, from: Math.floor(now / 1000) - minutes * 60, to: Math.floor(now / 1000) })
  }
  return <fieldset className="log-time-presets"><legend>时间范围</legend>{[
    [15, '最近 15 分钟'], [60, '最近 1 小时'], [360, '最近 6 小时'], [1440, '最近 24 小时'],
  ].map(([minutes, label]) => <button key={minutes} type="button" onClick={() => applyRange(Number(minutes))}>{label}</button>)}<small>也可以在下方选择任意历史起止时间，实际可查范围由云日志保留策略决定。</small></fieldset>
}

function ActionParameterFields({ entries, parameters, setParameters, requiredParameters, resourceContainers, selectedContainer, actionName, projects }: {
  entries: [string, ParameterSchema][]
  parameters: Record<string, unknown>
  setParameters: Dispatch<SetStateAction<Record<string, unknown>>>
  requiredParameters: string[]
  resourceContainers: { name: string; image: string }[]
  selectedContainer?: { name: string; image: string }
  actionName: string
  projects: ApiProject[]
}) {
  return <>{entries.map(([key, schema]) => {
    if ((actionName === 'jenkins.job.create' || actionName === 'jenkins.job.update') && key === 'pipelineScript' && parameters.mode !== 'inline') return null
    if ((actionName === 'jenkins.job.create' || actionName === 'jenkins.job.update') && key === 'scm' && parameters.mode !== 'scm') return null
    if (key === 'container' && resourceContainers.length > 0) {
      return <fieldset className="action-picker container-picker" key={key}><legend>选择容器</legend>{resourceContainers.map((container) => <label key={container.name} className={parameters.container === container.name ? 'selected' : ''}><input type="radio" name="container" checked={parameters.container === container.name} onChange={() => setParameters((current) => ({ ...current, container: container.name }))} /><span><strong>{container.name}</strong><small>{container.image}</small></span></label>)}</fieldset>
    }
    if (key === 'projectId' && actionName === 'huawei.ecs.create') {
      return <label key={key}><span>{parameterLabel(key, actionName)}</span><div className="select-wrap full"><select value={String(parameters[key] ?? '')} required onChange={(event) => setParameters((current) => ({ ...current, [key]: event.target.value }))}><option value="">选择 PAM 项目</option>{projects.filter((project) => !project.is_default).map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}</select><ChevronDown size={15} /></div></label>
    }
    if (isCloudLogTimeParameter(actionName, key)) {
      const unit = actionName === 'huawei.lts.logs.query' ? 1 : 1000
      return <label key={key}><span>{parameterLabel(key, actionName)}</span><input type="datetime-local" step="1" value={epochToLocalInput(Number(parameters[key] ?? 0) * unit)} required={requiredParameters.includes(key)} onChange={(event) => { const epoch = new Date(event.target.value).getTime(); if (Number.isFinite(epoch)) setParameters((current) => ({ ...current, [key]: Math.floor(epoch / unit) })) }} /></label>
    }
    if (schema.enum?.length) {
      return <label key={key}><span>{parameterLabel(key, actionName)}</span><div className="select-wrap full"><select value={String(parameters[key] ?? '')} required={requiredParameters.includes(key)} onChange={(event) => setParameters((current) => {
        const next = { ...current, [key]: event.target.value }
        if (key === 'mode' && (actionName === 'jenkins.job.create' || actionName === 'jenkins.job.update')) {
          delete next.pipelineScript
          delete next.scm
        }
        return next
      })}><option value="">请选择</option>{schema.enum.map((value) => <option key={value} value={value}>{value}</option>)}</select><ChevronDown size={15} /></div></label>
    }
    if (schema.type === 'boolean') {
      return <label className="check-field" key={key}><input type="checkbox" checked={Boolean(parameters[key])} onChange={(event) => setParameters((current) => ({ ...current, [key]: event.target.checked }))} /><span>{parameterLabel(key, actionName)}</span></label>
    }
    if (schema.type === 'object' || schema.type === 'array') {
      if (key === 'scm' && (actionName === 'jenkins.job.create' || actionName === 'jenkins.job.update')) {
        const scm = asRecord(parameters.scm)
        const setScmField = (field: string, value: string) => setParameters((current) => ({ ...current, scm: { ...asRecord(current.scm), [field]: value } }))
        return <fieldset className="jenkins-scm-fields" key={key}><legend>SCM Pipeline</legend>
          <label><span>仓库地址</span><input type="text" value={String(scm.repoUrl ?? '')} required onChange={(event) => setScmField('repoUrl', event.target.value)} placeholder="ssh://git@example.com/team/app.git" /></label>
          <label><span>Credential ID</span><input type="text" value={String(scm.credentialsId ?? '')} onChange={(event) => setScmField('credentialsId', event.target.value)} placeholder="deploy-key" /></label>
          <label><span>分支</span><input type="text" value={String(scm.branch ?? '')} onChange={(event) => setScmField('branch', event.target.value)} placeholder="*/main" /></label>
          <label><span>Jenkinsfile 路径</span><input type="text" value={String(scm.scriptPath ?? '')} onChange={(event) => setScmField('scriptPath', event.target.value)} placeholder="Jenkinsfile" /></label>
          <div className="manifest-security-note">仅填写 Jenkins Credential ID；不得输入 API Token、密码、私钥或 Authorization 值。</div>
        </fieldset>
      }
      return <label key={key}><span>{parameterLabel(key, actionName)}</span><textarea rows={4} value={typeof parameters[key] === 'object' ? JSON.stringify(parameters[key], null, 2) : String(parameters[key] ?? '')} onChange={(event) => setParameters((current) => { const text = event.target.value.trim(); if (!text) { const next = { ...current }; delete next[key]; return next } try { return { ...current, [key]: JSON.parse(text) } } catch { return { ...current, [key]: event.target.value } } })} required={requiredParameters.includes(key) || (key === 'scm' && parameters.mode === 'scm')} placeholder={schema.type === 'array' ? '["id-1"]' : parameterPlaceholder(key)} /></label>
    }
    if (key === 'pipelineScript') {
      return <label key={key}><span>{parameterLabel(key, actionName)}</span><textarea rows={8} value={String(parameters[key] ?? '')} onChange={(event) => setParameters((current) => ({ ...current, [key]: event.target.value }))} required={requiredParameters.includes(key) || parameters.mode === 'inline'} placeholder={parameterPlaceholder(key)} /></label>
    }
    const readOnlyRegion = key === 'region' && actionName === 'huawei.ecs.create'
    return <label key={key}><span>{parameterLabel(key, actionName)}</span>{key === 'image' && selectedContainer && <small className="current-image">当前镜像：<code>{selectedContainer.image}</code></small>}<input type={schema.type === 'integer' || schema.type === 'number' ? 'number' : 'text'} min={schema.minimum} max={schema.maximum} readOnly={readOnlyRegion} value={readOnlyRegion ? cloudRegionLabel(String(parameters[key] ?? '')) : String(parameters[key] ?? '')} onChange={(event) => setParameters((current) => ({ ...current, [key]: schema.type === 'integer' || schema.type === 'number' ? Number(event.target.value) : event.target.value }))} required={requiredParameters.includes(key)} placeholder={parameterPlaceholder(key)} /></label>
  })}</>
}

const DATABASE_FILTER_OPERATORS = ['eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'like', 'in', 'is_null', 'is_not_null']

type DatabaseFilterInputKind = 'text' | 'number' | 'boolean' | 'date' | 'time' | 'datetime-local'

function databaseFilterInputKind(column: Record<string, unknown> | undefined): DatabaseFilterInputKind {
  const dataType = String(column?.dataType ?? '').trim().toLowerCase()
  if (/^(bool|boolean)$/.test(dataType)) return 'boolean'
  if (/^(tinyint|smallint|mediumint|int|integer|bigint|decimal|numeric|float|double|real|bit|year)(\b|\()/.test(dataType)) return 'number'
  if (/^(datetime|timestamp)(\b|\()/.test(dataType)) return 'datetime-local'
  if (/^date(\b|\()/.test(dataType)) return 'date'
  if (/^time(\b|\()/.test(dataType)) return 'time'
  return 'text'
}

function DatabaseFilterValueInput({ column, operator, value, onChange }: {
  column: Record<string, unknown> | undefined
  operator: string
  value: unknown
  onChange: (value: unknown) => void
}) {
  if (operator === 'is_null' || operator === 'is_not_null') return null
  if (operator === 'in') {
    const rawValue = Array.isArray(value) ? value.map(String).join(', ') : String(value ?? '')
    return <input type="text" value={rawValue} onChange={(event) => onChange(event.target.value.split(',').map((item) => item.trim()).filter(Boolean))} placeholder="多个值用逗号分隔" aria-label="筛选值" />
  }
  const inputKind = databaseFilterInputKind(column)
  if (inputKind === 'boolean') {
    return <select value={String(value ?? '')} onChange={(event) => onChange(event.target.value === '' ? '' : event.target.value === 'true')} aria-label="筛选值"><option value="">请选择</option><option value="true">true</option><option value="false">false</option></select>
  }
  return <input type={inputKind} step={inputKind === 'number' ? 'any' : undefined} value={String(value ?? '')} onChange={(event) => onChange(event.target.value)} placeholder="值" aria-label="筛选值" />
}

function DatabaseTableQueryBuilder({ resource, parameters, setParameters, startExecution }: {
  resource: ApiResource | null
  parameters: Record<string, unknown>
  setParameters: Dispatch<SetStateAction<Record<string, unknown>>>
  startExecution: ExecutionStarter
}) {
  const [metadata, setMetadata] = useState<Record<string, unknown> | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [loadedFor, setLoadedFor] = useState('')

  const loadMetadata = useCallback(() => {
    if (!resource) return
    setLoading(true)
    setError('')
    startExecution({
      taskId: `ui-db-describe-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
      resourceId: resource.id,
      action: 'database.table.describe',
      actionVersion: 1,
      parameters: {
        name: resource.name,
        ...(resource.namespace ? { namespace: resource.namespace } : {}),
      },
      reason: '查询表单读取表结构、索引与行数',
    }).then(waitForTerminalExecution).then((completed) => {
      if (completed.status !== 'SUCCEEDED') throw new Error(completed.errorMessage || completed.errorCode || '读取表结构失败')
      setMetadata(completed.result ?? {})
      setLoadedFor(resource.id)
    }).catch((err: Error) => setError(err.message || '读取表结构失败'))
      .finally(() => setLoading(false))
  }, [resource, startExecution])

  useEffect(() => {
    if (!resource || resource.id === loadedFor || loading) return
    setMetadata(null)
    loadMetadata()
  }, [resource, loadedFor, loading, loadMetadata])

  const columns = (Array.isArray(metadata?.columns) ? metadata.columns : []).filter((column): column is Record<string, unknown> => Boolean(column && typeof column === 'object'))
  const indexes = (Array.isArray(metadata?.indexes) ? metadata.indexes : []).filter((index): index is Record<string, unknown> => Boolean(index && typeof index === 'object'))
  const constraints = (Array.isArray(metadata?.constraints) ? metadata.constraints : []).filter((constraint): constraint is Record<string, unknown> => Boolean(constraint && typeof constraint === 'object'))
  const selectedColumns = Array.isArray(parameters.columns) ? parameters.columns.map(String) : []
  const filters = (Array.isArray(parameters.filters) ? parameters.filters : []).filter((filter): filter is Record<string, unknown> => Boolean(filter && typeof filter === 'object'))

  const indexLabels = (columnName: string) => {
    const labels: string[] = []
    const inColumns = (value: unknown) => Array.isArray(value) && value.map(String).includes(columnName)
    if (constraints.some((constraint) => constraint.type === 'PRIMARY KEY' && inColumns(constraint.columns))) labels.push('PK')
    else if (constraints.some((constraint) => constraint.type === 'UNIQUE' && inColumns(constraint.columns)) || indexes.some((index) => index.unique === true && inColumns(index.columns))) labels.push('UQ')
    for (const index of indexes) {
      if (index.unique !== true && inColumns(index.columns)) labels.push(String(index.name ?? ''))
    }
    return labels.filter(Boolean)
  }

  const toggleColumn = (name: string) => setParameters((current) => {
    const currentColumns = Array.isArray(current.columns) ? current.columns.map(String) : []
    const next = currentColumns.includes(name) ? currentColumns.filter((column) => column !== name) : [...currentColumns, name]
    return { ...current, columns: next }
  })

  const addFilter = () => setParameters((current) => ({
    ...current,
    filters: [...(Array.isArray(current.filters) ? current.filters : []), { column: String(columns[0]?.name ?? ''), operator: 'eq', value: '' }],
  }))

  const updateFilter = (index: number, patch: Record<string, unknown>) => setParameters((current) => {
    const currentFilters = (Array.isArray(current.filters) ? current.filters : []).map((filter) => ({ ...asRecord(filter) }))
    currentFilters[index] = { ...currentFilters[index], ...patch }
    return { ...current, filters: currentFilters }
  })

  const removeFilter = (index: number) => setParameters((current) => ({
    ...current,
    filters: (Array.isArray(current.filters) ? current.filters : []).filter((_, filterIndex) => filterIndex !== index),
  }))

  const orders = (Array.isArray(parameters.orderBy) ? parameters.orderBy : []).filter((order): order is Record<string, unknown> => Boolean(order && typeof order === 'object'))
  const addOrder = () => setParameters((current) => ({
    ...current,
    orderBy: [...(Array.isArray(current.orderBy) ? current.orderBy : []), { column: String(columns[0]?.name ?? ''), direction: 'asc' }],
  }))
  const updateOrder = (index: number, patch: Record<string, unknown>) => setParameters((current) => {
    const currentOrders = (Array.isArray(current.orderBy) ? current.orderBy : []).map((order) => ({ ...asRecord(order) }))
    currentOrders[index] = { ...currentOrders[index], ...patch }
    return { ...current, orderBy: currentOrders }
  })
  const removeOrder = (index: number) => setParameters((current) => ({
    ...current,
    orderBy: (Array.isArray(current.orderBy) ? current.orderBy : []).filter((_, orderIndex) => orderIndex !== index),
  }))

  const rowStat = Number(metadata?.estimatedRows ?? 0)
  return <fieldset className="database-query-builder">
    <legend>返回字段与筛选（单表快捷查询）</legend>
    {!resource ? <div className="form-error" role="alert">请先选择数据库表资源。</div>
      : loading ? <div className="onboarding-hint"><Loader2 size={16} className="spin" /><span>正在读取表结构、索引与行数…</span></div>
        : error ? <div className="form-error" role="alert">{error}<button type="button" className="quiet-button" onClick={loadMetadata}>重试</button></div>
          : metadata ? <>
            <div className="database-builder-meta">{columns.length} 列 · {rowStat > 0 ? `约 ${rowStat.toLocaleString()} 行` : '行数未知'}{metadata.statsUpdatedAt ? ` · 统计时间 ${String(metadata.statsUpdatedAt)}` : ''}</div>
            <div className="database-builder-selection-head"><div><strong>返回字段</strong><small>已选择 {selectedColumns.length} / {columns.length} 个字段，可多选</small></div></div>
            <div className="database-builder-columns">{columns.map((column) => {
              const name = String(column.name)
              return <label key={name} className={selectedColumns.includes(name) ? 'selected' : ''}><input type="checkbox" checked={selectedColumns.includes(name)} onChange={() => toggleColumn(name)} /><span className="database-column-details"><strong>{name}</strong><small>{String(column.dataType)}{column.nullable === true ? ' · 可空' : ''}</small></span>{indexLabels(name).map((label) => <code className="index-badge" key={label}>{label}</code>)}</label>
            })}</div>
            <div className="database-builder-filters">
              <div className="database-builder-filter-head"><strong>筛选条件</strong><button type="button" className="quiet-button" onClick={addFilter}>添加筛选</button></div>
              {filters.length === 0 ? <div className="onboarding-hint">未添加筛选，将按 limit 返回表数据。</div> : filters.map((filter, index) => {
                const item = asRecord(filter)
                const operator = String(item.operator ?? 'eq')
                const selectedColumn = columns.find((column) => String(column.name) === String(item.column ?? ''))
                return <div className="database-builder-filter-row" key={index}>
                  <select value={String(item.column ?? '')} onChange={(event) => updateFilter(index, { column: event.target.value })}>{columns.map((column) => <option key={String(column.name)} value={String(column.name)}>{String(column.name)}</option>)}</select>
                  <select value={operator} onChange={(event) => updateFilter(index, { operator: event.target.value })}>{DATABASE_FILTER_OPERATORS.map((value) => <option key={value} value={value}>{value}</option>)}</select>
                  <DatabaseFilterValueInput column={selectedColumn} operator={operator} value={item.value} onChange={(value) => updateFilter(index, { value })} />
                  <button type="button" className="icon-button" onClick={() => removeFilter(index)} aria-label="删除筛选"><X size={14} /></button>
                </div>
              })}
            </div>
            <div className="database-builder-filters">
              <div className="database-builder-filter-head"><strong>排序</strong><button type="button" className="quiet-button" onClick={addOrder}>添加排序</button></div>
              {orders.length === 0 ? <div className="onboarding-hint">未添加排序，结果顺序由数据库决定。</div> : orders.map((order, index) => {
                const item = asRecord(order)
                return <div className="database-builder-filter-row" key={index}>
                  <select value={String(item.column ?? '')} onChange={(event) => updateOrder(index, { column: event.target.value })}>{columns.map((column) => <option key={String(column.name)} value={String(column.name)}>{String(column.name)}</option>)}</select>
                  <select value={String(item.direction ?? 'asc')} onChange={(event) => updateOrder(index, { direction: event.target.value })}><option value="asc">asc</option><option value="desc">desc</option></select>
                  <button type="button" className="icon-button" onClick={() => removeOrder(index)} aria-label="删除排序"><X size={14} /></button>
                </div>
              })}
            </div>
          </> : null}
  </fieldset>
}

function DatabaseQueryTemplatePicker({ resource, parameters, setParameters }: {
  resource: ApiResource | null
  parameters: Record<string, unknown>
  setParameters: Dispatch<SetStateAction<Record<string, unknown>>>
}) {
  const [templates, setTemplates] = useState<ApiQueryTemplate[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!resource?.integration_id) {
      setTemplates([])
      return
    }
    let cancelled = false
    setLoading(true)
    setError('')
    listQueryTemplates(resource.integration_id, 'PUBLISHED')
      .then((items) => {
        if (cancelled) return
        setTemplates((items ?? []).filter((item) => !item.resource_bindings?.length || item.resource_bindings.includes(resource.external_id)))
      })
      .catch((err: Error) => { if (!cancelled) setError(err.message || '读取已发布查询模板失败') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [resource?.id, resource?.integration_id, resource?.external_id])

  const selected = templates.find((item) => item.id === String(parameters.templateId ?? '') && item.version === Number(parameters.templateVersion ?? 0))
  const bindingProperties = asRecord(selected?.parameter_schema?.properties)
  const bindings = asRecord(parameters.bindings)
  const chooseTemplate = (value: string) => {
    const [id, versionText] = value.split('@')
    const template = templates.find((item) => item.id === id && item.version === Number(versionText))
    setParameters((current) => template
      ? { ...current, templateId: template.id, templateVersion: template.version, bindings: {} }
      : (() => { const next = { ...current }; delete next.templateId; delete next.templateVersion; delete next.bindings; return next })())
  }
  const updateBinding = (name: string, value: string) => setParameters((current) => ({
    ...current,
    bindings: { ...asRecord(current.bindings), [name]: parseTemplateBinding(value) },
  }))

  return <fieldset className="database-query-builder">
    <legend>已发布查询模板</legend>
    {!resource?.integration_id ? <div className="form-error" role="alert">当前表未关联数据库集成，无法选择模板。</div>
      : loading ? <div className="onboarding-hint"><Loader2 size={16} className="spin" /><span>正在读取已发布模板…</span></div>
        : error ? <div className="form-error" role="alert">{error}</div>
          : <>
            <label><span>查询模板</span><div className="select-wrap full"><select value={selected ? `${selected.id}@${selected.version}` : ''} onChange={(event) => chooseTemplate(event.target.value)} required><option value="">选择已发布模板</option>{templates.map((template) => <option key={`${template.id}@${template.version}`} value={`${template.id}@${template.version}`}>{template.name} · v{template.version}</option>)}</select><ChevronDown size={15} /></div></label>
            {templates.length === 0 && <div className="onboarding-hint">当前数据库没有适用于该表的已发布模板。模板需由管理员通过 SQL 导入生成草稿，并完成发布审批。</div>}
            {selected && <>
              <div className="database-builder-meta">模板 ID <code>{selected.id}</code> · 名称 {selected.name} · v{selected.version} · 最多 {selected.max_rows} 行 · 超时 {selected.timeout_seconds}s{selected.owner ? ` · Owner ${selected.owner}` : ''}</div>
              {Object.keys(bindingProperties).length > 0 ? <div className="database-builder-filters">
                <div className="database-builder-filter-head"><strong>运行参数</strong><span>仅参数值可变，表、JOIN 与返回字段由模板固定</span></div>
                {Object.keys(bindingProperties).map((name) => <label key={name}><span>{name}</span><input value={formatTemplateBinding(bindings[name])} onChange={(event) => updateBinding(name, event.target.value)} required /></label>)}
              </div> : <div className="onboarding-hint">该模板没有运行参数，可直接提交。</div>}
            </>}
          </>}
  </fieldset>
}

function parseTemplateBinding(value: string): unknown {
  const trimmed = value.trim()
  if (!trimmed) return ''
  try { return JSON.parse(trimmed) } catch { return value }
}

function formatTemplateBinding(value: unknown): string {
  if (value === undefined || value === null) return ''
  return typeof value === 'string' ? value : JSON.stringify(value)
}

type DatabaseQueryBuilderField = {
  key: string
  source: string
  column: string
  table: ApiResource
  metadata: Record<string, unknown>
  definition: Record<string, unknown>
}

const queryFieldKey = (source: string, column: string) => `${source}.${column}`

function queryFieldRef(key: string) {
  const separator = key.indexOf('.')
  return separator > 0 ? { source: key.slice(0, separator), column: key.slice(separator + 1) } : { source: DATABASE_QUERY_SOURCE_ALIAS, column: key }
}

function queryOutputAlias(source: string, column: string) {
  const raw = source === DATABASE_QUERY_SOURCE_ALIAS ? column : `${source}_${column}`
  return raw.slice(0, 64)
}

function DatabaseQueryAstBuilder({ resource, parameters, setParameters, previewRequest, onPreviewLoadingChange, onPreviewVerdictChange, startExecution, actingProjectId }: {
  resource: ApiResource | null
  parameters: Record<string, unknown>
  setParameters: Dispatch<SetStateAction<Record<string, unknown>>>
  previewRequest: number
  onPreviewLoadingChange: (loading: boolean) => void
  onPreviewVerdictChange: (verdict: string) => void
  startExecution: ExecutionStarter
  actingProjectId: string
}) {
  const [metadataByExternalId, setMetadataByExternalId] = useState<Record<string, Record<string, unknown>>>({})
  const [tableResources, setTableResources] = useState<ApiResource[]>([])
  const [loading, setLoading] = useState(false)
  const [tableLoading, setTableLoading] = useState(false)
  const [error, setError] = useState('')
  const [joinError, setJoinError] = useState('')
  const [joinTableId, setJoinTableId] = useState('')
  const [preview, setPreview] = useState<Record<string, unknown> | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  const metadataRequests = useRef(new Set<string>())
  const handledPreviewRequest = useRef(previewRequest)

  const invalidatePreview = () => {
    setPreview(null)
    setPreviewError('')
    onPreviewVerdictChange('')
  }

  const describeTable = useCallback(async (table: ApiResource) => {
    const started = await startExecution({
      taskId: `ui-db-describe-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
      resourceId: table.id,
      action: 'database.table.describe',
      actionVersion: 1,
      parameters: { name: table.name, ...(table.namespace ? { namespace: table.namespace } : {}) },
      reason: '多表查询构造器读取表结构、索引与行数',
    })
    const completed = await waitForTerminalExecution(started)
    if (completed.status !== 'SUCCEEDED') throw new Error(completed.errorMessage || completed.errorCode || `读取表 ${table.name} 结构失败`)
    const metadata = completed.result ?? {}
    setMetadataByExternalId((current) => ({ ...current, [table.external_id]: metadata }))
    return metadata
  }, [startExecution])

  const ensureTableMetadata = useCallback(async (table: ApiResource) => {
    if (metadataByExternalId[table.external_id]) return metadataByExternalId[table.external_id]
    if (metadataRequests.current.has(table.external_id)) {
      for (let attempt = 0; attempt < 50; attempt += 1) {
        await new Promise((resolve) => window.setTimeout(resolve, 100))
        const existing = metadataByExternalId[table.external_id]
        if (existing) return existing
      }
    }
    metadataRequests.current.add(table.external_id)
    try { return await describeTable(table) } finally { metadataRequests.current.delete(table.external_id) }
  }, [describeTable, metadataByExternalId])

  const loadBaseMetadata = useCallback(() => {
    if (!resource) return
    setLoading(true)
    setError('')
    describeTable(resource).catch((err: Error) => setError(err.message || '读取表结构失败')).finally(() => setLoading(false))
  }, [describeTable, resource])

  useEffect(() => {
    if (!resource || metadataByExternalId[resource.external_id] || loading) return
    loadBaseMetadata()
  }, [resource, metadataByExternalId, loading, loadBaseMetadata])

  useEffect(() => {
    if (!resource?.integration_id) {
      setTableResources(resource ? [resource] : [])
      return
    }
    let cancelled = false
    setTableLoading(true)
    setJoinError('')
    listResources({ type: 'database_table', integrationId: resource.integration_id, projectId: actingProjectId })
      .then((items) => {
        if (cancelled) return
        // Keep the client-side guard even though the catalog endpoint is
        // filtered. It prevents a stale proxy/cache response from surfacing a
        // schema, instance, or table from another Integration as a JOIN target.
        const available = (items ?? []).filter((item) => item.resource_type === 'database_table'
          && item.status === 'ACTIVE'
          && item.connector_id === resource.connector_id
          && item.integration_id === resource.integration_id)
        setTableResources(available.some((item) => item.id === resource.id) ? available : [resource, ...available])
      })
      .catch((err: Error) => { if (!cancelled) setJoinError(err.message || '读取可关联表失败') })
      .finally(() => { if (!cancelled) setTableLoading(false) })
    return () => { cancelled = true }
  }, [resource?.id, resource?.integration_id, resource?.connector_id, actingProjectId])

  const ast = asRecord(parameters.ast)
  useEffect(() => {
    if (!resource) return
    const normalized = normalizedDatabaseQueryAst(ast, resource)
    const sources = Array.isArray(ast.sources) ? ast.sources : []
    const pinned = sources.length === 1 && asRecord(sources[0]).resourceId === resource.external_id
    if (ast.astVersion === DATABASE_QUERY_AST_VERSION && pinned && Array.isArray(ast.select) && ast.bindings && typeof ast.bindings === 'object') return
    setParameters((current) => ({ ...current, ast: normalized }))
  }, [resource?.id, resource?.external_id]) // eslint-disable-line react-hooks/exhaustive-deps

  const joins = (Array.isArray(ast.joins) ? ast.joins : []).filter((join): join is Record<string, unknown> => Boolean(join && typeof join === 'object'))
  const sourceEntries: Array<{ alias: string; table: ApiResource; metadata?: Record<string, unknown> }> = []
  if (resource) {
    sourceEntries.push({ alias: DATABASE_QUERY_SOURCE_ALIAS, table: resource, metadata: metadataByExternalId[resource.external_id] })
    for (const join of joins) {
      const source = asRecord(join.source)
      const table = tableResources.find((item) => item.external_id === String(source.resourceId ?? ''))
      if (table) sourceEntries.push({ alias: String(source.alias ?? ''), table, metadata: metadataByExternalId[table.external_id] })
    }
  }

  useEffect(() => {
    for (const entry of sourceEntries.slice(1)) {
      if (!entry.metadata && !metadataRequests.current.has(entry.table.external_id)) void ensureTableMetadata(entry.table).catch((err: Error) => setJoinError(err.message))
    }
  }, [sourceEntries.map((entry) => `${entry.alias}:${entry.table.id}:${Boolean(entry.metadata)}`).join('|'), ensureTableMetadata]) // eslint-disable-line react-hooks/exhaustive-deps

  const fieldOptions: DatabaseQueryBuilderField[] = sourceEntries.flatMap((entry) => {
    const columns = (Array.isArray(entry.metadata?.columns) ? entry.metadata.columns : []).filter((column): column is Record<string, unknown> => Boolean(column && typeof column === 'object'))
    return columns.map((column) => ({
      key: queryFieldKey(entry.alias, String(column.name)), source: entry.alias, column: String(column.name),
      table: entry.table, metadata: entry.metadata ?? {}, definition: column,
    }))
  })
  const fieldByKey = new Map(fieldOptions.map((field) => [field.key, field]))
  const selects = (Array.isArray(ast.select) ? ast.select : []).filter((select): select is Record<string, unknown> => Boolean(select && typeof select === 'object'))
  const selectedKeys = selects.map((select) => {
    const field = asRecord(select.field)
    return queryFieldKey(String(field.source ?? DATABASE_QUERY_SOURCE_ALIAS), String(field.column ?? ''))
  }).filter((key) => fieldByKey.has(key))
  const selectedAliases = selects.map((select) => String(select.alias ?? '')).filter(Boolean)
  const where = asRecord(ast.where)
  const bindings = asRecord(ast.bindings)
  const filters = (Array.isArray(where.and) ? where.and : []).filter((filter): filter is Record<string, unknown> => Boolean(filter && typeof filter === 'object')).map((filter) => {
    const item = asRecord(filter)
    const field = asRecord(item.field)
    const parameter = String(item.parameter ?? '')
    return {
      source: String(field.source ?? DATABASE_QUERY_SOURCE_ALIAS), column: String(field.column ?? ''),
      operator: String(item.operator ?? 'eq'), value: parameter ? bindings[parameter] : item.value,
    }
  })
  const orders = (Array.isArray(ast.orderBy) ? ast.orderBy : []).filter((order): order is Record<string, unknown> => Boolean(order && typeof order === 'object'))
  const limit = typeof ast.limit === 'number' ? Number(ast.limit) : 50
  const baseMetadata = resource ? metadataByExternalId[resource.external_id] : undefined

  const updateAst = (patch: Record<string, unknown>) => {
    invalidatePreview()
    setParameters((current) => {
      if (!resource) return current
      const next: Record<string, unknown> = { ...current, ast: { ...normalizedDatabaseQueryAst(current.ast, resource), ...patch } }
      delete next.previewHash
      return next
    })
  }

  const encodeFilters = (items: Record<string, unknown>[]) => {
    const leaves: Record<string, unknown>[] = []
    const nextBindings: Record<string, unknown> = {}
    items.forEach((item, index) => {
      const operator = String(item.operator ?? 'eq')
      const leaf: Record<string, unknown> = { field: { source: String(item.source ?? DATABASE_QUERY_SOURCE_ALIAS), column: String(item.column ?? '') }, operator }
      if (operator !== 'is_null' && operator !== 'is_not_null') {
        const parameter = `p${index + 1}`
        leaf.parameter = parameter
        nextBindings[parameter] = item.value
      }
      leaves.push(leaf)
    })
    return { where: leaves.length > 0 ? { and: leaves } : undefined, bindings: nextBindings }
  }
  const writeFilters = (items: Record<string, unknown>[]) => updateAst(encodeFilters(items))

  const indexLabels = (metadata: Record<string, unknown>, columnName: string) => {
    const indexes = (Array.isArray(metadata.indexes) ? metadata.indexes : []).filter((item): item is Record<string, unknown> => Boolean(item && typeof item === 'object'))
    const constraints = (Array.isArray(metadata.constraints) ? metadata.constraints : []).filter((item): item is Record<string, unknown> => Boolean(item && typeof item === 'object'))
    const labels: string[] = []
    const inColumns = (value: unknown) => Array.isArray(value) && value.map(String).includes(columnName)
    if (constraints.some((constraint) => constraint.type === 'PRIMARY KEY' && inColumns(constraint.columns))) labels.push('PK')
    else if (constraints.some((constraint) => constraint.type === 'UNIQUE' && inColumns(constraint.columns)) || indexes.some((index) => index.unique === true && inColumns(index.columns))) labels.push('UQ')
    for (const index of indexes) if (index.unique !== true && inColumns(index.columns)) labels.push(String(index.name ?? ''))
    return labels.filter(Boolean)
  }

  const setSelectedKeys = (keys: string[]) => {
    const allowedKeys = [...new Set(keys.filter((key) => fieldByKey.has(key)))]
    const existingByKey = new Map(selects.map((select) => {
      const field = asRecord(select.field)
      return [queryFieldKey(String(field.source ?? DATABASE_QUERY_SOURCE_ALIAS), String(field.column ?? '')), String(select.alias ?? '')]
    }))
    const usedAliases = new Set<string>()
    const nextSelect = allowedKeys.map((key, index) => {
      const field = fieldByKey.get(key)!
      let alias = existingByKey.get(key) || queryOutputAlias(field.source, field.column)
      const baseAlias = alias
      let suffix = 2
      while (usedAliases.has(alias)) {
        const suffixText = `_${suffix++}`
        alias = `${baseAlias.slice(0, 64 - suffixText.length)}${suffixText}`
      }
      usedAliases.add(alias)
      return { field: { source: field.source, column: field.column }, alias: alias || `field_${index + 1}` }
    })
    const allowedAliases = new Set(nextSelect.map((item) => item.alias))
    updateAst({ select: nextSelect, orderBy: orders.filter((order) => allowedAliases.has(String(order.outputAlias ?? ''))) })
  }
  const toggleSelect = (key: string) => setSelectedKeys(selectedKeys.includes(key) ? selectedKeys.filter((item) => item !== key) : [...selectedKeys, key])

  const addJoin = async () => {
    if (!resource || !joinTableId) return
    const target = tableResources.find((item) => item.id === joinTableId)
    if (!target) return
    setJoinError('')
    try {
      const [leftMetadata, rightMetadata] = await Promise.all([ensureTableMetadata(resource), ensureTableMetadata(target)])
      const leftColumn = String((Array.isArray(leftMetadata.columns) ? asRecord(leftMetadata.columns[0]).name : '') ?? '')
      const rightColumn = String((Array.isArray(rightMetadata.columns) ? asRecord(rightMetadata.columns[0]).name : '') ?? '')
      if (!leftColumn || !rightColumn) throw new Error('关联表缺少可用字段，无法创建 JOIN')
      const usedAliases = new Set(sourceEntries.map((entry) => entry.alias))
      let number = joins.length + 1
      while (usedAliases.has(`j${number}`)) number += 1
      const alias = `j${number}`
      updateAst({ joins: [...joins, { type: 'INNER', source: { alias, resourceId: target.external_id }, on: [{ left: { source: DATABASE_QUERY_SOURCE_ALIAS, column: leftColumn }, operator: 'eq', right: { source: alias, column: rightColumn } }] }] })
      setJoinTableId('')
    } catch (err) {
      setJoinError(err instanceof Error ? err.message : '添加关联表失败')
    }
  }

  const updateJoin = (index: number, patch: Record<string, unknown>) => updateAst({ joins: joins.map((join, joinIndex) => joinIndex === index ? { ...join, ...patch } : join) })
  const updateJoinOn = (index: number, side: 'left' | 'right', fieldKey: string) => {
    const join = joins[index]
    const on = asRecord((Array.isArray(join.on) ? join.on : [])[0])
    updateJoin(index, { on: [{ ...on, [side]: queryFieldRef(fieldKey), operator: 'eq' }] })
  }
  const removeJoin = (index: number) => {
    const removedAlias = String(asRecord(joins[index].source).alias ?? '')
    const fallbackField = fieldOptions.find((field) => field.source === DATABASE_QUERY_SOURCE_ALIAS)
    const remainingJoins = joins.filter((_, joinIndex) => joinIndex !== index).map((join) => {
      const on = asRecord((Array.isArray(join.on) ? join.on : [])[0])
      const left = asRecord(on.left)
      return left.source === removedAlias && fallbackField ? { ...join, on: [{ ...on, left: { source: fallbackField.source, column: fallbackField.column } }] } : join
    })
    const remainingSelects = selects.filter((select) => String(asRecord(select.field).source ?? '') !== removedAlias)
    const remainingFilters = filters.filter((filter) => filter.source !== removedAlias)
    const allowedAliases = new Set(remainingSelects.map((select) => String(select.alias ?? '')))
    updateAst({ joins: remainingJoins, select: remainingSelects, orderBy: orders.filter((order) => allowedAliases.has(String(order.outputAlias ?? ''))), ...encodeFilters(remainingFilters) })
  }

  const addFilter = () => {
    const field = fieldOptions[0]
    if (field) writeFilters([...filters, { source: field.source, column: field.column, operator: 'eq', value: '' }])
  }
  const updateFilter = (index: number, patch: Record<string, unknown>) => writeFilters(filters.map((filter, filterIndex) => filterIndex === index ? { ...filter, ...patch } : filter))
  const removeFilter = (index: number) => writeFilters(filters.filter((_, filterIndex) => filterIndex !== index))
  const addOrder = () => updateAst({ orderBy: [...orders, { outputAlias: String(selectedAliases[0] ?? ''), direction: 'asc' }] })
  const updateOrder = (index: number, patch: Record<string, unknown>) => updateAst({ orderBy: orders.map((order, orderIndex) => orderIndex === index ? { ...order, ...patch } : order) })
  const removeOrder = (index: number) => updateAst({ orderBy: orders.filter((_, orderIndex) => orderIndex !== index) })

  const joinsReady = joins.every((join) => {
    const source = asRecord(join.source)
    const on = asRecord((Array.isArray(join.on) ? join.on : [])[0])
    const left = asRecord(on.left)
    const right = asRecord(on.right)
    return Boolean(source.alias && source.resourceId && left.source && left.column && right.source && right.column)
  })

  const runPreview = useCallback(async () => {
    if (!resource) return
    setPreviewLoading(true)
    onPreviewLoadingChange(true)
    setPreviewError('')
    setPreview(null)
    try {
      const previewAst = normalizedDatabaseQueryAst(parameters.ast, resource)
      const started = await startExecution({
        taskId: `ui-db-preview-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
        resourceId: resource.id,
        action: 'database.query.preview', actionVersion: 1,
        parameters: { ast: previewAst }, reason: '受治理多表查询预检',
      })
      const completed = await waitForTerminalExecution(started)
      if (completed.status !== 'SUCCEEDED') throw new Error(completed.errorMessage || completed.errorCode || '查询预检失败')
      const previewHash = String(completed.result?.previewHash ?? completed.result?.planHash ?? '')
      if (!/^[a-f0-9]{64}$/.test(previewHash)) throw new Error('预检未返回有效 previewHash')
      setPreview(completed.result ?? {})
      onPreviewVerdictChange(String(completed.result?.verdict ?? ''))
      setParameters((current) => ({ ...current, ast: previewAst, previewHash }))
    } catch (err) {
      onPreviewVerdictChange('')
      setPreviewError(err instanceof Error ? err.message : '查询预检失败')
    } finally {
      setPreviewLoading(false)
      onPreviewLoadingChange(false)
    }
  }, [onPreviewLoadingChange, onPreviewVerdictChange, parameters.ast, resource, setParameters, startExecution])

  useEffect(() => {
    if (previewRequest <= 0 || previewRequest === handledPreviewRequest.current) return
    handledPreviewRequest.current = previewRequest
    void runPreview()
  }, [previewRequest, runPreview])
  useEffect(() => () => { onPreviewLoadingChange(false); onPreviewVerdictChange('') }, [onPreviewLoadingChange, onPreviewVerdictChange])

  const usedResourceIDs = new Set([resource?.id ?? '', ...sourceEntries.slice(1).map((entry) => entry.table.id)])
  const availableJoinTables = tableResources.filter((table) => !usedResourceIDs.has(table.id))
  const rowStat = Number(baseMetadata?.estimatedRows ?? 0)
  return <fieldset className="database-query-builder">
    <legend>返回字段与筛选（支持多表 JOIN）</legend>
    {!resource ? <div className="form-error" role="alert">请先选择数据库表资源。</div>
      : loading ? <div className="onboarding-hint"><Loader2 size={16} className="spin" /><span>正在读取表结构、索引与行数…</span></div>
        : error ? <div className="form-error" role="alert">{error}<button type="button" className="quiet-button" onClick={loadBaseMetadata}>重试</button></div>
          : baseMetadata ? <>
            <div className="database-builder-meta">主表 {resource.namespace ? `${resource.namespace}.` : ''}{resource.name} · {fieldOptions.filter((field) => field.source === DATABASE_QUERY_SOURCE_ALIAS).length} 列 · {rowStat > 0 ? `约 ${rowStat.toLocaleString()} 行` : '行数未知'} · 当前 {sourceEntries.length} 张表</div>
            <div className="database-builder-filters">
              <div className="database-builder-filter-head"><strong>关联表</strong><span>仅可关联同一数据库集成中的活动表，最多 8 个 JOIN</span></div>
              <div className="database-builder-filter-row">
                <select value={joinTableId} onChange={(event) => setJoinTableId(event.target.value)} disabled={tableLoading || joins.length >= 8}><option value="">选择要关联的表</option>{availableJoinTables.map((table) => <option key={table.id} value={table.id}>{table.namespace ? `${table.namespace}.` : ''}{table.name}</option>)}</select>
                <button type="button" className="quiet-button" disabled={!joinTableId || tableLoading || joins.length >= 8} onClick={() => void addJoin()}>{tableLoading ? '读取表目录…' : '添加 JOIN'}</button>
              </div>
              {joinError && <div className="form-error" role="alert">{joinError}</div>}
              {joins.length === 0 ? <div className="onboarding-hint">尚未关联其它表；添加后可配置 INNER/LEFT JOIN 与两侧字段。</div> : joins.map((join, index) => {
                const source = asRecord(join.source)
                const alias = String(source.alias ?? '')
                const table = sourceEntries.find((entry) => entry.alias === alias)?.table
                const on = asRecord((Array.isArray(join.on) ? join.on : [])[0])
                const left = asRecord(on.left)
                const right = asRecord(on.right)
                const leftFields = fieldOptions.filter((field) => sourceEntries.slice(0, index + 1).some((entry) => entry.alias === field.source))
                const rightFields = fieldOptions.filter((field) => field.source === alias)
                return <div className="database-builder-filter-row" key={`${alias}:${String(source.resourceId)}`}>
                  <select value={String(join.type ?? 'INNER')} onChange={(event) => updateJoin(index, { type: event.target.value })}><option value="INNER">INNER JOIN</option><option value="LEFT">LEFT JOIN</option></select>
                  <span>{table ? `${table.namespace ? `${table.namespace}.` : ''}${table.name}` : String(source.resourceId)}</span>
                  <select value={queryFieldKey(String(left.source ?? ''), String(left.column ?? ''))} onChange={(event) => updateJoinOn(index, 'left', event.target.value)}>{leftFields.map((field) => <option key={field.key} value={field.key}>{field.table.name}.{field.column}</option>)}</select>
                  <span>=</span>
                  <select value={queryFieldKey(String(right.source ?? ''), String(right.column ?? ''))} onChange={(event) => updateJoinOn(index, 'right', event.target.value)}>{rightFields.map((field) => <option key={field.key} value={field.key}>{field.table.name}.{field.column}</option>)}</select>
                  <button type="button" className="icon-button" onClick={() => removeJoin(index)} aria-label="删除关联表"><X size={14} /></button>
                </div>
              })}
            </div>
            <div className="database-builder-selection-head"><div><strong>返回字段</strong><small>已选择 {selectedKeys.length} / {fieldOptions.length} 个字段，可跨表多选</small></div><div><button type="button" className="quiet-button" disabled={selectedKeys.length === fieldOptions.length} onClick={() => setSelectedKeys(fieldOptions.map((field) => field.key))}>全选</button><button type="button" className="quiet-button" disabled={selectedKeys.length === 0} onClick={() => setSelectedKeys([])}>清空</button></div></div>
            <div className="database-builder-columns" aria-label="选择返回字段（支持跨表多选）">{fieldOptions.map((field) => <label key={field.key} className={selectedKeys.includes(field.key) ? 'selected' : ''}><input type="checkbox" checked={selectedKeys.includes(field.key)} onChange={() => toggleSelect(field.key)} /><span className="database-column-details"><strong>{field.table.name}.{field.column}</strong><small>{String(field.definition.dataType)}{field.definition.nullable === true ? ' · 可空' : ''} · 别名 {field.source}</small></span>{indexLabels(field.metadata, field.column).map((label) => <code className="index-badge" key={label}>{label}</code>)}</label>)}</div>
            <div className="database-builder-filters">
              <div className="database-builder-filter-head"><strong>筛选条件</strong><button type="button" className="quiet-button" disabled={fieldOptions.length === 0} onClick={addFilter}>添加筛选</button></div>
              {filters.length === 0 ? <div className="onboarding-hint">未添加筛选，将按 limit 返回查询结果。</div> : filters.map((filter, index) => {
                const key = queryFieldKey(filter.source, filter.column)
                const field = fieldByKey.get(key)
                return <div className="database-builder-filter-row" key={index}>
                  <select value={key} onChange={(event) => updateFilter(index, queryFieldRef(event.target.value))}>{fieldOptions.map((option) => <option key={option.key} value={option.key}>{option.table.name}.{option.column}</option>)}</select>
                  <select value={filter.operator} onChange={(event) => updateFilter(index, { operator: event.target.value })}>{DATABASE_FILTER_OPERATORS.map((value) => <option key={value} value={value}>{value}</option>)}</select>
                  <DatabaseFilterValueInput column={field?.definition} operator={filter.operator} value={filter.value} onChange={(value) => updateFilter(index, { value })} />
                  <button type="button" className="icon-button" onClick={() => removeFilter(index)} aria-label="删除筛选"><X size={14} /></button>
                </div>
              })}
            </div>
            <div className="database-builder-filters">
              <div className="database-builder-filter-head"><strong>排序</strong><button type="button" className="quiet-button" disabled={selectedAliases.length === 0} onClick={addOrder}>添加排序</button></div>
              {orders.length === 0 ? <div className="onboarding-hint">未添加排序。</div> : orders.map((order, index) => <div className="database-builder-filter-row" key={index}>
                <select value={String(order.outputAlias ?? '')} onChange={(event) => updateOrder(index, { outputAlias: event.target.value })}>{selects.map((select) => { const field = asRecord(select.field); return <option key={String(select.alias)} value={String(select.alias)}>{String(field.source)}.{String(field.column)} → {String(select.alias)}</option> })}</select>
                <select value={String(order.direction ?? 'asc')} onChange={(event) => updateOrder(index, { direction: event.target.value })}><option value="asc">asc</option><option value="desc">desc</option></select>
                <button type="button" className="icon-button" onClick={() => removeOrder(index)} aria-label="删除排序"><X size={14} /></button>
              </div>)}
            </div>
            <label><span>返回行数（limit）</span><input type="number" min={1} max={1000} value={limit} onChange={(event) => updateAst({ limit: event.target.value === '' ? undefined : Math.max(1, Math.min(1000, Number(event.target.value))) })} /></label>
            <div className="database-builder-filters">
              <div className="database-builder-filter-head"><strong>预检</strong><button type="button" className="secondary-button preview-button" disabled={previewLoading || selectedKeys.length === 0 || !joinsReady} onClick={runPreview}>{previewLoading ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}{previewLoading ? '正在预检' : '预览并生成 previewHash'}</button></div>
              {previewError && <div className="form-error" role="alert">{previewError}</div>}
              {preview && <div className="database-builder-meta"><strong>SQL：</strong><code>{String(preview.sql ?? '-')}</code> · 判定 {String(preview.verdict ?? '-')} · 预估扫描 {String(preview.estimatedRows ?? '-')} 行{Array.isArray(preview.warnings) && preview.warnings.length > 0 ? ` · 告警 ${preview.warnings.length} 条` : ''}</div>}
            </div>
          </> : null}
  </fieldset>
}

function isCloudLogAction(actionName: string) {
  return actionName === 'huawei.lts.logs.query' || actionName === 'aliyun.sls.logs.query'
}

function isCloudLogTimeParameter(actionName: string, key: string) {
  return (actionName === 'huawei.lts.logs.query' && (key === 'startTime' || key === 'endTime')) ||
    (actionName === 'aliyun.sls.logs.query' && (key === 'from' || key === 'to'))
}

function epochToLocalInput(epoch: number) {
  if (!Number.isFinite(epoch) || epoch <= 0) return ''
  const date = new Date(epoch)
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60 * 1000)
  return local.toISOString().slice(0, 19)
}
