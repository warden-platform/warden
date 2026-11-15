import { useEffect, useMemo, type CSSProperties } from 'react'
import {
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  type Edge,
  type Node,
  type NodeMouseHandler,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import TopologyNode from './TopologyNode'
import type { TopoEdge, TopoNode } from '../../lib/topology/buildTopology'
import { layoutTopology } from '../../lib/topology/layout'

const nodeTypes = { topology: TopologyNode }

function TopologyGraphInner({
  rawNodes,
  rawEdges,
  onNodeClick,
  onNodeDoubleClick,
}: {
  rawNodes: TopoNode[]
  rawEdges: TopoEdge[]
  onNodeClick: (id: string) => void
  onNodeDoubleClick: (id: string) => void
}) {
  const laid = useMemo(() => layoutTopology(rawNodes, rawEdges), [rawNodes, rawEdges])
  const [nodes, setNodes, onNodesChange] = useNodesState(laid.nodes as Node[])
  const [edges, setEdges, onEdgesChange] = useEdgesState(laid.edges as Edge[])

  useEffect(() => {
    setNodes(laid.nodes as Node[])
    setEdges(laid.edges as Edge[])
  }, [laid, setNodes, setEdges])

  const handleNodeClick: NodeMouseHandler = (_evt, node) => onNodeClick(node.id)
  const handleNodeDoubleClick: NodeMouseHandler = (_evt, node) => onNodeDoubleClick(node.id)

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      onNodeClick={handleNodeClick}
      onNodeDoubleClick={handleNodeDoubleClick}
      nodeTypes={nodeTypes}
      fitView
      fitViewOptions={{ padding: 0.15 }}
      proOptions={{ hideAttribution: true }}
      defaultEdgeOptions={{ type: 'smoothstep' }}
    >
      <Background variant={BackgroundVariant.Dots} gap={18} size={1} className="!bg-white/40 dark:!bg-surface-dark/60" />
      <Controls className="!border-white/60 !bg-white/70 !text-content !shadow-glass !backdrop-blur-xl dark:!border-white/10 dark:!bg-surface-dark/70 dark:!text-content-dark dark:!shadow-glass-dark" />
      <MiniMap
        pannable
        zoomable
        className="!border-white/60 !bg-white/70 !shadow-glass !backdrop-blur-xl dark:!border-white/10 dark:!bg-surface-dark/70 dark:!shadow-glass-dark"
        nodeColor={(n) => {
          const status = (n.data as unknown as { status?: string } | undefined)?.status
          if (status === 'active') return '#22c55e'
          if (status === 'warning') return '#f59e0b'
          if (status === 'error') return '#ef4444'
          return '#94a3b8'
        }}
      />
    </ReactFlow>
  )
}

export function TopologyGraph(props: {
  rawNodes: TopoNode[]
  rawEdges: TopoEdge[]
  onNodeClick: (id: string) => void
  onNodeDoubleClick: (id: string) => void
  height?: number
}) {
  const style: CSSProperties = { height: props.height ?? 560 }
  return (
    <div
      style={style}
      className="relative overflow-hidden rounded-lg border border-white/60 bg-white/40 shadow-glass backdrop-blur-xl dark:border-white/10 dark:bg-surface-dark/60 dark:shadow-glass-dark"
    >
      <ReactFlowProvider>
        <TopologyGraphInner {...props} />
      </ReactFlowProvider>
    </div>
  )
}
