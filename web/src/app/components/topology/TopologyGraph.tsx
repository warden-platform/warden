import { useEffect, useMemo, useState, type CSSProperties } from 'react'
import {
  Background,
  BackgroundVariant,
  BaseEdge,
  EdgeLabelRenderer,
  MarkerType,
  ReactFlow,
  ReactFlowProvider,
  ViewportPortal,
  getBezierPath,
  useNodesState,
  useReactFlow,
  useViewport,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeMouseHandler,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Maximize2, Minus, Plus } from 'lucide-react'
import TopologyNode from './TopologyNode'
import type { TopoEdge, TopoNode } from '../../lib/topology/graph'
import { layoutTopologyGraph } from '../../lib/topology/layout'

const nodeTypes = { topology: TopologyNode }
const NODE_WIDTH = 232
const NODE_HEIGHT = 64
const LAYER_ORDER = ['channel', 'middleware', 'resource', 'action'] as const

// Edge labels stay hidden until hover so a dense graph doesn't turn into text.
function TopologyEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, label, markerEnd, animated }: EdgeProps) {
  const [hovered, setHovered] = useState(false)
  const [path, labelX, labelY] = getBezierPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition })
  return (
    <>
      <BaseEdge id={id} path={path} markerEnd={markerEnd} />
      <path
        d={path}
        fill="none"
        stroke="transparent"
        strokeWidth={16}
        style={{ cursor: 'pointer' }}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
      />
      {hovered && label && (
        <EdgeLabelRenderer>
          <div className="topo-edge-label nodrag nopan" style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}>
            {label}
          </div>
        </EdgeLabelRenderer>
      )}
      {animated && <path d={path} className="topo-edge-flow" />}
    </>
  )
}

const edgeTypes = { topo: TopologyEdge }

function laneLabel(layer: string): string {
  switch (layer) {
    case 'channel':
      return '通道'
    case 'middleware':
      return '中间件'
    case 'resource':
      return '资源'
    case 'action':
      return '动作'
    default:
      return '资源'
  }
}

function TopologyLanes({ nodes }: { nodes: Node[] }) {
  if (nodes.length === 0) return null
  const byLayer = new Map<string, { minX: number; maxX: number; minY: number; maxY: number }>()
  for (const node of nodes) {
    const layer = String((node.data as { layer?: string }).layer ?? 'resource')
    const box = byLayer.get(layer)
    if (!box) {
      byLayer.set(layer, { minX: node.position.x, maxX: node.position.x + NODE_WIDTH, minY: node.position.y, maxY: node.position.y + NODE_HEIGHT })
    } else {
      box.minX = Math.min(box.minX, node.position.x)
      box.maxX = Math.max(box.maxX, node.position.x + NODE_WIDTH)
      box.minY = Math.min(box.minY, node.position.y)
      box.maxY = Math.max(box.maxY, node.position.y + NODE_HEIGHT)
    }
  }
  return (
    <ViewportPortal>
      {LAYER_ORDER.filter((layer) => byLayer.has(layer)).map((layer) => {
        const box = byLayer.get(layer)!
        return (
          <div
            key={layer}
            className="topo-lane"
            style={{
              left: box.minX - 24,
              top: box.minY - 44,
              width: box.maxX - box.minX + 48,
              height: box.maxY - box.minY + 76,
            }}
          >
            <span>{laneLabel(layer)}</span>
          </div>
        )
      })}
    </ViewportPortal>
  )
}

function CanvasControls() {
  const { zoomIn, zoomOut, fitView } = useReactFlow()
  const { zoom } = useViewport()
  return (
    <div className="topo-controls">
      <button type="button" onClick={() => zoomOut()} aria-label="缩小">
        <Minus size={14} />
      </button>
      <span>{Math.round(zoom * 100)}%</span>
      <button type="button" onClick={() => zoomIn()} aria-label="放大">
        <Plus size={14} />
      </button>
      <button type="button" onClick={() => fitView({ padding: 0.22 })} aria-label="适配画布">
        <Maximize2 size={13} />
      </button>
    </div>
  )
}

function TopologyGraphInner({
  rawNodes,
  rawEdges,
  onNodeClick,
  onNodeDoubleClick,
  onPaneClick,
}: {
  rawNodes: TopoNode[]
  rawEdges: TopoEdge[]
  onNodeClick: (id: string) => void
  onNodeDoubleClick: (id: string) => void
  onPaneClick: () => void
}) {
  const laid = useMemo(() => layoutTopologyGraph(rawNodes, rawEdges), [rawNodes, rawEdges])
  const [nodes, setNodes, onNodesChange] = useNodesState(laid.nodes as Node[])

  useEffect(() => {
    setNodes(laid.nodes as Node[])
  }, [laid, setNodes])

  const edges = laid.edges.map(
    (edge) =>
      ({
        ...edge,
        markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: '#5b6b82' },
      }) as Edge,
  )

  const handleNodeClick: NodeMouseHandler = (_evt, node) => onNodeClick(node.id)
  const handleNodeDoubleClick: NodeMouseHandler = (_evt, node) => onNodeDoubleClick(node.id)

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      onNodeClick={handleNodeClick}
      onNodeDoubleClick={handleNodeDoubleClick}
      onPaneClick={onPaneClick}
      nodeTypes={nodeTypes}
      edgeTypes={edgeTypes}
      fitView
      fitViewOptions={{ padding: 0.08, maxZoom: 1.1, minZoom: 0.25 }}
      minZoom={0.16}
      maxZoom={1.8}
      zoomOnDoubleClick={false}
      nodesConnectable={false}
      edgesFocusable={false}
      proOptions={{ hideAttribution: true }}
    >
      <Background variant={BackgroundVariant.Dots} gap={24} size={1} />
      <TopologyLanes nodes={nodes} />
      <CanvasControls />
      {nodes.length === 0 && (
        <div className="pointer-events-none absolute inset-0 z-10 grid place-items-center">
          <p className="rounded-lg border border-dashed border-border bg-surface/80 px-4 py-2 text-xs text-content-subtle dark:border-border-dark dark:bg-surface-dark/80 dark:text-content-dark-subtle">
            当前没有可展示的节点
          </p>
        </div>
      )}
    </ReactFlow>
  )
}

export function TopologyGraph(props: {
  rawNodes: TopoNode[]
  rawEdges: TopoEdge[]
  onNodeClick: (id: string) => void
  onNodeDoubleClick: (id: string) => void
  onPaneClick?: () => void
  height?: number
}) {
  const style: CSSProperties = { height: props.height ?? 640 }
  return (
    <div style={style} className="topo-canvas relative overflow-hidden rounded-xl border border-border shadow-card dark:border-border-dark">
      <ReactFlowProvider>
        <TopologyGraphInner {...props} onPaneClick={props.onPaneClick ?? (() => {})} />
      </ReactFlowProvider>
    </div>
  )
}