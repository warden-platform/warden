import type { ApiExecution, CreateExecutionRequest } from '../../../shared/api'

// ActionPanel 领域常量与共享类型：主面板与各资源类型编辑器共用。

export type ParameterSchema = {
  type?: string
  default?: unknown
  description?: string
  enum?: string[]
  minimum?: number
  maximum?: number
}
export type NetworkInterfacePreview = {
  before?: unknown
  after?: unknown
  commands?: unknown
  save?: boolean
  noOp?: boolean
  previewHash: string
  signature: string
}
export type ExecutionStarter = (request: CreateExecutionRequest) => Promise<ApiExecution>

export const GRAFANA_UPDATE_READ_ACTIONS: Record<string, string> = {
  'grafana.folder.update': 'grafana.folder.get',
  'grafana.dashboard.update': 'grafana.dashboard.get',
  'grafana.datasource.update': 'grafana.datasource.get',
}
export const KUBERNETES_MUTATION_READ_ACTIONS: Record<string, string> = {
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
export const GENERIC_KUBERNETES_KINDS: Record<string, { apiVersion: string; kind: string }> = {
  configmap: { apiVersion: 'v1', kind: 'ConfigMap' },
  service: { apiVersion: 'v1', kind: 'Service' },
  deployment: { apiVersion: 'apps/v1', kind: 'Deployment' },
  statefulset: { apiVersion: 'apps/v1', kind: 'StatefulSet' },
  daemonset: { apiVersion: 'apps/v1', kind: 'DaemonSet' },
  job: { apiVersion: 'batch/v1', kind: 'Job' },
  cronjob: { apiVersion: 'batch/v1', kind: 'CronJob' },
  ingress: { apiVersion: 'networking.k8s.io/v1', kind: 'Ingress' },
  networkpolicy: { apiVersion: 'networking.k8s.io/v1', kind: 'NetworkPolicy' },
  poddisruptionbudget: { apiVersion: 'policy/v1', kind: 'PodDisruptionBudget' },
}
export const GENERIC_KUBERNETES_KIND_OPTIONS = Object.values(GENERIC_KUBERNETES_KINDS)
export const NETWORK_MUTATION_PREVIEW_ACTIONS: Record<string, string> = {
  'network.interface.description.update': 'network.interface.description.preview',
  'network.interface.access_vlan.update': 'network.interface.access_vlan.preview',
  'network.interface.trunk_vlan.add': 'network.interface.trunk_vlan.preview',
  'network.interface.trunk_vlan.remove': 'network.interface.trunk_vlan.preview',
}
export const INTERNAL_PREVIEW_ACTIONS = new Set([
  'kubernetes.resource.preview',
  'database.query.preview',
  ...Object.values(NETWORK_MUTATION_PREVIEW_ACTIONS),
])
export const DATABASE_QUERY_AST_VERSION = 1
export const DATABASE_QUERY_SOURCE_ALIAS = 't'

export const DATABASE_FILTER_OPERATORS = ['eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'like', 'in', 'is_null', 'is_not_null']
