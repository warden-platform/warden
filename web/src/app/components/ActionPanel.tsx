import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { parse as parseYaml, stringify as stringifyYaml } from 'yaml'
import { Activity, Bot, ChevronDown, FolderKanban, KeyRound, Loader2, Play, ShieldAlert, ShieldCheck, X } from 'lucide-react'
import type { ApiAction, ApiConnector, ApiError, ApiExecution, ApiProject, ApiResource } from '../../shared/api'
import {
  canUser,
  createExecution,
  getExecution,
  listAvailableActions,
  listAvailableResources,
  listConnectors,
  listProjects,
  listResourceActions,
} from '../../shared/api'
import { mapActionName, mapRiskHint, mapRiskLevel } from '../../shared/types'
import { useApp } from './Toast'
import { apiResourceToView } from '../lib/mappers'
import { EMPTY_ARRAY } from '../lib/useApiQuery'
import { cloudRegionLabel, resourceTypeLabel } from '../lib/labels'
import { Badge } from './Badge'
import { ErrorState } from './States'
import { ExecutionResult } from './ExecutionResult'
import { GenericKubernetesEditor, MutationPrefillState, NetworkInterfacePreviewPanel } from './action-panel/PreviewPanels'
import { ActionParameterFields, LogTimePresets } from './action-panel/ParameterFields'
import { DatabaseTableQueryBuilder } from './action-panel/DatabaseTableQueryBuilder'
import { DatabaseQueryAstBuilder } from './action-panel/DatabaseQueryAstBuilder'
import { DatabaseQueryTemplatePicker } from './action-panel/DatabaseQueryTemplatePicker'
import {
  GENERIC_KUBERNETES_KINDS,
  GENERIC_KUBERNETES_KIND_OPTIONS,
  GRAFANA_UPDATE_READ_ACTIONS,
  INTERNAL_PREVIEW_ACTIONS,
  KUBERNETES_MUTATION_READ_ACTIONS,
  NETWORK_MUTATION_PREVIEW_ACTIONS,
  type ExecutionStarter,
  type NetworkInterfacePreview,
  type ParameterSchema,
} from './action-panel/constants'
import {
  actionNeedsUpgrade,
  asRecord,
  connectorSupportsAction,
  genericPreviewableSignature,
  isCloudLogAction,
  loadGenericKubernetesParameters,
  loadGrafanaUpdateParameters,
  loadKubernetesMutationParameters,
  networkInterfacePreviewSignature,
  normalizedDatabaseQueryAst,
  waitForTerminalExecution,
  withTimeout,
} from './action-panel/utils'

const resourceNamespace = (resource: ApiResource) => resource.namespace ?? ''

// Lightweight action drawer triggered from resource context. Replaces the
// old global ActionDrawer: same execution form, but opened with a preselected
// resource instead of a top-level cascade.
// 资源类型专属编辑器（K8s Manifest / H3C 接口预检 / 数据库查询构建器等）
// 拆分在 ./action-panel/ 目录，这里只保留共享的执行流程骨架。
// Module-level helpers: time/ID generation stays out of render-phase code.
function lastHourRangeMs() {
  const now = Date.now()
  return { startTime: now - 60 * 60 * 1000, endTime: now }
}
function lastHourRangeSec() {
  const now = Math.floor(Date.now() / 1000)
  return { from: now - 60 * 60, to: now }
}
function previewTaskId(prefix: string) {
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

// Pure default-parameter builder shared by the render-phase prefill adjustment
// and the async prefill effect.
function buildActionDefaults(
  resource: ApiResource,
  selectedAction: ApiAction,
  resourceContainers: { name: string; image: string }[],
): Record<string, unknown> {
  const schemaProperties = (selectedAction.parameter_schema?.properties ?? {}) as Record<string, { default?: unknown }>
  const next: Record<string, unknown> = {}
  if ('name' in schemaProperties) next.name = resource.name
  if ('namespace' in schemaProperties)
    next.namespace = resource.resource_type === 'namespace' ? resource.name : (resource.namespace ?? '')
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
    const range = lastHourRangeMs()
    next.startTime = range.startTime
    next.endTime = range.endTime
  }
  if (selectedAction.name === 'aliyun.sls.logs.query') {
    const range = lastHourRangeSec()
    next.from = range.from
    next.to = range.to
  }
  if (selectedAction.name.replace(/@v\d+$/, '') === 'database.query.execute') {
    next.ast = normalizedDatabaseQueryAst(next.ast, resource)
  }
  if (selectedAction.name.startsWith('kubernetes.resource.')) {
    const selectedKind = GENERIC_KUBERNETES_KINDS[resource.resource_type] ?? GENERIC_KUBERNETES_KIND_OPTIONS[0]
    next.apiVersion = selectedKind.apiVersion
    next.kind = selectedKind.kind
    if (
      selectedAction.name === 'kubernetes.resource.create' ||
      (selectedAction.name === 'kubernetes.resource.preview' && resource.resource_type === 'namespace')
    ) {
      if (selectedAction.name === 'kubernetes.resource.preview') next.operation = 'create'
      next.manifest = {
        apiVersion: selectedKind.apiVersion,
        kind: selectedKind.kind,
        metadata: { name: 'warden-e2e-7213-resource', namespace: resource.name },
      }
    }
  }
  return next
}

