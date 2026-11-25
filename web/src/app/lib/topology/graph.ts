// Build a layered topology graph (nodes + edges) from the /api/v1/topology
// tree. The relational view is:
//   执行通道 (every connector: k8s/linux/network/database/jenkins — they hold
//             task keys and execute operations)
//     → 中间件 (integration: MySQL/Jenkins/Grafana/… — credentials resolved
//               over the connector channel)
//       → 资源分组 (folded per resource type)
//         → 动作
import type { TopologyGroup, TopologyTree } from '../../../shared/api'
import { resourceTypeLabel } from '../labels'
import { resourceTypeDomain } from '../resources'

export type TopoStatus = 'active' | 'warning' | 'error' | 'neutral'

export type TopoEntity = 'connector' | 'integration' | 'resource-group' | 'action'

export type TopoLayer = 'channel' | 'middleware' | 'resource' | 'action'

export type TopoNodeData = {
  label: string
  sublabel?: string
  entity: TopoEntity
  layer: TopoLayer
  kind: string
  status: TopoStatus
  count?: number
  actions?: string[]
  detailPath?: string
  live?: boolean
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

export type TopologyGraphData = { nodes: TopoNode[]; edges: TopoEdge[] }

// Connector types are the execution channel kinds; database/jenkins/grafana are
// channels too — they connect middleware and store its credentials, not the
// middleware itself. The middleware are the integrations attached to them.
export function channelTypeLabel(type: string): string {
  switch (type.toLowerCase()) {
    case 'kubernetes':
      return 'Kubernetes 集群'
    case 'linux':
      return 'Linux 主机'
    case 'network':
      return '网络设备'
    case 'database':
      return '数据库'
    case 'jenkins':
      return 'Jenkins'
    case 'grafana':
      return 'Grafana'
    case 'huawei':
      return '云厂商'
    default:
      return type
  }
}

// Kubernetes resource types drop the redundant ecosystem prefix on cards.
function compactTypeLabel(label: string): string {
  return label.replace(/^Kubernetes\s+/, '')
}

function normalizeStatus(status: string | undefined): TopoStatus {
  switch (status) {
    case 'ACTIVE':
      return 'active'
    case 'DEGRADED':
    case 'FAILED':
    case 'DISABLED':
    case 'QUARANTINED':
    case 'ORPHANED':
      return 'warning'
    case 'TESTING':
    case 'DRAFT':
    case 'DISCOVERED':
      return 'neutral'
    default:
      return 'neutral'
  }
}

function edge(source: string, target: string, label: string, animated = false): TopoEdge {
  return { id: `e:${source}->${target}`, source, target, label, animated }
}

export function buildTopologyGraph(tree: TopologyTree, options?: { includeActions?: boolean }): TopologyGraphData {
  const includeActions = options?.includeActions ?? true
  const nodes: TopoNode[] = []
  const edges: TopoEdge[] = []
  const actionTypes = new Set<string>()

  const groupNode = (group: TopologyGroup): TopoNode => ({
    id: group.id,
    type: 'topology',
    position: { x: 0, y: 0 },
    data: {
      label: compactTypeLabel(resourceTypeLabel(group.type)),
      sublabel: group.health,
      entity: 'resource-group',
      layer: 'resource',
      kind: group.type,
      status: group.status,
      count: group.count,
      actions: group.actions.map((action) => action.name),
      live: group.live,
      detailPath: `/resources?domain=${resourceTypeDomain(group.type)}`,
      raw: group,
    },
  })

  const pushAction = (group: TopologyGroup) => {
    if (!includeActions || group.actions.length === 0) return
    const actionId = `action:${group.type}`
    if (!actionTypes.has(group.type)) {
      actionTypes.add(group.type)
      nodes.push({
        id: actionId,
        type: 'topology',
        position: { x: 0, y: 0 },
        data: {
          label: compactTypeLabel(resourceTypeLabel(group.type)),
          sublabel: `${group.actions.length} 个动作`,
          entity: 'action',
          layer: 'action',
          kind: group.type,
          status: 'neutral',
          actions: group.actions.map((action) => action.name),
          live: group.live,
          raw: { type: group.type, actions: group.actions },
        },
      })
    }
    edges.push(edge(group.id, actionId, '可执行', group.live))
  }

  for (const connector of tree.connectors) {
    const connectorId = `connector:${connector.id}`
    nodes.push({
      id: connectorId,
      type: 'topology',
      position: { x: 0, y: 0 },
      data: {
        label: connector.name,
        sublabel: undefined,
        entity: 'connector',
        layer: 'channel',
        kind: connector.type.toLowerCase(),
        status: normalizeStatus(connector.status),
        count: connector.integrations.length + connector.groups.length,
        live: connector.live,
        detailPath: `/connectors/${connector.id}`,
        raw: connector,
      },
    })

    for (const integration of connector.integrations) {
      const integrationId = `integration:${integration.id}`
      nodes.push({
        id: integrationId,
        type: 'topology',
        position: { x: 0, y: 0 },
        data: {
          label: integration.name,
          sublabel: undefined,
          entity: 'integration',
          layer: 'middleware',
          kind: integration.protocol.toLowerCase(),
          status: normalizeStatus(integration.status),
          count: integration.groups.length,
          live: integration.live,
          detailPath: `/integrations/${integration.id}`,
          raw: integration,
        },
      })
      edges.push(edge(connectorId, integrationId, '承载', integration.live || connector.live))
      integration.groups.forEach((group) => {
        nodes.push(groupNode(group))
        edges.push(edge(integrationId, group.id, '发现', group.live))
        pushAction(group)
      })
    }

    connector.groups.forEach((group) => {
      nodes.push(groupNode(group))
      edges.push(edge(connectorId, group.id, '发现', group.live))
      pushAction(group)
    })
  }

  return { nodes, edges }
}