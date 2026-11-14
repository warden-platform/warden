// API client for pam-control. All Web UI pages should call these functions
// instead of reading mock data, so the UI stays in sync with the backend.

const API_BASE = import.meta.env.VITE_API_BASE_URL ?? '/api/v1'

// Admin authentication is held in an HttpOnly cookie; these compatibility
// helpers intentionally keep JWT material out of browser storage.
export function getToken() { return '' }
export function setToken(_token: string) {}
export function clearToken() {}

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  let payload: BodyInit | undefined
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  const resp = await fetch(`${API_BASE}${path}`, { method, headers, body: payload, credentials: 'include' })
  // Parse the server error before applying the generic session-expired
  // handling. Login deliberately returns 401/INVALID_CREDENTIALS and callers
  // need that distinction to show the correct message.
  const text = resp.status === 204 ? '' : await resp.text()
  let data: unknown = null
  if (text) {
    try { data = JSON.parse(text) } catch { data = text }
  }
  if (resp.status === 401) {
    const errObj = (typeof data === 'object' && data && 'error' in (data as object)) ? data as { error?: string; message?: string } : null
    if (errObj?.error === 'INVALID_CREDENTIALS') {
      throw new ApiError(401, errObj.error, errObj.message ?? '用户名或密码错误')
    }
    // Token missing/expired: clear and bounce to login. Hard redirect avoids
    // React Router loops when the caller is already inside a guarded route.
    clearToken()
    if (window.location.pathname !== '/login') {
      window.location.href = '/login'
    }
    throw new ApiError(401, 'UNAUTHORIZED', '登录已过期，请重新登录')
  }
  if (resp.status === 204) return undefined as T
  if (!resp.ok) {
    const errObj = (typeof data === 'object' && data && 'error' in (data as object)) ? data as { error?: string; message?: string } : null
    throw new ApiError(resp.status, errObj?.error ?? 'HTTP_ERROR', errObj?.message ?? `请求失败 (${resp.status})`)
  }
  return data as T
}

async function coolAppRequest<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  let payload: BodyInit | undefined
  if (body !== undefined) { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(body) }
  const resp = await fetch(`${API_BASE}${path}`, { method, headers, body: payload, credentials: 'include' })
  const text = resp.status === 204 ? '' : await resp.text()
  let data: unknown = null
  if (text) { try { data = JSON.parse(text) } catch { data = text } }
  if (resp.status === 204) return undefined as T
  if (!resp.ok) {
    const errObj = (typeof data === 'object' && data) ? data as { error?: string; message?: string; user_id?: string } : null
    const error = new ApiError(resp.status, errObj?.error ?? 'HTTP_ERROR', errObj?.message ?? `请求失败 (${resp.status})`) as ApiError & { userId?: string }
    error.userId = errObj?.user_id
    throw error
  }
  return data as T
}

// --- Types (API response shapes) ---

export type ApiResource = {
  id: string
  resource_type: string
  external_id: string
  name: string
  namespace?: string
  connector_id: string
  management_type?: 'connector' | 'control_plane'
  integration_id?: string
  project_id?: string
  project_name?: string
  project_source?: 'RESOURCE' | 'INTEGRATION_RULE' | 'CLOUD_TAG' | 'NAMESPACE' | 'INTEGRATION' | 'CONNECTOR' | 'DEFAULT'
  project_scope_mode: 'INHERITED' | 'SELECTED_PROJECTS' | 'ALL_PROJECTS' | 'UNASSIGNED'
  project_ids: string[]
  project_names: string[]
  owner_team?: string
  environment: string
  environment_source?: 'RESOURCE' | 'DATABASE_SCHEMA' | 'DATABASE_INSTANCE' | 'INTEGRATION' | 'CONNECTOR'
  status: 'ACTIVE' | 'DISCOVERED' | 'ORPHANED' | 'DISABLED'
  labels: Record<string, unknown>
  annotations: Record<string, unknown>
  metadata: Record<string, unknown>
  created_at: string
  updated_at: string
  action_access?: 'READ_ONLY' | 'CHANGE' | 'RESTRICTED_CHANGE'
  action_upgrade_required?: boolean
  available_action_count?: number
}

export type ApiAction = {
  id: string
  name: string
  version: number
  display_name?: string
  description?: string
  resource_type: string
  risk_level: 'low' | 'medium' | 'high' | 'critical'
  execution_mode: 'SYNCHRONOUS' | 'ASYNCHRONOUS'
  executor_type?: 'connector' | 'control_plane'
  parameter_schema: Record<string, unknown>
  timeout_seconds: number
  available?: boolean
  availability_code?: 'RESOURCE_NOT_ACTIVE' | 'CONNECTOR_NOT_ACTIVE' | 'CONNECTOR_HEARTBEAT_TIMEOUT' | 'INTEGRATION_NOT_ACTIVE' | 'CONNECTOR_UPGRADE_REQUIRED'
  availability_reason?: string
  required_capability?: string
  connector_version?: string
}

export type ApiConnector = {
  id: string
  name: string
  connector_type: string
  status: 'DRAFT' | 'TESTING' | 'ACTIVE' | 'DEGRADED' | 'QUARANTINED' | 'DISABLED'
  environment: string
  version: string
  capabilities: string[]
  config: Record<string, unknown>
  project_id?: string
  last_heartbeat?: string
  created_at: string
  updated_at: string
  deletion_status?: '' | 'READY' | 'DELETE_REQUESTED' | 'DELETED'
  available_namespaces?: string[]
  available_services?: string[]
  available_containers?: string[]
}

