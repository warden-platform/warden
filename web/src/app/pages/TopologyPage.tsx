import { useMemo, useState } from 'react'
import { useNavigate } from '../../shared/router'
import { RefreshCw, Share2 } from 'lucide-react'
import { getTopology } from '../../shared/api'
import { LoadingState, ErrorState, EmptyState } from '../components/States'
import { TopologyGraph } from '../components/topology/TopologyGraph'
import { TopologyDetails } from '../components/topology/TopologyDetails'
import { TopologyExport } from '../components/topology/TopologyExport'
import { buildTopologyGraph } from '../lib/topology/graph'
import { countLiveGroups } from '../lib/topology/tree'
import { exportTopologyText } from '../lib/topology/exportText'
import { useApiQuery } from '../lib/useApiQuery'

function FilterChip({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`rounded-md border px-2.5 py-1 text-xs font-semibold transition ${
        active
          ? 'border-brand bg-brand/10 text-brand dark:border-brand-dark dark:bg-brand-dark/15 dark:text-brand-dark'
          : 'border-border bg-surface text-content-subtle hover:bg-surface-muted dark:border-border-dark dark:bg-transparent dark:text-content-dark-subtle dark:hover:bg-surface-dark-muted'
      }`}
    >
      {children}
    </button>
  )
}

export function TopologyPage() {
  const navigate = useNavigate()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [includeActions, setIncludeActions] = useState(false)

  const topologyQuery = useApiQuery(() => getTopology(), [], { intervalMs: 15000 })
  const tree = topologyQuery.data
  const loading = topologyQuery.loading
  const error = topologyQuery.error
  const load = () => topologyQuery.reload()

  const graph = useMemo(() => (tree ? buildTopologyGraph(tree, { includeActions }) : null), [tree, includeActions])
  const liveCount = useMemo(() => (tree ? countLiveGroups(tree) : 0), [tree])
  const exportText = useMemo(() => (tree ? exportTopologyText(tree) : ''), [tree])

  const selectedData = useMemo(() => {
    if (!selectedId || !graph) return null
    return graph.nodes.find((node) => node.id === selectedId)?.data ?? null
  }, [selectedId, graph])

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={load} />
  if (!tree || tree.connectors.length === 0) {
    return <EmptyState icon={Share2} title="尚无拓扑数据" text="接入第一个执行通道后，这里会呈现一张完整的组织拓扑图。" />
  }

  return (
    <div className="view-stack">
      <div className="panel">
        <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border px-4 py-3 dark:border-border-dark">
          <div className="flex items-start gap-2.5">
            <div className="grid h-9 w-9 flex-none place-items-center rounded-lg bg-brand/10 text-brand dark:bg-brand-dark/10 dark:text-brand-dark">
              <Share2 size={17} />
            </div>
            <div>
              <h1 className="text-sm font-semibold text-content dark:text-content-dark">组织拓扑图</h1>
              <p className="mt-0.5 text-[11px] text-content-subtle dark:text-content-dark-subtle">
                通道 → 中间件 → 资源，一张图看清组织全貌，每 15 秒自动刷新
              </p>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {liveCount > 0 ? (
              <span className="topbar-live" title="执行中 / 排队中 / 等待审批的动作信号">
                <i aria-hidden="true" />
                {liveCount} 个执行信号
              </span>
            ) : (
              <span className="rounded-full border border-border px-2 py-0.5 text-[10px] font-medium text-content-subtle dark:border-border-dark dark:text-content-dark-subtle">
                无执行信号
              </span>
            )}
            <span className="rounded-full border border-border px-2 py-0.5 text-[10px] font-medium tabular-nums text-content-subtle dark:border-border-dark dark:text-content-dark-subtle">
              {graph?.nodes.length ?? 0} 节点 · {graph?.edges.length ?? 0} 连线
            </span>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2.5">
          <span className="text-[10px] font-semibold uppercase tracking-wider text-content-subtle dark:text-content-dark-subtle">分层</span>
          <FilterChip active={!includeActions} onClick={() => setIncludeActions(false)}>
            通道 · 中间件 · 资源
          </FilterChip>
          <FilterChip active={includeActions} onClick={() => setIncludeActions(true)}>
            含动作层
          </FilterChip>
          <div className="ml-auto flex items-center gap-2">
            <button className="quiet-button" onClick={() => load()}>
              <RefreshCw size={15} />
              刷新
            </button>
            <TopologyExport text={exportText} />
          </div>
        </div>
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_300px]">
        <TopologyGraph
          rawNodes={graph?.nodes ?? []}
          rawEdges={graph?.edges ?? []}
          onNodeClick={setSelectedId}
          onNodeDoubleClick={(id) => {
            const node = graph?.nodes.find((item) => item.id === id)
            if (node?.data.detailPath) navigate(node.data.detailPath)
          }}
          onPaneClick={() => setSelectedId(null)}
          height={680}
        />
        <div className="h-auto xl:h-[680px]">
          <TopologyDetails data={selectedData} onClose={() => setSelectedId(null)} onNavigate={navigate} />
        </div>
      </div>
    </div>
  )
}