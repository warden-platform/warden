import { request } from './core'

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
    case 'DRAFT':
      return '草稿'
    case 'TESTING':
      return '测试中'
    case 'DEGRADED':
      return '降级'
    case 'QUARANTINED':
      return '隔离'
    case 'DISABLED':
      return '已停用'
    default:
      return status
  }
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
export type HostPairingOptionsResponse = {
  availableCommands: string[]
  machine: Record<string, unknown>
  platform: 'windows' | 'macos'
  expiresAt: string
}

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

export const listConnectors = () => request<ApiConnector[]>('GET', '/connectors')
export const getInstallConfig = () => request<InstallConfig>('GET', '/install-config')
export type NetworkConnectorProvision = {
  id: string
  name: string
  connectorType: 'network'
  status: 'DRAFT'
  taskSigningKey: string
  credentialWarning: string
}
export const createNetworkConnector = (body: { name: string; environment: 'development' | 'testing' | 'production'; projectId?: string }) =>
  request<NetworkConnectorProvision>('POST', '/connectors/network', body)
export const deleteConnector = (id: string) => request<{ id: string; status: 'DISABLED' }>('DELETE', `/connectors/${id}`)
export const archiveConnector = (id: string) => request<{ id: string; status: 'ARCHIVED' }>('POST', `/connectors/${id}/archive`)
export const forceArchiveConnector = (id: string) =>
  request<{ id: string; status: 'ARCHIVED' }>('POST', `/connectors/${id}/force-archive`, { confirmTargetRemoved: true })
export const checkConnector = (id: string) => request<ConnectorCheckResponse>('POST', `/connectors/${id}/check`)
export const updateConnectorNamespaces = (id: string, namespaces: string[]) =>
  request<{ id: string; namespaces: string[]; status: 'RECONCILING' }>('PUT', `/connectors/${id}/namespaces`, { namespaces })
export const updateConnectorServices = (id: string, services: string[], containers: string[]) =>
  request<{ id: string; services: string[]; containers: string[]; status: 'RECONCILING' }>('PUT', `/connectors/${id}/services`, {
    services,
    containers,
  })
export const onboardCluster = (req: OnboardRequest) => request<OnboardResponse>('POST', '/connectors/onboard', req)
export const confirmClusterPairing = (req: ConfirmPairingRequest) =>
  request<ConfirmPairingResponse>('POST', '/cluster-pairings/confirm', req)
export const getClusterPairingOptions = (code: string) => request<PairingOptionsResponse>('POST', '/cluster-pairings/options', { code })
export const getLinuxPairingOptions = (code: string) => request<LinuxPairingOptionsResponse>('POST', '/linux-pairings/options', { code })
export const confirmLinuxPairing = (req: {
  code: string
  name: string
  environment: 'development' | 'testing' | 'production'
  projectId?: string
  allowedServices: string[]
  allowedContainers: string[]
}) => request<ConfirmPairingResponse>('POST', '/linux-pairings/confirm', req)
export const getHostPairingOptions = (code: string) => request<HostPairingOptionsResponse>('POST', '/host/pairings/options', { code })
export const confirmHostPairing = (req: {
  code: string
  platform: 'windows' | 'macos'
  name: string
  environment: 'development' | 'testing' | 'production'
  projectId?: string
  allowedCommands: string[]
}) => request<ConfirmPairingResponse>('POST', '/host/pairings/confirm', req)
export const setConnectorProject = (connectorId: string, projectId: string) =>
  request<{ connector_id: string; project_id: string }>('PUT', `/connectors/${encodeURIComponent(connectorId)}/project`, {
    project_id: projectId,
  })
export const setConnectorEnvironment = (connectorId: string, environment: 'development' | 'testing' | 'production') =>
  request<{ connector_id: string; environment: string }>('PUT', `/connectors/${encodeURIComponent(connectorId)}/environment`, {
    environment,
  })