// ACTIVE is a lifecycle state. A connector is only online when its heartbeat
// is recent; this keeps stale ACTIVE records from being shown as healthy.
export function isConnectorHealthy(connector: ApiConnector, now = Date.now()): boolean {
  if (connector.status !== 'ACTIVE' || !connector.last_heartbeat) return false
  const heartbeat = Date.parse(connector.last_heartbeat)
  if (!Number.isFinite(heartbeat)) return false
  const ageSeconds = (now - heartbeat) / 1000
  return ageSeconds >= 0 && ageSeconds <= 90
}

export function connectorHealthLabel(connector: ApiConnector, now = Date.now()): string {
  if (isConnectorHealthy(connector, now)) return '健康'
  if (connector.status === 'ACTIVE') return '离线'
  return mapConnectorStatusForHealth(connector.status)
}

function mapConnectorStatusForHealth(status: ApiConnector['status']): string {
  switch (status) {
    case 'DRAFT': return '草稿'
    case 'TESTING': return '测试中'
    case 'DEGRADED': return '降级'
    case 'QUARANTINED': return '隔离'
    case 'DISABLED': return '已停用'
    default: return status
  }
}

export type ApiExecution = {
  id: string
  status: string
  actingProjectId?: string
  principalId?: string
  principalName?: string
  principalKind?: string
  credentialId?: string
  credentialName?: string
  actionId: string
  actionName?: string
  actionDisplayName?: string
  riskLevel?: 'low' | 'medium' | 'high' | 'critical'
  resourceId: string
  resourceName?: string
  resourceType?: string
  traceId: string
  errorCode?: string
  errorMessage?: string
  result?: Record<string, unknown>
  createdAt: string
  updatedAt: string
}

export type ApiAccessRequest = {
  id: string
  execution_id: string
  principal_id: string
  principal_name?: string
  resource_id: string
  resource_name?: string
  resource_type?: string
  resource_status?: string
  action_id: string
  acting_project_id?: string
  action_name?: string
  action_display_name?: string
  risk_level?: 'low' | 'medium' | 'high' | 'critical'
  approval_context?: Record<string, unknown>
  parameters: Record<string, unknown>
  parameter_hash: string
  reason?: string
  status: 'PENDING' | 'APPROVED' | 'REJECTED' | 'EXPIRED' | 'CANCELLED'
  expires_at?: string
  created_at: string
  updated_at: string
}

export type ApiAuditEvent = {
  id: string
  action: string
  actor_id?: string
  actor_type?: string
  acting_project_id?: string
  credential_id?: string
  resource_id?: string
  execution_id?: string
  connector_id?: string
  trace_id?: string
  target_id?: string
  summary?: string
  metadata: Record<string, unknown>
  created_at: string
}

export type ApiPrincipal = {
  id: string
  kind: 'user' | 'agent_type' | 'agent_instance' | 'workload'
  /** Stable internal identifier. Federated users keep a `federated:<hash>` name. */
  name: string
  status: 'ACTIVE' | 'DISABLED'
  tenant_id: string
  metadata: Record<string, unknown>
  last_login_at?: string
  created_at: string
  updated_at: string
}

export type ApiPrincipalKey = {
  id: string
  principal_id: string
  name: string
  key_prefix: string
  status: 'ACTIVE' | 'REVOKED'
  purpose: 'AGENT' | 'REGISTRATION'
  audience?: string
  scopes: string[]
  expires_at?: string
  last_used_at?: string
  last_used_ip?: string
  created_by?: string
  created_at: string
  revoked_at?: string
  revoked_by?: string
  rotation_parent_id?: string
}

export type CreatePrincipalRequest = {
  name: string
  kind?: 'agent_instance' | 'workload'
  metadata?: Record<string, unknown>
}

export type CreateAPIKeyRequest = {
  name?: string
  scopes?: string[]
  expiresAt?: string
}

export type CreateAPIKeyResponse = {
  credential: ApiPrincipalKey
  apiKey: string
  warning: string
  replaces?: string
  apiEndpoint?: string
}

export type CreateRegistrationAPIKeyRequest = {
  name?: string
  audience?: string
  expiresAt?: string
}

export type OnboardRequest = {
  name: string
  environment: 'development' | 'testing' | 'production'
  connectorNamespace: string
  allowedNamespaces: string[]
  allowedImagePrefixes: string[]
  controlURL: string
  tokenAudience?: string
  executionServiceAccount?: string
}

export type OnboardResponse = {
  connectorId: string
  manifest: string
  secretName: string
  instructions: string
}

export type ConfirmPairingRequest = {
  code: string
  name: string
  environment: 'development' | 'testing' | 'production'
  projectId?: string
  allowedNamespaces: string[]
  allowedImagePrefixes: string[]
}

export type ConfirmPairingResponse = {
  pairingId: string
  connectorId: string
  status: 'CONFIRMED'
}

export type PairingOptionsResponse = {
  availableNamespaces: string[]
  expiresAt: string
}

export type LinuxPairingOptionsResponse = {
  availableServices: string[]
  availableContainers: string[]
  machine: Record<string, unknown>
  expiresAt: string
}
export type HostPairingOptionsResponse = { availableCommands: string[]; machine: Record<string, unknown>; platform: 'windows' | 'macos'; expiresAt: string }

export type ConnectorCheckResponse = {
  connectorId: string
  healthy: boolean
  status: ApiConnector['status']
  lastHeartbeat?: string
  heartbeatAgeSeconds: number
  checkedAt: string
}

export type InstallConfig = {
  releaseVersion: string
  publicControlUrl: string
  operatorChartReference: string
  operatorImageRepository: string
  operatorImageTag: string
  connectorImage: string
}

export type Integration = {
  id: string
  connector_id: string
  protocol: string
  name: string
  endpoint: string
  username?: string
  credential_ref: string
  project_id?: string
  environment?: '' | 'development' | 'testing' | 'production'
  project_ids: string[]
  project_rules: IntegrationProjectRule[]
  scope: Record<string, unknown>
  status: 'ACTIVE' | 'DISABLED' | 'FAILED'
  last_probe_at?: string
  last_error?: string
  created_at: string
  updated_at: string
}

