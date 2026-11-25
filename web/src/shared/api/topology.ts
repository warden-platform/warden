import { request } from './core'

// Nested topology tree returned by GET /api/v1/topology. The server folds
// resources by (parent, resource_type) and attaches up to six real sample
// names plus the actions available for that type.
export type TopologySample = {
  name: string
  namespace?: string
  status: string
}

export type TopologyActionRef = {
  name: string
  displayName?: string
  risk?: string
}

export type TopologyGroup = {
  id: string
  type: string
  count: number
  activeCount: number
  status: 'active' | 'warning' | 'error'
  health: string
  live: boolean
  samples: TopologySample[]
  actions: TopologyActionRef[]
}

export type TopologyIntegration = {
  id: string
  name: string
  protocol: string
  endpoint: string
  status: string
  live: boolean
  groups: TopologyGroup[]
}

export type TopologyConnector = {
  id: string
  name: string
  type: string
  version: string
  status: string
  live: boolean
  integrations: TopologyIntegration[]
  groups: TopologyGroup[]
}

export type TopologySummary = {
  connectors: number
  integrations: number
  groups: number
  resources: number
  actions: number
}

export type TopologyTree = {
  summary: TopologySummary
  connectors: TopologyConnector[]
}

export const getTopology = () => request<TopologyTree>('GET', '/topology')
