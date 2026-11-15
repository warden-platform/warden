import { request } from './core'

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

// --- API functions ---

const resourcesInFlight = new Map<string, Promise<ApiResource[]>>()

// React StrictMode and concurrent page mounts may ask for the catalog at the
// same time. Share the in-flight response so a navigation never downloads the
// multi-megabyte catalog twice.
export const listResources = (
  options: {
    domain?: string
    topology?: boolean
    type?: string
    excludeType?: string
    namespace?: string
    integrationId?: string
    projectId?: string
    limit?: number
  } = {},
) => {
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
  request<{ resource_id: string; project_id: string; project_name: string; project_source: 'RESOURCE' }>(
    'PUT',
    `/resources/${encodeURIComponent(resourceId)}/project`,
    { project_id: projectId, reason },
  )
export const clearResourceProject = (resourceId: string) => request<void>('DELETE', `/resources/${encodeURIComponent(resourceId)}/project`)
export const setResourceEnvironment = (resourceId: string, environment: 'development' | 'testing' | 'production', reason = '') =>
  request<{ resource_id: string; environment: string; environment_source: 'RESOURCE' }>(
    'PUT',
    `/resources/${encodeURIComponent(resourceId)}/environment`,
    { environment, reason },
  )
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
export const listAvailableResources = (type?: string) =>
  request<ApiResource[]>('GET', `/resources?availableForExecution=true${type ? `&type=${encodeURIComponent(type)}` : ''}`)
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
export const updateResourceProjectScope = (
  resourceId: string,
  body: { mode: ResourceProjectScope['mode']; projectIds: string[]; reason?: string },
) => request<ResourceProjectScope>('PUT', `/resources/${encodeURIComponent(resourceId)}/project-scope`, body)