export type IntegrationProjectRule = {
  id?: string
  integration_id?: string
  project_id: string
  matcher_type: 'JENKINS_PATH_PREFIX'
  pattern: string
  priority: number
}

export type IntegrationProjectScope = {
  default_project_id: string
  environment: '' | 'development' | 'testing' | 'production'
  project_ids: string[]
  rules: IntegrationProjectRule[]
}

export type CreateIntegrationRequest = {
  protocol: string
  name: string
  endpoint: string
  username: string
  credentialRef: string
  projectId?: string
  environment?: '' | 'development' | 'testing' | 'production'
  // Broad access is always explicit; it can be narrowed after discovery.
  scope: {
    allowAll?: boolean
    allowedJobs?: string[]
    allowedDashboards?: string[]
    allowedSchemas?: string[]
    allowedServices?: string[]
    allowedProjects?: string[]
    regionPolicy?: { mode: 'single' | 'all' | 'allowlist'; regions: string[]; deniedRegions: string[] }
    deviceRole?: string
    deviceName?: string
    hostKeySHA256?: string
    legacyRsa?: boolean
    allowInterfaceChanges?: boolean
    protectedInterfaces?: string[]
  }
}

export type CreateExecutionRequest = {
  taskId?: string
  resourceId: string
  actingProjectId?: string
  action: string
  actionVersion?: number
  parameters: Record<string, unknown>
  reason?: string
}

export type ApiQueryTemplate = {
  id: string
  integration_id: string
  schema_resource_id: string
  name: string
  version: number
  status: 'DRAFT' | 'PUBLISHED' | 'DEPRECATED' | 'DISABLED'
  parameter_schema?: Record<string, unknown>
  resource_bindings?: string[]
  max_rows: number
  timeout_seconds: number
  sensitivity: string
  owner?: string
  published_at?: string
  template_hash: string
  created_at: string
  updated_at: string
}

export type ApiTemplatePublishRequest = {
  id: string
  template_id: string
  requested_by: string
  status: 'PENDING' | 'APPROVED' | 'REJECTED'
  approved_by?: string
  rejected_reason?: string
  requested_at: string
  decided_at?: string
}

// --- API functions ---

const resourcesInFlight = new Map<string, Promise<ApiResource[]>>()

// React StrictMode and concurrent page mounts may ask for the catalog at the
// same time. Share the in-flight response so a navigation never downloads the
// multi-megabyte catalog twice.
export const listResources = (options: { domain?: string; topology?: boolean; type?: string; excludeType?: string; namespace?: string; integrationId?: string; projectId?: string; limit?: number } = {}) => {
  const params = new URLSearchParams()
  if (options.domain) params.set('domain', options.domain)
  if (options.topology) params.set('topology', 'true')
  if (options.type) params.set('type', options.type)
  if (options.excludeType) params.set('excludeType', options.excludeType)
  if (options.namespace) params.set('namespace', options.namespace)
  if (options.integrationId) params.set('integrationId', options.integrationId)
  if (options.projectId) params.set('projectId', options.projectId)
  if (options.limit) params.set('limit', String(options.limit))
  const path = `/resources${params.size ? `?${params.toString()}` : ''}`
  const current = resourcesInFlight.get(path)
  if (current) return current
  const pending = request<ApiResource[]>('GET', path)
  resourcesInFlight.set(path, pending)
  pending.finally(() => resourcesInFlight.delete(path)).catch(() => undefined)
  return pending
}
export const setResourceProject = (resourceId: string, projectId: string, reason = '') =>
  request<{ resource_id: string; project_id: string; project_name: string; project_source: 'RESOURCE' }>('PUT', `/resources/${encodeURIComponent(resourceId)}/project`, { project_id: projectId, reason })
export const clearResourceProject = (resourceId: string) =>
  request<void>('DELETE', `/resources/${encodeURIComponent(resourceId)}/project`)
export const setResourceEnvironment = (resourceId: string, environment: 'development' | 'testing' | 'production', reason = '') =>
  request<{ resource_id: string; environment: string; environment_source: 'RESOURCE' }>('PUT', `/resources/${encodeURIComponent(resourceId)}/environment`, { environment, reason })
export const clearResourceEnvironment = (resourceId: string) =>
  request<void>('DELETE', `/resources/${encodeURIComponent(resourceId)}/environment`)
export type ApiResourceStats = {
  total: number
  current: number
  active: number
  abnormal: number
  history: number
  byType: Record<string, number>
  byStatus: Record<string, number>
  catalogByDomainStatus: Record<string, Record<string, number>>
  catalogByProjectDomainStatus: Record<string, Record<string, Record<string, number>>>
}
export const getResourceStats = () => request<ApiResourceStats>('GET', '/resources/stats')
export const listAvailableResources = (type?: string) => request<ApiResource[]>('GET', `/resources?availableForExecution=true${type ? `&type=${encodeURIComponent(type)}` : ''}`)
export const listAvailableActions = () => request<ApiAction[]>('GET', '/actions')
export const listResourceActions = (id: string, includeUnavailable = false) => request<ApiAction[]>('GET', `/resources/${id}/actions${includeUnavailable ? '?includeUnavailable=true' : ''}`)
export const listConnectors = () => request<ApiConnector[]>('GET', '/connectors')
export const listQueryTemplates = (integrationId: string, status = 'PUBLISHED') => {
  const params = new URLSearchParams({ integrationId, status })
  return request<ApiQueryTemplate[]>('GET', `/database/query-templates?${params.toString()}`)
}
export const importQueryTemplate = (body: { integrationId: string; schemaResourceId: string; name: string; sql: string; owner?: string }) =>
  request<ApiQueryTemplate>('POST', '/database/query-templates', body)
