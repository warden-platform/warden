import { request } from './core'

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
