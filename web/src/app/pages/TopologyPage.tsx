import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from '../../shared/router'
import { RefreshCw, Search, Share2 } from 'lucide-react'
import type { ApiAction, ApiConnector, ApiError, ApiExecution, ApiResource } from '../../shared/api'
import { listAvailableActions, listConnectors, listIntegrations, listResources, type Integration } from '../../shared/api'
import { LoadingState, ErrorState, EmptyState } from '../components/States'
import { TopologyGraph } from '../components/topology/TopologyGraph'
import { TopologyDetails } from '../components/topology/TopologyDetails'
import { TopologyExport } from '../components/topology/TopologyExport'
import { buildTopology, type TopoNodeData, type TopoEntity, type TopoStatus } from '../lib/topology/buildTopology'
import { exportTopologyText } from '../lib/topology/exportText'

const TYPE_FILTERS: { key: TopoEntity; label: string }[] = [
  { key: 'connector', label: '通道' },
  { key: 'integration', label: '中间件' },
  { key: 'resource-group', label: '资源' },
  { key: 'action', label: '动作' },
]
const STATUS_FILTERS: { key: TopoStatus; label: string; dot: string }[] = [
  { key: 'active', label: '正常', dot: 'bg-green-500' },
  { key: 'warning', label: '注意', dot: 'bg-amber-500' },
  { key: 'error', label: '异常', dot: 'bg-red-500' },
  { key: 'neutral', label: '中性', dot: 'bg-gray-400' },
]

const DOMAIN_LEGEND: { label: string; dot: string }[] = [
  { label: 'Kubernetes', dot: 'bg-sky-500' },
  { label: 'Linux', dot: 'bg-violet-500' },
  { label: 'Jenkins', dot: 'bg-orange-500' },
  { label: 'Grafana', dot: 'bg-yellow-500' },
  { label: '数据库', dot: 'bg-emerald-500' },
  { label: '华为云', dot: 'bg-red-500' },
]

function FilterChip({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`rounded-md border px-2.5 py-1 text-xs font-semibold transition ${
        active
          ? 'border-brand bg-brand/10 text-brand dark:border-brand-dark dark:bg-brand-dark/15 dark:text-brand-dark'
          : 'border-border bg-surface text-content-subtle hover:bg-surface-muted dark:border-border-dark dark:bg-surface-dark dark:text-content-dark-subtle dark:hover:bg-surface-dark-muted'
      }`}
    >
      {children}
    </button>
  )
}

export function TopologyPage() {
  const navigate = useNavigate()
  const [connectors, setConnectors] = useState<ApiConnector[]>([])
  const [integrations, setIntegrations] = useState<Integration[]>([])
  const [resources, setResources] = useState<ApiResource[]>([])
  const [executions, setExecutions] = useState<ApiExecution[]>([])
  const [actions, setActions] = useState<ApiAction[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const [query, setQuery] = useState('')
  const [typeOn, setTypeOn] = useState<Set<TopoEntity>>(new Set(TYPE_FILTERS.map((t) => t.key)))
  const [statusOn, setStatusOn] = useState<Set<TopoStatus>>(new Set(STATUS_FILTERS.map((s) => s.key)))
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const load = () => {
    setLoading(true)
    setError('')
    Promise.all([listConnectors(), listIntegrations(), listResources({ topology: true }), listAvailableActions()])
      .then(([c, i, r, a]) => {
        setConnectors(c ?? [])
        setIntegrations(i ?? [])
        setResources(r ?? [])
        setExecutions([])
        setActions(a ?? [])
      })
      .catch((err: ApiError) => setError(err.message || '加载失败'))
      .finally(() => setLoading(false))
  }
  useEffect(load, [])

  const full = useMemo(
    () => buildTopology(connectors, integrations, resources, actions, executions),
    [connectors, integrations, resources, actions, executions],
  )

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    const nodes = full.nodes.filter((n) => {
      if (!typeOn.has(n.data.entity)) return false
      if (!statusOn.has(n.data.status)) return false
      if (q && !(`${n.data.label} ${n.data.sublabel ?? ''} ${n.data.kind}`.toLowerCase().includes(q))) return false
      return true
    })
    const keep = new Set(nodes.map((n) => n.id))
    const edges = full.edges.filter((e) => keep.has(e.source) && keep.has(e.target))
    return { nodes, edges }
  }, [full, query, typeOn, statusOn])

  const selectedData: TopoNodeData | null = useMemo(() => {
    if (!selectedId) return null
    const n = full.nodes.find((x) => x.id === selectedId)
    return n ? n.data : null
  }, [selectedId, full])

  const exportText = useMemo(
    () => exportTopologyText(connectors, integrations, resources, actions, executions),
    [connectors, integrations, resources, actions, executions],
  )

  const toggle = <T,>(set: Set<T>, key: T, fn: (s: Set<T>) => void) => {
    const next = new Set(set)
    if (next.has(key)) next.delete(key); else next.add(key)
    fn(next)
  }

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={load} />

  if (connectors.length === 0) {
    return <EmptyState icon={Share2} title="尚无拓扑数据" text="接入第一个执行通道后，组织拓扑图谱会在此可视化展示。" />
  }

  return (
    <div className="view-stack">
      <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-surface p-3 dark:border-border-dark dark:bg-surface-dark">
        <div className="search-box">
          <Search size={16} />
          <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="搜索节点..." />
        </div>
        <div className="flex items-center gap-1.5">
          <span className="text-[10px] font-semibold uppercase tracking-wider text-content-subtle dark:text-content-dark-subtle">类型</span>
          {TYPE_FILTERS.map((t) => (
            <FilterChip key={t.key} active={typeOn.has(t.key)} onClick={() => toggle(typeOn, t.key, setTypeOn)}>{t.label}</FilterChip>
          ))}
        </div>
        <div className="flex items-center gap-1.5">
          <span className="text-[10px] font-semibold uppercase tracking-wider text-content-subtle dark:text-content-dark-subtle">状态</span>
          {STATUS_FILTERS.map((s) => (
            <FilterChip key={s.key} active={statusOn.has(s.key)} onClick={() => toggle(statusOn, s.key, setStatusOn)}>
              <span className={`mr-1 inline-block h-1.5 w-1.5 rounded-full ${s.dot}`} />{s.label}
            </FilterChip>
          ))}
        </div>
        <div className="ml-auto flex items-center gap-2">
          <div className="hidden items-center gap-2 xl:flex" aria-label="生态图例">
            {DOMAIN_LEGEND.map((d) => (
              <span key={d.label} className="flex items-center gap-1 text-[10px] text-content-subtle dark:text-content-dark-subtle">
                <span className={`inline-block h-1.5 w-1.5 rounded-full ${d.dot}`} />{d.label}
              </span>
            ))}
          </div>
          <span className="text-xs text-content-subtle dark:text-content-dark-subtle">{filtered.nodes.length} 节点 · {filtered.edges.length} 连线</span>
          <button className="quiet-button" onClick={load}><RefreshCw size={15} />刷新</button>
          <TopologyExport text={exportText} />
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        <TopologyGraph
          rawNodes={filtered.nodes}
          rawEdges={filtered.edges}
          onNodeClick={setSelectedId}
          onNodeDoubleClick={(id) => {
            const n = full.nodes.find((x) => x.id === id)
            if (n?.data.detailPath) navigate(n.data.detailPath)
          }}
          height={600}
        />
        <div className="h-[600px]">
          <TopologyDetails data={selectedData} onClose={() => setSelectedId(null)} onNavigate={navigate} />
        </div>
      </div>
    </div>
  )
}