export const submitQueryTemplatePublish = (templateId: string) =>
  request<ApiTemplatePublishRequest>('POST', `/database/query-templates/${encodeURIComponent(templateId)}/publish`)
export const listTemplatePublishRequests = (status = '') =>
  request<ApiTemplatePublishRequest[]>('GET', `/database/query-templates/publish-requests${status ? `?status=${encodeURIComponent(status)}` : ''}`)
export const approveTemplatePublish = (templateId: string, requestId: string) =>
  request<{ id: string; status: string }>('POST', `/database/query-templates/${encodeURIComponent(templateId)}/publish-requests/${encodeURIComponent(requestId)}/approve`)
export const rejectTemplatePublish = (templateId: string, requestId: string, reason: string) =>
  request<{ id: string; status: string }>('POST', `/database/query-templates/${encodeURIComponent(templateId)}/publish-requests/${encodeURIComponent(requestId)}/reject`, { reason })
export const getInstallConfig = () => request<InstallConfig>('GET', '/install-config')
export type NetworkConnectorProvision = { id: string; name: string; connectorType: 'network'; status: 'DRAFT'; taskSigningKey: string; credentialWarning: string }
export const createNetworkConnector = (body: { name: string; environment: 'development' | 'testing' | 'production'; projectId?: string }) =>
  request<NetworkConnectorProvision>('POST', '/connectors/network', body)
export const deleteConnector = (id: string) => request<{ id: string; status: 'DISABLED' }>('DELETE', `/connectors/${id}`)
export const archiveConnector = (id: string) => request<{ id: string; status: 'ARCHIVED' }>('POST', `/connectors/${id}/archive`)
export const forceArchiveConnector = (id: string) => request<{ id: string; status: 'ARCHIVED' }>('POST', `/connectors/${id}/force-archive`, { confirmTargetRemoved: true })
export const checkConnector = (id: string) => request<ConnectorCheckResponse>('POST', `/connectors/${id}/check`)
export const updateConnectorNamespaces = (id: string, namespaces: string[]) =>
  request<{ id: string; namespaces: string[]; status: 'RECONCILING' }>('PUT', `/connectors/${id}/namespaces`, { namespaces })
export const updateConnectorServices = (id: string, services: string[], containers: string[]) =>
  request<{ id: string; services: string[]; containers: string[]; status: 'RECONCILING' }>('PUT', `/connectors/${id}/services`, { services, containers })
// Legacy rows may carry mixed-case protocol values; normalize to the lowercase
// canonical form so exact-match filtering in the UI stays consistent.
const normalizeIntegration = (ig: Integration): Integration => ({ ...ig, protocol: ig.protocol.toLowerCase(), project_ids: ig.project_ids ?? [], project_rules: ig.project_rules ?? [] })
export const listIntegrations = () => request<Integration[]>('GET', '/integrations').then((items) => (items ?? []).map(normalizeIntegration))
export const listConnectorIntegrations = (id: string) => request<Integration[]>('GET', `/connectors/${id}/integrations`).then((items) => (items ?? []).map(normalizeIntegration))
export const createIntegration = (connectorId: string, req: CreateIntegrationRequest) =>
  request<Integration>('POST', `/connectors/${connectorId}/integrations`, req).then(normalizeIntegration)
export const updateIntegration = (connectorId: string, integrationId: string, req: CreateIntegrationRequest & { status?: string }) =>
  request<Integration>('PUT', `/connectors/${connectorId}/integrations/${integrationId}`, req).then(normalizeIntegration)
export const deleteIntegration = (connectorId: string, integrationId: string) =>
  request<{ id: string; status: 'DELETED' }>('DELETE', `/connectors/${connectorId}/integrations/${integrationId}`)
export const getIntegrationProjectScope = (integrationId: string) =>
  request<IntegrationProjectScope>('GET', `/integrations/${encodeURIComponent(integrationId)}/project-scope`)
export const updateIntegrationProjectScope = (integrationId: string, body: { defaultProjectId: string; environment: string; projectIds: string[]; rules: IntegrationProjectRule[] }) =>
  request<Integration>('PUT', `/integrations/${encodeURIComponent(integrationId)}/project-scope`, body).then(normalizeIntegration)
export const getResourceConsumerProjects = (resourceId: string) =>
  request<{ project_ids: string[] }>('GET', `/resources/${encodeURIComponent(resourceId)}/consumer-projects`)
export const updateResourceConsumerProjects = (resourceId: string, projectIds: string[]) =>
  request<{ project_ids: string[] }>('PUT', `/resources/${encodeURIComponent(resourceId)}/consumer-projects`, { projectIds })
export type ResourceProjectScope = {
  mode: ApiResource['project_scope_mode']
  project_ids: string[]
  project_names: string[]
}
export const getResourceProjectScope = (resourceId: string) =>
  request<ResourceProjectScope>('GET', `/resources/${encodeURIComponent(resourceId)}/project-scope`)
export const updateResourceProjectScope = (resourceId: string, body: { mode: ResourceProjectScope['mode']; projectIds: string[]; reason?: string }) =>
  request<ResourceProjectScope>('PUT', `/resources/${encodeURIComponent(resourceId)}/project-scope`, body)
export const listExecutions = (status?: string) =>
  request<ApiExecution[]>('GET', `/executions${status ? `?status=${encodeURIComponent(status)}` : ''}`)
export const listAccessRequests = (status?: string) =>
  request<ApiAccessRequest[]>('GET', `/access-requests${status ? `?status=${encodeURIComponent(status)}` : ''}`)
