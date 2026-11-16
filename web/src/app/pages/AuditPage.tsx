import { useEffect, useState } from 'react'
import { ChevronDown, Fingerprint, ListFilter, Search, SlidersHorizontal } from 'lucide-react'
import type { ApiAuditEvent, ApiError } from '../../shared/api'
import { listAuditEvents } from '../../shared/api'
import { Badge } from '../components/Badge'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { apiAuditToView } from '../lib/mappers'

const CATEGORIES = ['全部', '执行', '审批', '授权', '连接器', '策略', '权限', '系统'] as const
const CATEGORY_QUERY: Record<string, string> = {
  执行: 'execution',
  审批: 'access_request',
  授权: 'grant',
  连接器: 'connector',
  策略: 'policy',
  权限: 'permission_drift',
  系统: 'system',
}

export function AuditPage() {
  const [events, setEvents] = useState<ApiAuditEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [category, setCategory] = useState<string>('全部')
  const [workspaceId, setWorkspaceId] = useState('')
  const [agentId, setAgentId] = useState('')
  const [taskId, setTaskId] = useState('')
  const [delegationTokenId, setDelegationTokenId] = useState('')
  const [advancedOpen, setAdvancedOpen] = useState(false)

  const load = (search = query, selectedCategory = category) => {
    setLoading(true)
    setError('')
    listAuditEvents({
      q: search.trim(),
      category: CATEGORY_QUERY[selectedCategory],
      workspaceId: workspaceId.trim(),
      agentId: agentId.trim(),
      taskId: taskId.trim(),
      delegationTokenId: delegationTokenId.trim(),
    })
      .then((items) => setEvents(items ?? []))
      .catch((e: ApiError) => setError(e.message || '加载失败'))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    const timer = window.setTimeout(() => load(query, category), 300)
    return () => window.clearTimeout(timer)
  }, [query, category, workspaceId, agentId, taskId, delegationTokenId]) // eslint-disable-line react-hooks/exhaustive-deps

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  const view = events.map(apiAuditToView)
  const list = view

  return (
    <section className="panel">
      <div className="ops-ph">
        <span>Audit Trail · 不可变审计时间线</span>
        <span className="font-mono text-[10px] text-content-subtle dark:text-content-dark-subtle">{list.length} 条事件</span>
      </div>
      <div className="toolbar">
        <div className="search-box">
          <Search size={17} />
          <input
            aria-label="搜索审计事件"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索事件、主体或 Trace ID"
          />
        </div>
        <div className="toolbar-actions">
          <label className="select-wrap">
            <span className="sr-only">分类</span>
            <select value={category} onChange={(e) => setCategory(e.target.value)}>
              {CATEGORIES.map((c) => (
                <option key={c}>{c}</option>
              ))}
            </select>
            <ChevronDown size={15} />
          </label>
          <button className={`quiet-button ${advancedOpen ? 'selected' : ''}`} onClick={() => setAdvancedOpen((current) => !current)}>
            <SlidersHorizontal size={16} />
            外部上下文
          </button>
          <button className="quiet-button" onClick={() => load()}>
            <ListFilter size={16} />
            刷新
          </button>
        </div>
      </div>
      {advancedOpen && (
        <div className="audit-filter-panel">
          <label>
            <span>Workspace ID</span>
            <input value={workspaceId} onChange={(e) => setWorkspaceId(e.target.value)} placeholder="workspace-prod" />
          </label>
          <label>
            <span>Agent ID</span>
            <input value={agentId} onChange={(e) => setAgentId(e.target.value)} placeholder="agent-release-01" />
          </label>
          <label>
            <span>Task ID</span>
            <input value={taskId} onChange={(e) => setTaskId(e.target.value)} placeholder="task-4821" />
          </label>
          <label>
            <span>Delegation Token ID</span>
            <input value={delegationTokenId} onChange={(e) => setDelegationTokenId(e.target.value)} placeholder="UUID" />
          </label>
          <button
            className="quiet-button"
            onClick={() => {
              setWorkspaceId('')
              setAgentId('')
              setTaskId('')
              setDelegationTokenId('')
            }}
          >
            清空
          </button>
        </div>
      )}
      {list.length === 0 ? (
        <EmptyState icon={Fingerprint} title="没有审计事件" text="受控动作的决策、授权和执行事件会在这里按时间倒序展示。" />
      ) : (
        <div className="audit-list">
          {list.map((item) => (
            <div className="audit-item" key={item.id}>
              <div className={`audit-dot audit-${item.result}`} />
              <span className="audit-time">{item.time}</span>
              <Badge
                tone={
                  item.result === '拒绝'
                    ? 'danger'
                    : item.result === '允许' || item.result === '完成'
                      ? 'success'
                      : item.result === '待处理'
                        ? 'warning'
                        : 'neutral'
                }
              >
                {item.category}
              </Badge>
              <div className="audit-copy">
                <strong>{item.event}</strong>
                <span>
                  {item.actor}
                  {(() => {
                    const context = (events.find((event) => event.id === item.id)?.metadata?.external_context ?? {}) as Record<
                      string,
                      unknown
                    >
                    const workspace = typeof context.workspaceId === 'string' ? context.workspaceId : ''
                    const task = typeof context.taskId === 'string' ? context.taskId : ''
                    return workspace || task ? ` · ${workspace}${task ? ` / ${task}` : ''}` : ''
                  })()}
                </span>
              </div>
              <code title={item.traceId}>{item.traceId}</code>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
