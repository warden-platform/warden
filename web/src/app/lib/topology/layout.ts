// Dagre layout for the topology graph: left-to-right DAG positioning.
import dagre from '@dagrejs/dagre'
import type { Edge, Node } from '@xyflow/react'
import type { TopoNode, TopoEdge } from './buildTopology'

const NODE_WIDTH = 220
const NODE_HEIGHT = 72

export function layoutTopology(
  nodes: TopoNode[],
  edges: TopoEdge[],
): { nodes: Node[]; edges: Edge[] } {
  const g = new dagre.graphlib.Graph()
  g.setGraph({ rankdir: 'LR', nodesep: 36, ranksep: 96, marginx: 24, marginy: 24 })
  g.setDefaultEdgeLabel(() => ({}))

  for (const n of nodes) {
    g.setNode(n.id, { width: NODE_WIDTH, height: NODE_HEIGHT })
  }
  for (const e of edges) {
    g.setEdge(e.source, e.target)
  }

  dagre.layout(g)

  const laid: Node[] = nodes.map((n) => ({
    id: n.id,
    type: 'topology',
    position: {
      x: (g.node(n.id).x ?? 0) - NODE_WIDTH / 2,
      y: (g.node(n.id).y ?? 0) - NODE_HEIGHT / 2,
    },
    data: n.data,
  }))

  const laidEdges: Edge[] = edges.map((e) => ({
    id: e.id,
    source: e.source,
    target: e.target,
    label: e.label,
    animated: e.animated,
    type: 'smoothstep',
  }))

  return { nodes: laid, edges: laidEdges }
}