export type AuditEventFilters = {
  action?: string; category?: string; executionId?: string; traceId?: string; connectorId?: string; q?: string
  externalSource?: string; workspaceId?: string; externalProjectId?: string; agentId?: string; taskId?: string; issueId?: string
  originatorUserId?: string; delegationTokenId?: string; onBehalfOfPrincipalId?: string; accountablePrincipalId?: string
}
export const listAuditEvents = (filters: AuditEventFilters = {}, limit = 200, offset = 0) => {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  for (const [key, value] of Object.entries(filters)) if (value) params.set(key, value)
  return request<ApiAuditEvent[]>('GET', `/audit-events?${params.toString()}`)
}
export const approveRequest = (id: string) => request<{ id: string; status: string }>('POST', `/access-requests/${id}/approve`)
export const rejectRequest = (id: string) => request<{ id: string; status: string }>('POST', `/access-requests/${id}/reject`)
export const createExecution = (req: CreateExecutionRequest) => request<ApiExecution>('POST', '/executions', req)
export const getExecution = (id: string) => request<ApiExecution>('GET', `/executions/${id}`)
export const listPrincipals = (kind = 'agent_instance') =>
  request<ApiPrincipal[]>('GET', `/principals${kind ? `?kind=${encodeURIComponent(kind)}` : ''}`)
export const createPrincipal = (req: CreatePrincipalRequest) => request<ApiPrincipal>('POST', '/principals', req)
export const deletePrincipal = (id: string) =>
  request<{ id: string; status: 'DISABLED' }>('DELETE', `/principals/${id}`)
export const listPrincipalAPIKeys = (principalId: string) =>
  request<ApiPrincipalKey[]>('GET', `/principals/${principalId}/api-keys`)
export const createPrincipalAPIKey = (principalId: string, req: CreateAPIKeyRequest) =>
  request<CreateAPIKeyResponse>('POST', `/principals/${principalId}/api-keys`, req)
export const createRegistrationAPIKey = (principalId: string, req: CreateRegistrationAPIKeyRequest) =>
  request<CreateAPIKeyResponse>('POST', `/principals/${principalId}/registration-api-keys`, req)
export const revokePrincipalAPIKey = (credentialId: string) =>
  request<{ id: string; status: 'REVOKED' }>('POST', `/api-keys/${credentialId}/revoke`)
export const rotatePrincipalAPIKey = (credentialId: string) =>
  request<CreateAPIKeyResponse>('POST', `/api-keys/${credentialId}/rotate`)
export type ApiExternalBinding = {
  id: string
  tenantId?: string
  source: string
  workspaceId: string
  externalProjectId?: string
  identityType: 'AGENT' | 'USER'
  externalId: string
  principalId: string
  projectId?: string
  status: 'ACTIVE' | 'REVOKED'
  createdBy?: string
  createdAt: string
  updatedAt: string
  revokedAt?: string
}
export const listExternalBindings = () => request<ApiExternalBinding[]>('GET', '/external-bindings')
export const createExternalBinding = (body: {
  source: string; workspaceId: string; externalProjectId?: string; identityType: ApiExternalBinding['identityType']; externalId: string; principalId: string; projectId?: string
}) => request<ApiExternalBinding>('POST', '/external-bindings', body)
export const revokeExternalBinding = (id: string) => request<{ id: string; status: 'REVOKED' }>('DELETE', `/external-bindings/${encodeURIComponent(id)}`)
export const revokeExternalWorkspace = (body: { source: string; workspaceId: string }) =>
  request<{ source: string; workspaceId: string; revokedBindings: number }>('POST', '/external-bindings/revoke-workspace', body)
export type ApiFederatedTenantBinding = {
  id: string
  tenantId: string
  source: string
  externalEnterpriseId: string
  externalWorkspaceId: string
  status: 'ACTIVE' | 'REVOKED'
  createdBy?: string
  createdAt: string
  updatedAt: string
  revokedAt?: string
}
export type ApiFederatedProjectBinding = {
  id: string
  tenantId: string
  source: string
  externalWorkspaceId: string
  externalProjectId: string
  externalProjectName?: string
  projectId: string
  status: 'ACTIVE' | 'REVOKED'
  createdBy?: string
  createdAt: string
  updatedAt: string
  revokedAt?: string
}
export const listFederatedTenants = () => request<ApiFederatedTenantBinding[]>('GET', '/federated/tenants')
export const bindFederatedTenant = (body: { source: string; externalEnterpriseId: string; externalWorkspaceId: string }) =>
  request<ApiFederatedTenantBinding>('POST', '/federated/tenants', body)
export const listFederatedProjects = () => request<ApiFederatedProjectBinding[]>('GET', '/federated/projects')
export const bindFederatedProject = (body: { source: string; externalWorkspaceId: string; externalProjectId: string; externalProjectName?: string; projectId: string }) =>
  request<ApiFederatedProjectBinding>('POST', '/federated/projects', body)
export const revokeFederatedProject = (id: string) => request<void>('DELETE', `/federated/projects/${encodeURIComponent(id)}`)
export const onboardCluster = (req: OnboardRequest) => request<OnboardResponse>('POST', '/connectors/onboard', req)
export const confirmClusterPairing = (req: ConfirmPairingRequest) =>
  request<ConfirmPairingResponse>('POST', '/cluster-pairings/confirm', req)
export const getClusterPairingOptions = (code: string) =>
  request<PairingOptionsResponse>('POST', '/cluster-pairings/options', { code })
export const getLinuxPairingOptions = (code: string) =>
  request<LinuxPairingOptionsResponse>('POST', '/linux-pairings/options', { code })
export const confirmLinuxPairing = (req: { code: string; name: string; environment: 'development' | 'testing' | 'production'; projectId?: string; allowedServices: string[]; allowedContainers: string[] }) =>
  request<ConfirmPairingResponse>('POST', '/linux-pairings/confirm', req)
