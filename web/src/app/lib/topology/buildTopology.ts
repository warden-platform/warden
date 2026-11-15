// Build a topology graph (nodes + edges) from PAM API data.
// Layers: Connectors -> Integrations -> Resource Groups -> Actions
// Resources are folded by (parent, resource_type) so a Jenkins integration
// with 359 jobs renders as a single "359 Jenkins Jobs" node, not 359 nodes.
import type { ApiAction, ApiConnector, ApiExecution, ApiResource, Integration } from '../../../shared/api'
import { connectorTypeLabel } from '../../../shared/types'
import { resourceTypeLabel } from '../labels'
import { resourceDomain } from '../resources'

export type TopoStatus = 'active' | 'warning' | 'error' | 'neutral'

export type TopoEntity = 'connector' | 'integration' | 'resource-group' | 'action'

export type TopoNodeData = {
  label: string
  sublabel?: string
  entity: TopoEntity
  kind: string // 'kubernetes' | 'linux' | 'jenkins' | 'deployment' | 'pod' | ...
  status: TopoStatus
  count?: number // resource-group member count
  actions?: string[] // for action nodes
  detailPath?: string
  raw?: unknown
}

export type TopoNode = {
  id: string
  type: 'topology'
  position: { x: number; y: number }
  data: TopoNodeData
}

export type TopoEdge = {
  id: string
  source: string
  target: string
  label?: string
  animated?: boolean
}

export type TopologyGraph = { nodes: TopoNode[]; edges: TopoEdge[] }

function normalizeStatus(s: string | undefined): TopoStatus {
  switch (s) {
    case 'ACTIVE':
      return 'active'
    case 'DEGRADED':
    case 'FAILED':
    case 'ORPHANED':
    case 'DISABLED':
    case 'QUARANTINED':
      return 'warning'
    case 'TESTING':
    case 'DRAFT':
    case 'DISCOVERED':
      return 'neutral'
    default:
      return 'neutral'
  }
}

function connectorKind(t: string): string {
  return t.toLowerCase() === 'kubernetes' ? 'kubernetes' : t.toLowerCase() === 'linux' ? 'linux' : t.toLowerCase()
}

export function buildTopology(
  connectors: ApiConnector[],
  integrations: Integration[],
  resources: ApiResource[],
  actions: ApiAction[],
  executions: ApiExecution[],
): TopologyGraph {
  const nodes: TopoNode[] = []
  const edges: TopoEdge[] = []

  // Recent execution set per resource_type (for animating action edges).
  const recentTypes = new Set<string>()
  const resourceByID = new Map(resources.map((r) => [r.id, r]))
  const cutoff = Date.now() - 24 * 60 * 60 * 1000
  for (const e of executions) {
    const res = resourceByID.get(e.resourceId)
    if (res && new Date(e.createdAt).getTime() >= cutoff) {
      recentTypes.add(res.resource_type)
    }
  }

  // --- Layer 1: Connectors ---
  for (const c of connectors) {
    nodes.push({
      id: `connector:${c.id}`,
      type: 'topology',
      position: { x: 0, y: 0 },
      data: {
        label: c.name,
        sublabel: `${connectorTypeLabel(c.connector_type)} · ${c.version}`,
        entity: 'connector',
        kind: connectorKind(c.connector_type),
        status: normalizeStatus(c.status),
        detailPath: `/connectors/${c.id}`,
        raw: c,
      },
    })
  }

  // --- Layer 2: Integrations ---
  for (const i of integrations) {
    nodes.push({
      id: `integration:${i.id}`,
      type: 'topology',
      position: { x: 0, y: 0 },
      data: {
        label: i.name,
        sublabel: `${i.protocol} · ${i.endpoint.replace(/^https?:\/\//, '').slice(0, 32)}`,
        entity: 'integration',
        kind: i.protocol.toLowerCase(),
        status: normalizeStatus(i.status),
        detailPath: `/integrations/${i.id}`,
        raw: i,
      },
    })
    edges.push({
      id: `e-conn-${i.connector_id}-int-${i.id}`,
      source: `connector:${i.connector_id}`,
      target: `integration:${i.id}`,
      label: '承载',
    })
  }

  // --- Layer 3: Resource groups (fold by parent + resource_type) ---
  type Group = {
    parentId: string
    integrationId?: string
    connectorId: string
    type: string
    members: ApiResource[]
    count: number
    activeCount: number
  }
  const groupMap = new Map<string, Group>()
  for (const r of resources) {
    const parentId = r.integration_id ? `integration:${r.integration_id}` : `connector:${r.connector_id}`
    const key = `${parentId}:${r.resource_type}`
    let g = groupMap.get(key)
    if (!g) {
      g = {
        parentId,
        integrationId: r.integration_id,
        connectorId: r.connector_id,
        type: r.resource_type,
        members: [],
        count: 0,
        activeCount: 0,
      }
      groupMap.set(key, g)
    }
    g.members.push(r)
    const memberCount = Number((r.metadata as { topologyCount?: number }).topologyCount ?? 1)
    g.count += memberCount
    if (r.status === 'ACTIVE') g.activeCount += memberCount
  }

  for (const [, g] of groupMap) {
    const id = `group:${g.integrationId ?? g.connectorId}:${g.type}`
    const typeLabel = resourceTypeLabel(g.type)
    const domain = resourceDomain(g.members[0])
    const allActive = g.activeCount === g.count
    const anyActive = g.activeCount > 0
    nodes.push({
      id,
      type: 'topology',
      position: { x: 0, y: 0 },
      data: {
        label: `${typeLabel} × ${g.count}`,
        sublabel: anyActive ? (allActive ? '全部可用' : `${g.activeCount} 可用`) : '无可用',
        entity: 'resource-group',
        kind: g.type,
        status: allActive ? 'active' : anyActive ? 'warning' : 'error',
        count: g.count,
        detailPath: `/resources?domain=${domain}`,
        raw: { type: g.type, count: g.count, connectorId: g.connectorId, integrationId: g.integrationId, sample: g.members.slice(0, 8) },
      },
    })
    edges.push({
      id: `e-${g.parentId}-group-${g.type}`,
      source: g.parentId,
      target: id,
      label: '发现',
    })

    // --- Layer 4: Action node (one per resource_type that has actions) ---
    const typeActions = actions.filter((a) => a.resource_type === g.type).map((a) => a.name)
    if (typeActions.length > 0) {
      const actionId = `action:${g.type}`
      // Only create the action node once per type.
      if (!nodes.some((n) => n.id === actionId)) {
        nodes.push({
          id: actionId,
          type: 'topology',
          position: { x: 0, y: 0 },
          data: {
            label: `${typeLabel} 动作`,
            sublabel: `${typeActions.length} 个动作`,
            entity: 'action',
            kind: g.type,
            status: 'neutral',
            actions: typeActions,
            detailPath: undefined,
            raw: { actions: typeActions },
          },
        })
      }
      edges.push({
        id: `e-${id}-to-${actionId}`,
        source: id,
        target: actionId,
        label: '可执行',
        animated: recentTypes.has(g.type),
      })
    }
  }

  return { nodes, edges }
}