function prefillReadAction(selectedAction: ApiAction): string {
  const genericRead = selectedAction.name === 'kubernetes.resource.update' || selectedAction.name === 'kubernetes.resource.delete'
  return (
    GRAFANA_UPDATE_READ_ACTIONS[selectedAction.name] ||
    KUBERNETES_MUTATION_READ_ACTIONS[selectedAction.name] ||
    (genericRead ? 'kubernetes.resource.get' : '')
  )
}

export function ActionPanel({
  initialResource,
  initialAction,
  initialActingProjectId,
  onClose,
}: {
  initialResource: ApiResource | null
  initialAction?: string
  initialActingProjectId?: string
  onClose: () => void
}) {
  const { notify, auth } = useApp()
  const isTenantAdmin = canUser(auth.user, 'warden:tenant:admin')
  const [resources, setResources] = useState<ApiResource[]>([])
  const [connectors, setConnectors] = useState<ApiConnector[]>([])
  const [actions, setActions] = useState<ApiAction[]>([])
  const [projects, setProjects] = useState<ApiProject[]>([])
  const [actionId, setActionId] = useState('')
  const [connectorId, setConnectorId] = useState('')
  const [namespace, setNamespace] = useState('')
  const [resourceId, setResourceId] = useState(initialResource?.id ?? '')
  const [projectDraft, setProjectDraft] = useState<{ resourceId: string; value: string } | null>(null)
  const [taskId] = useState(() => `ui-${Date.now().toString(36)}`)
  const [parameters, setParameters] = useState<Record<string, unknown>>({})
  const [reason, setReason] = useState('通过 Warden 管理界面发起')
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
  const [genericPreview, setGenericPreview] = useState<{
    before?: unknown
    after?: unknown
    previewHash: string
    signature: string
  } | null>(null)
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
    withTimeout(
      Promise.all([
        isTenantAdmin ? listConnectors() : Promise.resolve([]),
        initialResource ? listResourceActions(initialResource.id, true) : listAvailableActions(),
        listProjects(),
      ]),
    )
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
          const requested = (acts ?? []).find(
            (action) =>
              action.name === initialAction &&
              action.resource_type === initial.resource_type &&
              action.available !== false &&
              !INTERNAL_PREVIEW_ACTIONS.has(action.name),
          )
          const availableForResource = (acts ?? []).filter(
            (action) =>
              action.resource_type === initial.resource_type && action.available !== false && !INTERNAL_PREVIEW_ACTIONS.has(action.name),
          )
          const safestDefault = availableForResource.find((action) => action.risk_level === 'low') ?? availableForResource[0]
          setActionId(requested?.name ?? safestDefault?.name ?? '')
        }
      })
      .catch((e: ApiError) => setLoadError(e.message || '加载资源失败'))
      .finally(() => setLoading(false))
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const resource = useMemo(() => resources.find((r) => r.id === resourceId), [resources, resourceId])
  const resourceProjectIds = resource?.project_ids ?? []
  // Keyed draft: the project selection follows the resource's defaults until
  // the user picks one; changing resource re-derives (no sync effect).
  const defaultActingProjectId =
    initialActingProjectId && resourceProjectIds.includes(initialActingProjectId)
      ? initialActingProjectId
      : resourceProjectIds.length === 1
        ? resourceProjectIds[0]
        : ''
  const actingProjectId =
    projectDraft && projectDraft.resourceId === (resource?.id ?? '') ? projectDraft.value : defaultActingProjectId
  const setActingProjectId = (value: string) => setProjectDraft({ resourceId: resource?.id ?? '', value })
  const createScopedExecution: ExecutionStarter = useCallback(
    (request) => createExecution({ ...request, actingProjectId }),
    [actingProjectId],
  )
  const connectorName = (id: string) => connectors.find((c) => c.id === id)?.name ?? ''

  // Invalidate stale previews when the acting project changes (render-phase
  // state adjustment — the React-recommended alternative to a sync effect).
  const [prevActingProject, setPrevActingProject] = useState(actingProjectId)
  if (prevActingProject !== actingProjectId) {
    setPrevActingProject(actingProjectId)
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
  }
  // When opened with a preselected resource (from the resources page), lock the
  // action list to that resource's type so a Jenkins resource doesn't surface
  // K8s actions. Without initialResource (from ExecutionsPage "新建执行"), show all.
  const visibleActions = initialResource
    ? actions.filter(
        (a) =>
          a.resource_type === initialResource.resource_type &&
          !INTERNAL_PREVIEW_ACTIONS.has(a.name) &&
          a.name !== 'provider.request' &&
          a.name !== 'provider.write',
      )
    : actions.filter((a) => !INTERNAL_PREVIEW_ACTIONS.has(a.name) && a.name !== 'provider.request' && a.name !== 'provider.write')
  const selectedAction = visibleActions.find((a) => a.name === actionId)
  const controlPlaneAction = selectedAction?.executor_type === 'control_plane'
  const noAvailableAction = visibleActions.length > 0 && visibleActions.every((action) => action.available === false)
  const actionResources = resources.filter((item) => item.resource_type === selectedAction?.resource_type)
  // The server performs the authoritative 90-second heartbeat check when an
  // execution is created. Do not permanently disable a long-lived form from
  // the connector heartbeat snapshot captured when the drawer first opened.
  const actionConnectors: Array<Pick<ApiConnector, 'id' | 'name'>> = isTenantAdmin
    ? connectors.filter(
        (connector) =>
          connector.status === 'ACTIVE' &&
          selectedAction &&
          connectorSupportsAction(connector, selectedAction) &&
          actionResources.some((item) => item.connector_id === connector.id),
      )
    : [...new Set(actionResources.map((item) => item.connector_id).filter(Boolean))].map((id) => ({ id, name: '项目执行通道' }))
  const noExecutionTarget = Boolean(selectedAction && !controlPlaneAction && actionConnectors.length === 0)
  const namespaces = [
    ...new Set(actionResources.filter((item) => controlPlaneAction || item.connector_id === connectorId).map(resourceNamespace)),
  ].sort()
  const targetResources = actionResources.filter(
    (item) => controlPlaneAction || (item.connector_id === connectorId && resourceNamespace(item) === namespace),
  )
  const actionName = selectedAction?.name ?? ''
  // Action definitions normally expose the base name while connector
  // capabilities use @vN. Normalize defensively so database-specific editors
  // remain visible if an API response carries the versioned capability key.
  const normalizedActionName = actionName.replace(/@v\d+$/, '')
  const jenkinsContextTargetLocked = Boolean(initialResource && actionName.startsWith('jenkins.'))
  const approvalExemptChange = normalizedActionName === 'jenkins.job.create'
  const protocolChange =
    !approvalExemptChange &&
    (actionName.startsWith('jenkins.') || actionName.startsWith('grafana.')) &&
    selectedAction?.risk_level !== 'low' &&
    !actionName.endsWith('.query')
  const dashboardConfigChange = actionName === 'namespace.dashboard_configmap.apply' || actionName === 'configmap.dashboard.delete'
  const cloudChange = actionName.startsWith('huawei.ecs.') && selectedAction?.risk_level !== 'low'
  const genericKubernetesMutation =
    actionName === 'kubernetes.resource.create' ||
    actionName === 'kubernetes.resource.update' ||
    actionName === 'kubernetes.resource.delete'
  const genericKubernetesPreviewable = actionName === 'kubernetes.resource.create' || actionName === 'kubernetes.resource.update'
  const kubernetesChange = Boolean(KUBERNETES_MUTATION_READ_ACTIONS[actionName]) || genericKubernetesMutation
  const networkPreviewAction = NETWORK_MUTATION_PREVIEW_ACTIONS[normalizedActionName]
  const networkInterfaceMutation = Boolean(networkPreviewAction)
  const highRiskChange = !approvalExemptChange && (selectedAction?.risk_level === 'high' || selectedAction?.risk_level === 'critical')
  const databaseCostNeedsApproval = normalizedActionName === 'database.query.execute' && databasePreviewVerdict === 'require_approval'
  const needsApproval = Boolean(
    databaseCostNeedsApproval ||
    highRiskChange ||
    protocolChange ||
    dashboardConfigChange ||
    cloudChange ||
    (!approvalExemptChange && resource?.environment === 'production' && selectedAction?.risk_level !== 'low'),
  )
  const actionProperties = (selectedAction?.parameter_schema?.properties ?? {}) as Record<string, ParameterSchema>
  const requiredParameters = (selectedAction?.parameter_schema?.required ?? []) as string[]
  const nameIsEditable = actionName === 'jenkins.job.create' || actionName === 'huawei.ecs.create'
  const genericManagedFields = new Set(['apiVersion', 'kind', 'manifest', 'previewHash', 'expectedResourceVersion', 'operation'])
  const genericConcreteRead = actionName === 'kubernetes.resource.get' && resource?.resource_type !== 'namespace'
  const editableParameters = Object.entries(actionProperties).filter(
    ([key]) =>
      (key !== 'name' || nameIsEditable) &&
      key !== 'namespace' &&
      key !== 'expectedGeneration' &&
      !(genericKubernetesMutation && genericManagedFields.has(key)) &&
      !(genericConcreteRead && (key === 'apiVersion' || key === 'kind')) &&
      !(selectedAction?.name === 'grafana.folder.update' && key === 'version') &&
      !(normalizedActionName === 'database.table.rows.query' && (key === 'columns' || key === 'filters' || key === 'orderBy')) &&
      !(
        (normalizedActionName === 'database.query.execute' || normalizedActionName === 'database.query.preview') &&
        (key === 'ast' || key === 'previewHash')
      ) &&
      !(networkInterfaceMutation && key === 'previewHash') &&
      !(
        normalizedActionName === 'database.query_template.execute' &&
        (key === 'templateId' || key === 'templateVersion' || key === 'bindings')
      ),
  )
  const resourceContainers = (Array.isArray(resource?.metadata?.containers) ? resource.metadata.containers : EMPTY_ARRAY) as {
    name: string
    image: string
  }[]
  const selectedContainer = resourceContainers.find((item) => item.name === parameters.container)
  const deleteConfirmationMismatch =
    actionName === 'jenkins.job.delete' && String(parameters.confirmJobName ?? '') !== (resource?.name ?? '')
  const ecsDeleteConfirmationMismatch =
    actionName === 'huawei.ecs.delete' && String(parameters.confirmServerName ?? '') !== (resource?.name ?? '')
  const genericDeleteConfirmationMismatch =
    actionName === 'kubernetes.resource.delete' &&
    String(parameters.confirmResource ?? '') !==
      `${String(parameters.kind ?? '')}/${String(parameters.namespace ?? '')}/${resource?.name ?? ''}`
  const databaseExecuteNeedsPreview =
    normalizedActionName === 'database.query.execute' && !/^[a-f0-9]{64}$/.test(String(parameters.previewHash ?? ''))
  const databaseSelects = asRecord(parameters.ast).select
  const databaseExecuteMissingFields =
    normalizedActionName === 'database.query.execute' && (!Array.isArray(databaseSelects) || databaseSelects.length === 0)
  const previewSignature = genericPreviewableSignature(parameters)
  const genericPreviewCurrent = Boolean(
    genericPreview && genericPreview.signature === previewSignature && parameters.previewHash === genericPreview.previewHash,
  )
  const currentNetworkPreviewSignature = networkInterfacePreviewSignature(normalizedActionName, resource?.id ?? '', parameters)
  const networkPreviewCurrent = Boolean(
    networkPreview && networkPreview.signature === currentNetworkPreviewSignature && parameters.previewHash === networkPreview.previewHash,
  )

  // Prefill defaults on resource/action/project change: render-phase state
  // adjustment (React-recommended) instead of a synchronous-setState effect.
  // The key includes data readiness so prefill runs once resources/actions load.
  const prefillKey = [resourceId, actionId, prefillNonce, actingProjectId, resource?.id ?? '', selectedAction?.id ?? ''].join(':')
  const [prevPrefillKey, setPrevPrefillKey] = useState('')
  if (prevPrefillKey !== prefillKey) {
    setPrevPrefillKey(prefillKey)
    if (resource && selectedAction && actingProjectId) {
      const next = buildActionDefaults(resource, selectedAction, resourceContainers)
      setParameters(next)
      setGenericPreview(null)
      setNetworkPreview(null)
      setDatabasePreviewVerdict('')
      setPreviewError('')
      setManifestError('')
      setManifestText(next.manifest ? stringifyYaml(next.manifest) : '')
      if (prefillReadAction(selectedAction)) {
        setPrefillLoading(true)
        setPrefillError('')
      } else {
        setPrefillLoading(false)
        setPrefillError('')
      }
    }
  }

  useEffect(() => {
    if (!resource || !selectedAction || !actingProjectId) return
    const readAction = prefillReadAction(selectedAction)
    if (!readAction) return
    let cancelled = false
    const next = buildActionDefaults(resource, selectedAction, resourceContainers)
    const genericRead = selectedAction.name === 'kubernetes.resource.update' || selectedAction.name === 'kubernetes.resource.delete'
    const loadParameters = genericRead
      ? loadGenericKubernetesParameters(resource, selectedAction.name, createScopedExecution)
      : KUBERNETES_MUTATION_READ_ACTIONS[selectedAction.name]
        ? loadKubernetesMutationParameters(resource, readAction, selectedAction.name, createScopedExecution)
        : loadGrafanaUpdateParameters(resource, readAction, selectedAction.name, createScopedExecution)
    loadParameters
      .then((loaded) => {
        if (!cancelled) {
          const merged = { ...next, ...loaded }
          setParameters(merged)
          setManifestText(merged.manifest ? stringifyYaml(merged.manifest) : '')
        }
      })
      .catch((error: Error) => {
        if (!cancelled)
          setPrefillError(
            error.message ||
              (KUBERNETES_MUTATION_READ_ACTIONS[selectedAction.name] ? '读取 Kubernetes 当前状态失败' : '读取当前 Grafana 配置失败'),
          )
      })
      .finally(() => {
        if (!cancelled) setPrefillLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [resourceId, actionId, selectedAction?.id, prefillNonce, actingProjectId, createScopedExecution, resource, resourceContainers, selectedAction])

  useEffect(() => {
    if (!execution || ['SUCCEEDED', 'FAILED', 'CANCELLED', 'EXPIRED', 'UNKNOWN'].includes(execution.status)) return
    const timer = window.setInterval(() => {
      getExecution(execution.id)
        .then(setExecution)
        .catch(() => undefined)
    }, 1500)
    return () => window.clearInterval(timer)
  }, [execution])

  const chooseAction = (name: string) => {
    const action = actions.find((item) => item.name === name)
    if (action?.available === false) return
    setActionId(name)
    if (!action) return
    setTypeLoading(true)
    loadType(action.resource_type)
      .then((res) => {
        setResources(res)
        // Switching between actions for the same resource type must keep the
        // resource that opened the drawer. Falling back to the first catalog
        // item can silently retarget a Kubernetes mutation to another
        // namespace/workload while the user is only choosing an action.
        const target =
          res.find((item) => item.id === resourceId && item.resource_type === action.resource_type) ??
          res.find((item) => item.id === initialResource?.id && item.resource_type === action.resource_type) ??
          res.find((item) => item.resource_type === action.resource_type)
        setConnectorId(target?.connector_id ?? '')
        setNamespace(target?.namespace ?? '')
        setResourceId(target?.id ?? '')
      })
      .catch(() => undefined)
      .finally(() => setTypeLoading(false))
  }

  const chooseConnector = (id: string) => {
    const first = actionResources.find((item) => item.connector_id === id)
    setConnectorId(id)
    setNamespace(first?.namespace ?? '')
    setResourceId(first?.id ?? '')
  }

  const chooseNamespace = (value: string) => {
    const first = actionResources.find((item) => item.connector_id === connectorId && resourceNamespace(item) === value)
    setNamespace(value)
    setResourceId(first?.id ?? '')
  }

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!resource || !actionId) {
      notify('当前动作没有可用的 ACTIVE 资源或执行通道', 'error')
      return
    }
    if (databaseExecuteNeedsPreview) {
      setDatabasePreviewRequest((request) => request + 1)
      return
    }
    if (networkInterfaceMutation && !networkPreviewCurrent) {
      notify('请先完成与当前参数一致的接口变更预检', 'error')
      return
    }
    setSubmitting(true)
    const submittedParameters =
      normalizedActionName === 'database.query.execute'
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
    })
      .then((exec) => {
        setExecution(exec)
      })
      .catch((e: ApiError) => notify(`提交失败：${e.message}`, 'error'))
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
      setParameters((current) => {
        const next: Record<string, unknown> = { ...current, manifest: parsed }
        delete next.previewHash
        return next
      })
    } catch (error) {
      setManifestError(error instanceof Error ? error.message : 'YAML/JSON 格式无效')
      setParameters((current) => {
        const next = { ...current }
        delete next.previewHash
        return next
      })
    }
  }

  const chooseGenericKind = (value: string) => {
    const selected = GENERIC_KUBERNETES_KIND_OPTIONS.find((item) => `${item.apiVersion}/${item.kind}` === value)
    if (!selected) return
    const manifest = asRecord(parameters.manifest)
    const nextManifest = {
      ...manifest,
      apiVersion: selected.apiVersion,
      kind: selected.kind,
      metadata: { ...asRecord(manifest.metadata), namespace: parameters.namespace },
    }
    setParameters((current) => {
      const next: Record<string, unknown> = { ...current, apiVersion: selected.apiVersion, kind: selected.kind, manifest: nextManifest }
      delete next.previewHash
      return next
    })
    setManifestText(stringifyYaml(nextManifest))
    setGenericPreview(null)
    setPreviewError('')
  }

  const runGenericPreview = async () => {
    if (!resource || manifestError || !parameters.manifest) return
    setPreviewLoading(true)
    setPreviewError('')
    setGenericPreview(null)
    try {
      const previewParameters: Record<string, unknown> = {
        operation: actionName.endsWith('.create') ? 'create' : 'update',
        apiVersion: parameters.apiVersion,
        kind: parameters.kind,
        namespace: parameters.namespace,
        manifest: parameters.manifest,
      }
      if (actionName.endsWith('.update')) {
        previewParameters.name = resource.name
        previewParameters.expectedResourceVersion = parameters.expectedResourceVersion
      }
      const started = await createScopedExecution({
        taskId: previewTaskId('ui-k8s-preview'),
        resourceId: resource.id,
        action: 'kubernetes.resource.preview',
        actionVersion: 1,
        parameters: previewParameters,
        reason: 'Kubernetes 变更 server-side dry-run 预检',
      })
      const completed = await waitForTerminalExecution(started)
      if (completed.status !== 'SUCCEEDED') throw new Error(completed.errorMessage || completed.errorCode || 'Kubernetes 预检失败')
      const previewHash = String(completed.result?.previewHash ?? '')
      if (!/^[a-f0-9]{64}$/.test(previewHash)) throw new Error('预检未返回有效 previewHash')
      const signature = genericPreviewableSignature(parameters)
      setGenericPreview({ before: completed.result?.before, after: completed.result?.after, previewHash, signature })
      setParameters((current) => ({ ...current, previewHash }))
    } catch (error) {
      setPreviewError(error instanceof Error ? error.message : 'Kubernetes 预检失败')
    } finally {
      setPreviewLoading(false)
    }
  }

  const runNetworkPreview = async () => {
    if (!resource || !networkPreviewAction) return
    setPreviewLoading(true)
    setPreviewError('')
    setNetworkPreview(null)
    try {
      const previewParameters: Record<string, unknown> = { ...parameters }
      delete previewParameters.previewHash
      if (normalizedActionName === 'network.interface.trunk_vlan.add') previewParameters.operation = 'add'
      if (normalizedActionName === 'network.interface.trunk_vlan.remove') previewParameters.operation = 'remove'
      const started = await createScopedExecution({
        taskId: previewTaskId('ui-network-preview'),
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
    } finally {
      setPreviewLoading(false)
    }
  }

  const resourceView = resource ? apiResourceToView(resource, connectorName(resource.connector_id)) : null

  if (execution)
    return (
      <>
        <div className="drawer-scrim" aria-hidden="true" />
        <aside className="drawer" aria-label="执行跟踪">
          <div className="drawer-header">
            <div>
              <span className="eyebrow">
                <Activity size={15} />
                Execution Tracking
              </span>
              <h2>{mapActionName(selectedAction?.name ?? actionId)}</h2>
              <p>
                {resource?.namespace}/{resource?.name}
              </p>
            </div>
            <button className="icon-button" onClick={onClose} aria-label="关闭">
              <X size={20} />
            </button>
          </div>
          <ExecutionResult execution={execution} actionName={selectedAction?.name ?? actionId} onClose={onClose} />
        </aside>
      </>
    )

  return (
    <>
      <div className="drawer-scrim" aria-hidden="true" />
      <aside className="drawer" aria-label="请求受控动作">
        <div className="drawer-header">
          <div>
            <span className="eyebrow">
              <Bot size={15} />
              Agent Action Request
            </span>
            <h2>请求受控动作</h2>
            <p>动作参数会经过 Schema 和策略双重校验。</p>
          </div>
          <button className="icon-button" onClick={onClose} aria-label="关闭">
            <X size={20} />
          </button>
        </div>
        {loading ? (
          <div style={{ padding: 30, textAlign: 'center' }}>
            <Loader2 size={22} className="spin" />
          </div>
        ) : loadError ? (
          <ErrorState message={loadError} onRetry={() => window.location.reload()} />
        ) : actions.length === 0 ? (
          <div style={{ padding: 30, color: '#85919a', fontSize: 11 }}>没有可用动作。请等待 Connector 同步或激活资源。</div>
        ) : (
          <form onSubmit={submit}>
            <div className="form-grid">
              <fieldset className="action-picker">
                <legend>选择动作</legend>
                {visibleActions.map((item) => (
                  <label
                    key={item.id}
                    className={`${actionId === item.name ? 'selected' : ''} ${item.available === false ? 'disabled' : ''}`}
                    title={
                      item.available === false
                        ? `${item.availability_reason || '当前执行通道不支持'}${actionNeedsUpgrade(item) && item.required_capability ? `；需要 ${item.required_capability}` : ''}`
                        : mapRiskHint(item.risk_level)
                    }
                  >
                    <input
                      type="radio"
                      name="action"
                      value={item.name}
                      checked={actionId === item.name}
                      disabled={item.available === false}
                      onChange={() => chooseAction(item.name)}
                    />
                    <span>
                      <strong>{mapActionName(item.name)}</strong>
                      <small>
                        {item.available === false
                          ? `${item.availability_reason || '不可用'}${item.connector_version ? `（当前 ${item.connector_version}）` : ''}`
                          : item.description || mapRiskHint(item.risk_level)}
                      </small>
                      {item.available !== false && item.description && <small>{mapRiskHint(item.risk_level)}</small>}
                      {actionNeedsUpgrade(item) && item.required_capability && <small>所需能力：{item.required_capability}</small>}
                    </span>
                    <Badge
                      tone={
                        item.available === false
                          ? 'neutral'
                          : item.risk_level === 'high' || item.risk_level === 'critical'
                            ? 'warning'
                            : 'info'
                      }
                    >
                      {item.available === false ? (actionNeedsUpgrade(item) ? '需升级' : '不可用') : mapRiskLevel(item.risk_level)}
                    </Badge>
                  </label>
                ))}
              </fieldset>
              {noAvailableAction && (
                <div className="form-error" role="alert">
                  当前资源没有可执行动作。请根据动作提示恢复执行通道、启用 Integration 或升级 Connector。
                </div>
              )}
              {noExecutionTarget && (
                <div className="form-error" role="alert">
                  没有可用执行通道。请检查资源是否为 ACTIVE、Connector 心跳是否正常，以及关联 Integration 是否已启用。
                </div>
              )}
              {controlPlaneAction ? (
                <>
                  <div className="onboarding-hint">
                    <ShieldCheck size={17} />
                    <span>
                      该动作由 <strong>Warden 控制面</strong>执行，不经过 Connector，不依赖心跳。
                    </span>
                  </div>
                  <label>
                    <span>{resourceTypeLabel(selectedAction?.resource_type)}</span>
                    <div className="select-wrap full">
                      <select
                        value={resourceId}
                        disabled={typeLoading || targetResources.length === 0}
                        onChange={(e) => setResourceId(e.target.value)}
                      >
                        {targetResources.map((item) => (
                          <option key={item.id} value={item.id}>
                            {item.name}
                          </option>
                        ))}
                      </select>
                      <ChevronDown size={15} />
                    </div>
                  </label>
                </>
              ) : jenkinsContextTargetLocked ? (
                <div className="onboarding-hint">
                  <ShieldCheck size={17} />
                  <span>
                    目标已固定为{' '}
                    <strong>
                      {resourceTypeLabel(resource?.resource_type)} · {resource?.name}
                    </strong>
                    ，通过 {connectorName(resource?.connector_id ?? '')} 执行；不能切换到其他 Jenkins 资源。
                  </span>
                </div>
              ) : (
                <>
                  {actionConnectors.length === 1 ? (
                    <div className="onboarding-hint">
                      <ShieldCheck size={17} />
                      <span>
                        将通过 <strong>{actionConnectors[0].name}</strong> 的执行代理执行 · 在线 · 推荐
                      </span>
                    </div>
                  ) : (
                    <label>
                      <span>执行代理</span>
                      <div className="select-wrap full">
                        <select value={connectorId} disabled={noExecutionTarget} onChange={(e) => chooseConnector(e.target.value)}>
                          {noExecutionTarget && <option value="">暂无可用执行代理</option>}
                          {actionConnectors.map((item, index) => (
                            <option key={item.id} value={item.id}>
                              {item.name} · {index === 0 ? '主通道 · 在线 · 推荐' : '备用通道 · 在线'}
                            </option>
                          ))}
                        </select>
                        <ChevronDown size={15} />
                      </div>
                    </label>
                  )}
                  <label>
                    <span>范围</span>
                    <div className="select-wrap full">
                      <select value={namespace} onChange={(e) => chooseNamespace(e.target.value)}>
                        {namespaces.map((item) => (
                          <option key={item || '__host'} value={item}>
                            {item || '主机'}
                          </option>
                        ))}
                      </select>
                      <ChevronDown size={15} />
                    </div>
                  </label>
                  <label>
                    <span>{resourceTypeLabel(selectedAction?.resource_type)}</span>
                    <div className="select-wrap full">
                      <select
                        value={resourceId}
                        disabled={typeLoading || targetResources.length === 0}
                        onChange={(e) => setResourceId(e.target.value)}
                      >
                        {typeLoading ? (
                          <option>加载中…</option>
                        ) : targetResources.length === 0 ? (
                          <option value="">当前范围暂无可用资源</option>
                        ) : (
                          targetResources.map((item) => (
                            <option key={item.id} value={item.id}>
                              {item.resource_type === 'huawei_cloud_account'
                                ? cloudRegionLabel(String(item.metadata.region ?? item.name))
                                : item.name}
                            </option>
                          ))
                        )}
                      </select>
                      <ChevronDown size={15} />
                    </div>
                  </label>
                </>
              )}
              {resourceProjectIds.length === 0 ? (
                <div className="form-error" role="alert">
                  该资源暂未开放给任何项目，不能执行。
                </div>
              ) : resourceProjectIds.length === 1 ? (
                <div className="acting-project-context">
                  <FolderKanban size={17} />
                  <span>当前执行项目</span>
                  <strong>
                    {resource?.project_names?.[0] ?? projects.find((project) => project.id === actingProjectId)?.name ?? '已分配项目'}
                  </strong>
                </div>
              ) : (
                <label>
                  <span>当前执行项目</span>
                  <div className="select-wrap full">
                    <select value={actingProjectId} onChange={(event) => setActingProjectId(event.target.value)} required>
                      <option value="">请选择本次执行代表的项目</option>
                      {resourceProjectIds.map((projectId) => (
                        <option key={projectId} value={projectId}>
                          {projects.find((project) => project.id === projectId)?.name ?? projectId}
                        </option>
                      ))}
                    </select>
                    <ChevronDown size={15} />
                  </div>
                </label>
              )}
              {GRAFANA_UPDATE_READ_ACTIONS[actionName] && (
                <MutationPrefillState
                  loading={prefillLoading}
                  error={prefillError}
                  readyText={`目标 UID 已固定为 ${String(resource?.metadata?.uid ?? resource?.external_id?.split('/').pop() ?? '当前资源')}；密码、Token 与 secureJsonData 不会进入表单。`}
                  onRetry={() => setPrefillNonce((value) => value + 1)}
                />
              )}
              {KUBERNETES_MUTATION_READ_ACTIONS[actionName] && (
                <MutationPrefillState
                  loading={prefillLoading}
                  error={prefillError}
                  readyText="已读取目标最新 generation；审批或提交后若资源 Spec 被其他客户端修改，Connector 将拒绝陈旧请求。"
                  onRetry={() => setPrefillNonce((value) => value + 1)}
                />
              )}
              {(actionName === 'kubernetes.resource.update' || actionName === 'kubernetes.resource.delete') && (
                <MutationPrefillState
                  loading={prefillLoading}
                  error={prefillError}
                  readyText={`已固定当前 resourceVersion ${String(parameters.expectedResourceVersion ?? '')}；对象发生并发变更时将拒绝执行。`}
                  onRetry={() => setPrefillNonce((value) => value + 1)}
                />
              )}
              {genericKubernetesPreviewable && (
                <GenericKubernetesEditor
                  actionName={actionName}
                  parameters={parameters}
                  manifestText={manifestText}
                  manifestError={manifestError}
                  preview={genericPreview}
                  previewLoading={previewLoading}
                  previewError={previewError}
                  onManifestChange={updateManifest}
                  onKindChange={chooseGenericKind}
                  onPreview={runGenericPreview}
                />
              )}
              {isCloudLogAction(actionName) && <LogTimePresets actionName={actionName} setParameters={setParameters} />}
              {normalizedActionName === 'database.table.rows.query' && actingProjectId && (
                <DatabaseTableQueryBuilder
                  resource={resource ?? null}
                  parameters={parameters}
                  setParameters={setParameters}
                  startExecution={createScopedExecution}
                />
              )}
              {normalizedActionName === 'database.query.execute' && actingProjectId && (
                <DatabaseQueryAstBuilder
                  resource={resource ?? null}
                  parameters={parameters}
                  setParameters={setParameters}
                  previewRequest={databasePreviewRequest}
                  onPreviewLoadingChange={setDatabasePreviewLoading}
                  onPreviewVerdictChange={setDatabasePreviewVerdict}
                  startExecution={createScopedExecution}
                  actingProjectId={actingProjectId}
                />
              )}
              {normalizedActionName === 'database.query_template.execute' && (
                <DatabaseQueryTemplatePicker resource={resource ?? null} parameters={parameters} setParameters={setParameters} />
              )}
              <ActionParameterFields
                entries={editableParameters}
                parameters={parameters}
                setParameters={setParameters}
                requiredParameters={requiredParameters}
                resourceContainers={resourceContainers}
                selectedContainer={selectedContainer}
                actionName={actionName}
                projects={projects}
              />
              {networkInterfaceMutation && (
                <NetworkInterfacePreviewPanel
                  preview={networkPreviewCurrent ? networkPreview : null}
                  previewLoading={previewLoading}
                  previewError={previewError}
                  stale={Boolean(networkPreview && !networkPreviewCurrent)}
                  onPreview={runNetworkPreview}
                />
              )}
              {(needsApproval || (selectedAction?.risk_level === 'high' && !approvalExemptChange)) && (
                <label>
                  <span>业务原因</span>
                  <textarea value={reason} onChange={(e) => setReason(e.target.value)} rows={2} required />
                </label>
              )}
            </div>
            <div className={`decision-preview ${needsApproval ? 'needs-approval' : 'auto-allow'}`}>
              <div>
                {needsApproval ? <ShieldAlert size={20} /> : <ShieldCheck size={20} />}
                <div>
                  <strong>{needsApproval ? '预计需要租户审批' : '预计策略自动允许'}</strong>
                  <span>
                    {databaseCostNeedsApproval
                      ? '查询预检触发成本门禁 · 审批绑定 previewHash · 单次 Grant'
                      : networkInterfaceMutation
                        ? '固定接口动作 · 审批绑定 previewHash · 写后复核 · 禁止自动重试'
                        : needsApproval
                          ? '结构化变更 · 高风险 · 单次 Grant · 完整审计'
                          : approvalExemptChange
                            ? 'Jenkins Pipeline 创建 · 单次 Grant · 完整审计'
                            : kubernetesChange
                              ? '测试/开发环境结构化变更 · generation 并发保护 · 完整审计'
                              : '只读动作 · 最小权限 Grant'}
                  </span>
                </div>
              </div>
              <Badge tone={needsApproval ? 'warning' : 'success'}>{needsApproval ? '审批后执行' : '直接执行'}</Badge>
            </div>
            <div className="credential-note">
              <KeyRound size={17} />
              <span>
                {controlPlaneAction
                  ? '凭据密文仅由 Warden 控制面处理，不会返回 Agent。'
                  : '执行凭据仅在 Connector 内存中使用，不会返回 Agent。'}
              </span>
            </div>
            <div className="drawer-actions">
              <button type="button" className="secondary-button" onClick={onClose}>
                取消
              </button>
              <button
                className="primary-button"
                type="submit"
                disabled={
                  submitting ||
                  prefillLoading ||
                  previewLoading ||
                  databasePreviewLoading ||
                  Boolean(prefillError) ||
                  !actionId ||
                  !resource ||
                  !actingProjectId ||
                  noExecutionTarget ||
                  deleteConfirmationMismatch ||
                  ecsDeleteConfirmationMismatch ||
                  genericDeleteConfirmationMismatch ||
                  databaseExecuteMissingFields ||
                  (genericKubernetesPreviewable && !genericPreviewCurrent) ||
                  (networkInterfaceMutation && !networkPreviewCurrent)
                }
              >
                {submitting || prefillLoading || previewLoading || databasePreviewLoading ? (
                  <Loader2 size={16} className="spin" />
                ) : databaseExecuteNeedsPreview ? (
                  <ShieldCheck size={16} />
                ) : (
                  <Play size={16} fill="currentColor" />
                )}
                {prefillLoading
                  ? '读取当前配置'
                  : previewLoading && networkInterfaceMutation
                    ? '正在预检接口变更'
                    : databasePreviewLoading
                      ? '正在生成查询预览'
                      : !actingProjectId
                        ? '请选择执行项目'
                        : databaseExecuteMissingFields
                          ? '请先选择返回字段'
                          : databaseExecuteNeedsPreview
                            ? '生成查询预览'
                            : (genericKubernetesPreviewable && !genericPreviewCurrent) ||
                                (networkInterfaceMutation && !networkPreviewCurrent)
                              ? '请先完成预检'
                              : '提交策略决策'}
              </button>
            </div>
            {resourceView && (
              <div style={{ marginTop: 14, fontSize: 10, color: '#7a8992' }}>
                资源状态：{resourceView.status} · 服务目标：{resourceView.connector}
              </div>
            )}
          </form>
        )}
      </aside>
    </>
  )
}
