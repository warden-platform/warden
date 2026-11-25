// Manual layered layout: place nodes in fixed columns by `layer` so the
// semantic tiers (channel → middleware → resource → action) are exact, rather
// than letting dagre merge a middleware connector into the channel column.
import type { Edge, Node } from '@xyflow/react'
import type { TopoEdge, TopoLayer, TopoNode } from './graph'

const NODE_WIDTH = 232
const NODE_HEIGHT = 64
const COL_GAP = 150
const ROW_GAP = 20
const LAYER_ORDER: TopoLayer[] = ['channel', 'middleware', 'resource', 'action']

export function layoutTopologyGraph(nodes: TopoNode[], edges: TopoEdge[]): { nodes: Node[]; edges: Edge[] } {
  const columns = new Map<TopoLayer, TopoNode[]>()
  for (const node of nodes) {
    const layer = node.data.layer
    const list = columns.get(layer) ?? []
    list.push(node)
    columns.set(layer, list)
  }

  const laidNodes: Node[] = []
  LAYER_ORDER.forEach((layer, colIndex) => {
    const list = columns.get(layer) ?? []
    list.forEach((node, rowIndex) => {
      laidNodes.push({
        id: node.id,
        type: 'topology',
        position: {
          x: colIndex * (NODE_WIDTH + COL_GAP),
          y: rowIndex * (NODE_HEIGHT + ROW_GAP),
        },
        data: node.data,
      })
    })
  })

  const laidEdges: Edge[] = edges.map((edge) => ({
    id: edge.id,
    source: edge.source,
    target: edge.target,
    label: edge.label,
    animated: edge.animated,
    type: 'topo',
  }))

  return { nodes: laidNodes, edges: laidEdges }
}