export const getHostPairingOptions = (code: string) => request<HostPairingOptionsResponse>('POST', '/host/pairings/options', { code })
export const confirmHostPairing = (req: { code: string; platform: 'windows' | 'macos'; name: string; environment: 'development' | 'testing' | 'production'; projectId?: string; allowedCommands: string[] }) =>
  request<ConfirmPairingResponse>('POST', '/host/pairings/confirm', req)

// --- Projects (resource catalog grouping) ---

export type ApiProject = {
  id: string
  name: string
  description: string
  is_default: boolean
  created_at: string
  updated_at: string
}

export type ApiTenantQuota = {
  tenant_id: string
  max_principals: number
  max_connectors: number
  max_concurrent_tasks: number
  max_requests_per_minute: number
  audit_retention_days: number
  updated_at: string
}

export type ApiTenantUsage = {
  principals: number
  connectors: number
  projects: number
  in_flight_tasks: number
  administrators: number
}

export type ApiTenant = {
  id: string
  slug: string
  name: string
  status: 'ACTIVE' | 'DISABLED'
  settings: Record<string, unknown>
  quota: ApiTenantQuota
  usage: ApiTenantUsage
  created_at: string
  updated_at: string
}

export type TenantQuotaInput = {
  maxPrincipals: number
  maxConnectors: number
  maxConcurrentTasks: number
  maxRequestsPerMinute: number
  auditRetentionDays: number
}

export type BootstrapTenantInput = {
  slug: string
  name: string
  adminName: string
  adminPassword: string
  quota: TenantQuotaInput
}

export type BootstrapTenantResponse = { tenant: Omit<ApiTenant, 'quota' | 'usage'>; administrator: ApiPrincipal }

export const listTenants = () => request<ApiTenant[]>('GET', '/tenants')
export const getTenant = (slug: string) => request<ApiTenant>('GET', `/tenants/${encodeURIComponent(slug)}`)
export const bootstrapTenant = (body: BootstrapTenantInput) => request<BootstrapTenantResponse>('POST', '/tenants/bootstrap', body)
export const listTenantAdmins = (slug: string) => request<ApiPrincipal[]>('GET', `/tenants/${encodeURIComponent(slug)}/admins`)
export const createTenantAdmin = (slug: string, body: { name: string; password: string }) =>
  request<ApiPrincipal>('POST', `/tenants/${encodeURIComponent(slug)}/admins`, body)
export const updateTenantQuota = (slug: string, body: TenantQuotaInput) =>
  request<TenantQuotaInput>('PUT', `/tenants/${encodeURIComponent(slug)}/quota`, body)
export const disableTenant = (slug: string) =>
  request<{ slug: string; status: 'DISABLED' }>('POST', `/tenants/${encodeURIComponent(slug)}/disable`)

export type ApiProjectNamespaceRule = {
  id: string
  project_id: string
  connector_id: string
  namespace: string
  created_at: string
}

export const listProjects = () => request<ApiProject[]>('GET', '/projects')
export const createProject = (body: { name: string; description?: string }) =>
  request<ApiProject>('POST', '/projects', body)
export const updateProject = (id: string, body: { name: string; description?: string }) =>
  request<ApiProject>('PUT', `/projects/${encodeURIComponent(id)}`, body)
export const deleteProject = (id: string) => request<void>('DELETE', `/projects/${encodeURIComponent(id)}`)
export type ApiProjectMembership = {
  tenant_id: string
  project_id: string
  project_name: string
  principal_id: string
  principal_name: string
  /** IdP display label for federated users; empty for local identities. */
  principal_display_name?: string
  principal_kind: string
  role: 'PROJECT_ADMIN' | 'OPERATOR' | 'VIEWER'
  status: 'ACTIVE' | 'DISABLED'
  created_by?: string
  created_at: string
  updated_at: string
}
export type ProjectMembershipPolicy = { enforced: boolean }
export type ApiProjectUser = {
  id: string
  name: string
  /** IdP display label for federated users; empty for local identities. */
  display_name?: string
  status: 'ACTIVE' | 'DISABLED'
  tenant_id: string
  last_login_at?: string
  created_at: string
  updated_at: string
  memberships: ApiProjectMembership[]
}
export const listProjectMembers = (projectId: string) => request<ApiProjectMembership[]>('GET', `/projects/${encodeURIComponent(projectId)}/members`)
export const listProjectMemberCandidates = (projectId: string) => request<ApiPrincipal[]>('GET', `/projects/${encodeURIComponent(projectId)}/member-candidates`)
export const addProjectMember = (projectId: string, body: { principalId: string; role: ApiProjectMembership['role'] }) => request<ApiProjectMembership>('POST', `/projects/${encodeURIComponent(projectId)}/members`, body)
export const createProjectUser = (projectId: string, body: { name: string; password: string; role: ApiProjectMembership['role'] }) => request<{ user: ApiPrincipal; membership: ApiProjectMembership }>('POST', `/projects/${encodeURIComponent(projectId)}/users`, body)
export const updateProjectMember = (projectId: string, principalId: string, body: { role: ApiProjectMembership['role']; status: ApiProjectMembership['status'] }) => request<ApiProjectMembership>('PUT', `/projects/${encodeURIComponent(projectId)}/members/${encodeURIComponent(principalId)}`, body)
export const removeProjectMember = (projectId: string, principalId: string) => request<void>('DELETE', `/projects/${encodeURIComponent(projectId)}/members/${encodeURIComponent(principalId)}`)
export const getProjectMembershipPolicy = () => request<ProjectMembershipPolicy>('GET', '/project-membership-policy')
export const updateProjectMembershipPolicy = (enforced: boolean) => request<ProjectMembershipPolicy>('PUT', '/project-membership-policy', { enforced })
export const listProjectUsers = () => request<ApiProjectUser[]>('GET', '/project-users')
export const getProjectUser = (principalId: string) => request<ApiProjectUser>('GET', `/project-users/${encodeURIComponent(principalId)}`)
export const updateProjectUserStatus = (principalId: string, status: ApiProjectUser['status']) =>
  request<ApiProjectUser>('PUT', `/project-users/${encodeURIComponent(principalId)}/status`, { status })
