import { useEffect, useMemo, useState } from 'react'
import { ChevronDown, Plus, SlidersHorizontal, TerminalSquare } from 'lucide-react'
import type { ApiAction, ApiError, ApiExecution } from '../../shared/api'
import { listAvailableActions, listExecutions } from '../../shared/api'
import { mapActionName, mapRiskLevel } from '../../shared/types'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { ExecutionTable } from '../components/ExecutionTable'
import { ActionPanel } from '../components/ActionPanel'
import { apiExecutionToView } from '../lib/mappers'
import { useAutoRefresh } from '../lib/useAutoRefresh'

const STATUS_FILTERS = ['全部', '执行中', '成功', '失败', '等待审批'] as const
const statusMap: Record<string, string> = {
  '执行中': '执行中', '成功': '成功', '失败': '失败', '等待审批': '等待审批',
}

export function ExecutionsPage() {
  const [executions, setExecutions] = useState<ApiExecution[]>([])
  const [actions, setActions] = useState<ApiAction[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('全部')
  const [resourceType, setResourceType] = useState('全部类型')
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [panelOpen, setPanelOpen] = useState(false)

  const load = () => {
    setLoading(true)
    setError('')
    Promise.all([listExecutions(), listAvailableActions()])
      .then(([execs, acts]) => {
        setExecutions(execs ?? [])
        setActions(acts ?? [])
      })
      .catch((e: ApiError) => setError(e.message || '加载失败'))
      .finally(() => setLoading(false))
  }

  useEffect(load, [])

  useAutoRefresh(() => {
    listExecutions().then((execs) => {
      // Skip the state update (and full table re-render) when nothing changed.
      setExecutions((prev) => {
        const next = execs ?? []
        if (prev.length === next.length && prev.every((p, i) => p.id === next[i].id && p.updatedAt === next[i].updatedAt)) return prev
        return next
      })
    }).catch(() => undefined)
  }, 5000, autoRefresh)

  const actionByID = useMemo(() => new Map(actions.map((a) => [a.id, a])), [actions])
  const resourceTypes = useMemo(() => [...new Set(executions.map((e) => e.resourceType).filter(Boolean))].sort() as string[], [executions])

  const filtered = useMemo(() => executions
    .filter((e) => resourceType === '全部类型' || e.resourceType === resourceType)
    .map((e) => {
      const v = apiExecutionToView(e)
      const act = actionByID.get(e.actionId)
      if (act) {
        v.action = mapActionName(act.name)
        v.risk = mapRiskLevel(act.risk_level)
      }
      return v
    })
    .filter((item) => filter === '全部' || item.status === statusMap[filter]), [executions, actionByID, filter, resourceType])

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={load} />

  return <section className="panel">
    <div className="toolbar">
      <div className="segmented" role="group" aria-label="执行状态筛选">
        {STATUS_FILTERS.map((item) => <button key={item} className={filter === item ? 'selected' : ''} onClick={() => setFilter(item)}>{item}</button>)}
      </div>
      <div className="toolbar-actions">
        <label className="select-wrap"><span className="sr-only">资源类型</span><select value={resourceType} onChange={(e) => setResourceType(e.target.value)}><option>全部类型</option>{resourceTypes.map((rt) => <option key={rt} value={rt}>{rt}</option>)}</select><ChevronDown size={15} /></label>
        <label className="auto-refresh-toggle compact"><input type="checkbox" checked={autoRefresh} onChange={(e) => setAutoRefresh(e.target.checked)} /><span>自动刷新</span></label>
        <button className="quiet-button" onClick={load}><SlidersHorizontal size={16} />刷新</button>
        <button className="primary-button" onClick={() => setPanelOpen(true)}><Plus size={16} />新建执行</button>
      </div>
    </div>
    {filtered.length === 0 ? <EmptyState icon={TerminalSquare} title="没有执行记录" text="新提交的受控动作会出现在这里。" /> : <ExecutionTable executions={filtered} />}
    {panelOpen && <ActionPanel initialResource={null} onClose={() => setPanelOpen(false)} />}
  </section>
}
