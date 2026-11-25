// 托管资源总览数据层：把顶层拓扑树（connectors → integrations/groups →
// samples/actions）聚合为「按资源类型 → 按生态」的资产清单，让首屏一眼回答
// “平台托管了哪些资源、各自多少、健康如何”。
import type { TopologyConnector, TopologyGroup, TopologyIntegration, TopologyTree } from '../../../shared/api'
import { resourceTypeLabel } from '../labels'
import { domainLabel, resourceTypeDomain, type ResourceDomain } from '../resources'

export type TopoStatus = 'active' | 'warning' | 'error' | 'neutral'

export type ResourceTypeSummary = {
  type: string
  label: string
  count: number
  activeCount: number
  health: string
  status: TopoStatus
  live: boolean
  hosts: number
}

export type ResourceDomainSummary = {
  domain: string
  label: string
  total: number
  active: number
  status: TopoStatus
  live: boolean
  types: ResourceTypeSummary[]
}

type GroupLocation = {
  connector: TopologyConnector
  integration?: TopologyIntegration
  group: TopologyGroup
}

// Kubernetes 资源去掉冗长前缀（Kubernetes Pod → Pod），其余生态前缀保留。
function compactTypeLabel(label: string): string {
  return label.replace(/^Kubernetes\s+/, '')
}

function groupStatus(count: number, activeCount: number): TopoStatus {
  if (count === 0 || activeCount === 0) return 'error'
  return activeCount >= count ? 'active' : 'warning'
}

function groupHealth(count: number, activeCount: number): string {
  if (count === 0) return '无资源'
  if (activeCount === 0) return count === 1 ? '不可用' : '全部不可用'
  if (activeCount >= count) return count === 1 ? '正常' : '全部正常'
  return `${activeCount}/${count} 可用`
}

function groupLocations(tree: TopologyTree): GroupLocation[] {
  const locations: GroupLocation[] = []
  for (const connector of tree.connectors) {
    for (const group of connector.groups) locations.push({ connector, group })
    for (const integration of connector.integrations) {
      for (const group of integration.groups) locations.push({ connector, integration, group })
    }
  }
  return locations
}

function summarizeType(type: string, locations: GroupLocation[]): ResourceTypeSummary {
  let count = 0
  let activeCount = 0
  let live = false
  for (const location of locations) {
    count += location.group.count
    activeCount += location.group.activeCount
    live = live || location.group.live
  }
  return {
    type,
    label: compactTypeLabel(resourceTypeLabel(type)),
    count,
    activeCount,
    health: groupHealth(count, activeCount),
    status: groupStatus(count, activeCount),
    live,
    hosts: locations.length,
  }
}

export function resourceTypeBreakdown(tree: TopologyTree): ResourceTypeSummary[] {
  const byType = new Map<string, GroupLocation[]>()
  for (const location of groupLocations(tree)) {
    const list = byType.get(location.group.type) ?? []
    list.push(location)
    byType.set(location.group.type, list)
  }
  return [...byType.entries()]
    .map(([type, locations]) => summarizeType(type, locations))
    .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label))
}

export function resourceTotals(tree: TopologyTree): { total: number; active: number; types: number } {
  const breakdown = resourceTypeBreakdown(tree)
  return {
    total: breakdown.reduce((sum, item) => sum + item.count, 0),
    active: breakdown.reduce((sum, item) => sum + item.activeCount, 0),
    types: breakdown.length,
  }
}

export function resourceDomains(tree: TopologyTree): ResourceDomainSummary[] {
  const byDomain = new Map<ResourceDomain, ResourceTypeSummary[]>()
  for (const item of resourceTypeBreakdown(tree)) {
    const domain = resourceTypeDomain(item.type)
    const list = byDomain.get(domain) ?? []
    list.push(item)
    byDomain.set(domain, list)
  }
  return [...byDomain.entries()]
    .map(([domain, types]) => {
      const total = types.reduce((sum, type) => sum + type.count, 0)
      const active = types.reduce((sum, type) => sum + type.activeCount, 0)
      return {
        domain,
        label: domainLabel(domain),
        total,
        active,
        status: groupStatus(total, active),
        live: types.some((type) => type.live),
        types: types.sort((a, b) => b.count - a.count),
      }
    })
    .sort((a, b) => b.total - a.total)
}

export function countLiveGroups(tree: TopologyTree): number {
  let live = 0
  for (const connector of tree.connectors) {
    live += connector.groups.filter((group) => group.live).length
    for (const integration of connector.integrations) {
      live += integration.groups.filter((group) => group.live).length
    }
  }
  return live
}