export const resetProjectUserPassword = (principalId: string, password: string) =>
  request<{ status: string; revoked_sessions: number }>('POST', `/project-users/${encodeURIComponent(principalId)}/reset-password`, { password })
export const revokeProjectUserSessions = (principalId: string) =>
  request<{ status: string; revoked_sessions: number }>('POST', `/project-users/${encodeURIComponent(principalId)}/revoke-sessions`)
export const setConnectorProject = (connectorId: string, projectId: string) =>
  request<{ connector_id: string; project_id: string }>('PUT', `/connectors/${encodeURIComponent(connectorId)}/project`, { project_id: projectId })
export const setConnectorEnvironment = (connectorId: string, environment: 'development' | 'testing' | 'production') =>
  request<{ connector_id: string; environment: string }>('PUT', `/connectors/${encodeURIComponent(connectorId)}/environment`, { environment })
export const listProjectNamespaceRules = () =>
  request<ApiProjectNamespaceRule[]>('GET', '/project-namespace-rules')
export const upsertProjectNamespaceRule = (body: { project_id: string; connector_id: string; namespace: string }) =>
  request<ApiProjectNamespaceRule>('PUT', '/project-namespace-rules', body)
export const deleteProjectNamespaceRule = (id: string) =>
  request<void>('DELETE', `/project-namespace-rules/${encodeURIComponent(id)}`)

// --- Auth ---

export type LoginResponse = {
  token: string
  expiresAt: string
  user: { id: string; name: string }
}

export type AuthUser = {
  id: string
  /** Stable internal identifier; federated users keep a `federated:<hash>` name. */
  name: string
  /** Human-readable label from the identity provider, when available. */
  display_name?: string
  email?: string
  /** IdP profile picture URL, when the provider supplies one. */
  avatar_url?: string
  /** 'oidc' for federated logins, 'local' for password logins. */
  auth_source?: string
  kind: string
  tenant: Omit<ApiTenant, 'quota' | 'usage'> | null
  role: 'platform_admin' | 'tenant_admin' | string
  scopes: string[]
  /** Server-computed capability list: pam:admin is already expanded. */
  capabilities?: string[]
  is_platform_admin: boolean
  expires_at?: string | null
}

/**
 * The single capability list used by both the backend guards and the console.
 * Older sessions (before /auth/me exposed capabilities) fall back to scopes.
 */
export const userCapabilities = (user: Pick<AuthUser, 'capabilities' | 'scopes'> | null | undefined): string[] =>
  user?.capabilities?.length ? user.capabilities : (user?.scopes ?? [])

/** True when the identity holds a capability. Never infer from a role name. */
export const canUser = (user: AuthUser | null | undefined, scope: string): boolean =>
  Boolean(user && userCapabilities(user).includes(scope))

/** Prefer the IdP display name in the UI; keep `name` for identity/audit lookups. */
export const userDisplayName = (user: Pick<AuthUser, 'name' | 'display_name' | 'email'> | null | undefined) =>
  user?.display_name?.trim() || user?.email?.trim() || user?.name || ''

/** First character used as the avatar fallback when no IdP picture exists. */
export const userInitial = (user: Pick<AuthUser, 'name' | 'display_name' | 'email'> | null | undefined) =>
  (userDisplayName(user).trim()[0] ?? '?').toUpperCase()

/**
 * Federated principals keep an opaque `federated:<hash>` name for uniqueness.
 * Every list/detail view should call this helper so the hash never reaches the
 * UI unless the IdP provided no profile attributes at all.
 */
export const apiPrincipalDisplayName = (
  principal: Pick<ApiPrincipal, 'name' | 'metadata'> | null | undefined,
) => {
  if (!principal) return ''
  const label = principal.metadata?.display_name
  const email = principal.metadata?.email
  return (typeof label === 'string' && label.trim()) ||
    (typeof email === 'string' && email.trim()) || principal.name || ''
}

export const projectUserDisplayName = (user: Pick<ApiProjectUser, 'name' | 'display_name'>) =>
  user.display_name?.trim() || user.name

export const membershipDisplayName = (member: Pick<ApiProjectMembership, 'principal_name' | 'principal_display_name'>) =>
  member.principal_display_name?.trim() || member.principal_name

export const login = (username: string, password: string, tenant?: string) =>
  request<LoginResponse>('POST', '/auth/login', { username, password, ...(tenant ? { tenant } : {}) })

// login_url is the canonical absolute start URL derived from the redirect URI
// registered with the IdP. The state/nonce cookies are host-only, so starting
// the flow from any other origin (e.g. localhost vs 127.0.0.1) loses them.
export type OIDCConfig = { enabled: boolean; login_url?: string; logout_url?: string }
export const getOIDCConfig = () => request<OIDCConfig>('GET', '/auth/oidc/config')
// Top-level navigation URL for the OIDC authorization-code flow. Must be a
// full page load (not fetch) so the state/nonce cookies survive the redirect.
// Used only as a fallback when the server does not return login_url.
export const oidcLoginUrl = `${API_BASE}/auth/oidc/start`

export const getMe = () => request<AuthUser>('GET', '/auth/me')
export const logout = () => request<{ status: string }>('POST', '/auth/logout')
export const changeOwnPassword = (currentPassword: string, newPassword: string) =>
  request<{ status: string; revoked_sessions: number }>('PUT', '/auth/password', { currentPassword, newPassword })

