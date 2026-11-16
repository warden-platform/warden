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
      <Background variant={BackgroundVariant.Dots} gap={18} size={1} className="!bg-surface-muted dark:!bg-[#0a0e15]" />
      <Controls className="!border-border !bg-surface !text-content !shadow-card dark:!border-border-dark dark:!bg-surface-dark dark:!text-content-dark" />
      <MiniMap
        pannable
        zoomable
        className="!border-border !bg-surface !shadow-card dark:!border-border-dark dark:!bg-surface-dark"
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
      className="relative overflow-hidden rounded-lg border border-border bg-surface-muted shadow-card dark:border-border-dark dark:bg-[#0a0e15]"
    >
      <ReactFlowProvider>
        <TopologyGraphInner {...props} />
      </ReactFlowProvider>
    </div>
  )
}