// --- Approval Rules ---

export type ApiApprovalRule = {
  id: string
  risk_level: string
  environment: string
  requires_approval: boolean
  priority: number
  description?: string
  created_at?: string
  updated_at?: string
}

export const listApprovalRules = () => request<ApiApprovalRule[]>('GET', '/approval-rules')
export const updateApprovalRule = (id: string, body: { requires_approval?: boolean; priority?: number; description?: string }) =>
  request<ApiApprovalRule>('PUT', `/approval-rules/${id}`, body)

export type PolicyDecision = 'ALLOW' | 'REQUIRE_APPROVAL' | 'DENY'

export type ApiPolicyRule = {
  id: string
  name: string
  project_id?: string
  project_name?: string
  resource_type: string
  action_name: string
  environment: '*' | 'development' | 'testing' | 'production'
  risk_level: '' | 'low' | 'medium' | 'high' | 'critical'
  principal_kind: '' | 'user' | 'agent_type' | 'agent_instance' | 'workload'
  decision: PolicyDecision
  priority: number
  enabled: boolean
  description?: string
  hit_count: number
  last_hit_at?: string
  conflict_ids: string[]
  shadowed_by?: string
  created_at: string
  updated_at: string
}

export type PolicyRuleInput = Omit<ApiPolicyRule,
  'id' | 'project_name' | 'hit_count' | 'last_hit_at' | 'conflict_ids' | 'shadowed_by' | 'created_at' | 'updated_at'>

export type PolicySimulationInput = {
  project_id?: string
  resource_type: string
  action_name: string
  environment: '*' | 'development' | 'testing' | 'production'
  risk_level: '' | 'low' | 'medium' | 'high' | 'critical'
  principal_kind: '' | 'user' | 'agent_type' | 'agent_instance' | 'workload'
  base_decision: PolicyDecision
}

export type PolicySimulationResult = {
  base_decision: PolicyDecision
  effective_decision: PolicyDecision
  matched_rule?: ApiPolicyRule | null
  rego_deny_protected: boolean
}

export const listPolicyRules = () => request<ApiPolicyRule[]>('GET', '/policy-rules')
export const createPolicyRule = (body: PolicyRuleInput) => request<ApiPolicyRule>('POST', '/policy-rules', body)
export const updatePolicyRule = (id: string, body: PolicyRuleInput) => request<ApiPolicyRule>('PUT', `/policy-rules/${id}`, body)
export const deletePolicyRule = (id: string) => request<void>('DELETE', `/policy-rules/${id}`)
export const copyPolicyRule = (id: string) => request<ApiPolicyRule>('POST', `/policy-rules/${id}/copy`)
export const simulatePolicyRule = (body: PolicySimulationInput) => request<PolicySimulationResult>('POST', '/policy-rules/simulate', body)

export type ApiNotificationChannel = {
  id?: string
  type: 'dingtalk'
  name: string
  enabled: boolean
  events: string[]
  configured: boolean
  webhook_configured: boolean
  coolapp_configured: boolean
  card_configured: boolean
  stream_configured: boolean
  credential_source: 'secret_file'
  created_at?: string
  updated_at?: string
}

export const getDingTalkChannel = () =>
  request<ApiNotificationChannel>('GET', '/notification-channels/dingtalk')
export const updateDingTalkChannel = (body: { name: string; enabled: boolean; events: string[] }) =>
  request<ApiNotificationChannel>('PUT', '/notification-channels/dingtalk', body)
export const testDingTalkChannel = () =>
  request<{ status: 'SUCCEEDED' }>('POST', '/notification-channels/dingtalk/test')

export type DingTalkCoolAppUser = { id: string; name: string }
export const getDingTalkCoolAppConfig = () =>
  coolAppRequest<{ client_id: string }>('GET', '/dingtalk/config')
export type DingTalkCoolAppApproval = {
  id: string
  status: string
  reason?: string
  parameters: Record<string, unknown>
  expires_at?: string
  created_at: string
  applicant?: string
  resource_name?: string
  resource_type?: string
  environment?: string
  action_name?: string
  risk_level?: string
}

export const authenticateDingTalkCoolApp = (body: { auth_code: string; corp_id: string; open_conversation_id: string }) =>
  coolAppRequest<{ user: DingTalkCoolAppUser; expires_at: string }>('POST', '/dingtalk/auth', body)
export const listDingTalkCoolAppApprovals = () =>
  coolAppRequest<DingTalkCoolAppApproval[]>('GET', '/dingtalk/access-requests')
export const decideDingTalkCoolAppApproval = (id: string, approve: boolean) =>
  coolAppRequest<{ id: string; status: string }>('POST', `/dingtalk/access-requests/${encodeURIComponent(id)}/${approve ? 'approve' : 'reject'}`)
export const logoutDingTalkCoolApp = () => coolAppRequest<void>('POST', '/dingtalk/logout')

export type AccountRole = 'user' | 'tenant_admin' | 'platform_admin'
/** A login identity as seen by a platform administrator. */
export type ApiAccount = {
  id: string
  name: string
  display_name: string
  email: string
  avatar_url: string
  auth_source: string
  role: AccountRole | string
  status: string
  tenant_id: string
  last_login_at: string | null
  created_at: string
  is_builtin_admin: boolean
  is_self: boolean
  provider_issuer?: string
  provider_subject?: string
}
export const listAccounts = () => request<ApiAccount[]>('GET', '/accounts')
export const updateAccountRole = (id: string, role: AccountRole) =>
  request<{ id: string; role: string; previous_role: string; revoked_sessions: number; force_relogin: boolean }>(
    'PUT', `/accounts/${encodeURIComponent(id)}/role`, { role })
export const accountDisplayName = (account: ApiAccount) =>
  account.display_name?.trim() || account.email?.trim() || account.name
