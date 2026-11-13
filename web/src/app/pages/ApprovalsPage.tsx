import { useEffect, useMemo, useState } from 'react'
import { Check, Clock3, FileClock, History, Loader2, ShieldAlert, XCircle } from 'lucide-react'
import type { ApiAccessRequest, ApiError, ApiExecution } from '../../shared/api'
import { approveRequest, listAccessRequests, listExecutions, rejectRequest } from '../../shared/api'
import type { Approval } from '../../shared/types'
import { mapExecutionStatus } from '../../shared/types'
import { resourceTypeLabel } from '../lib/labels'
import { useApp } from '../components/Toast'
import { Badge } from '../components/Badge'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { apiRequestToApproval } from '../lib/mappers'
import { useAutoRefresh } from '../lib/useAutoRefresh'

export function ApprovalsPage() {
  const { notify, setPendingApprovals } = useApp()
  const [requests, setRequests] = useState<ApiAccessRequest[]>([])
  const [executions, setExecutions] = useState<ApiExecution[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [acting, setActing] = useState('')
  const [autoRefresh, setAutoRefresh] = useState(true)

  const load = (showSpinner = true) => {
    if (showSpinner) setLoading(true)
    setError('')
    Promise.all([listAccessRequests('PENDING'), listExecutions()])
      .then(([reqs, execs]) => {
        setRequests(reqs ?? [])
        setExecutions(execs ?? [])
        setPendingApprovals((reqs ?? []).length)
      })
      .catch((e: ApiError) => setError(e.message || '加载失败'))
      .finally(() => setLoading(false))
  }

  useEffect(() => load(), []) // eslint-disable-line react-hooks/exhaustive-deps

  useAutoRefresh(() => load(false), 5000, autoRefresh)

  // Recent finished executions per resource, so approvers see the target's
  // track record instead of deciding blind.
  const recentByResource = useMemo(() => {
    const m = new Map<string, ApiExecution[]>()
    for (const e of executions) {
      if (e.status !== 'SUCCEEDED' && e.status !== 'FAILED') continue
      const list = m.get(e.resourceId) ?? []
      if (list.length < 3) { list.push(e); m.set(e.resourceId, list) }
    }
    return m
  }, [executions])

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  const approvals = requests.map((r) => apiRequestToApproval(r))

  const decide = (approval: Approval, allowed: boolean) => {
    setActing(approval.id)
    const call = allowed ? approveRequest(approval.id) : rejectRequest(approval.id)
    call.then(() => {
      setRequests((items) => items.filter((r) => r.id !== approval.id))
      setPendingApprovals(Math.max(0, requests.length - 1))
      notify(allowed ? '审批已通过，Connector 开始执行' : '审批已拒绝，授权申请已关闭')
    }).catch((e: ApiError) => notify(`操作失败：${e.message}`))
      .finally(() => setActing(''))
  }

  return (
    <section className="panel">
      <div className="toolbar">
        <div className="auto-refresh-toggle">
          <label><input type="checkbox" checked={autoRefresh} onChange={(e) => setAutoRefresh(e.target.checked)} /><span>自动刷新（5秒）</span></label>
        </div>
        <button className="quiet-button" onClick={() => load()}><Clock3 size={16} />刷新</button>
      </div>
      {!approvals.length ? <EmptyState icon={FileClock} title="没有待处理的审批" text="需要人工确认的生产动作会出现在这里，并展示完整资源、参数和风险上下文。" /> : (
        <div className="approval-list">{requests.map((req) => {
          const approval = apiRequestToApproval(req)
          const recent = recentByResource.get(req.resource_id) ?? []
          const resourceOK = req.resource_status === 'ACTIVE'
          const queryCost = req.action_name === 'database.query.execute' ? req.approval_context : undefined
          const queryCostSummary = queryCost ? [
            `LIMIT ${String(queryCost.limit ?? '-')}`,
            `预估扫描 ${String(queryCost.estimatedRows ?? '-')} 行`,
            `成本 ${String(queryCost.estimatedCost ?? '-')}`,
            queryCost.fullScan === true ? '全表扫描' : '',
            queryCost.usingTempTable === true ? '临时表' : '',
            queryCost.usingFilesort === true ? '文件排序' : '',
          ].filter(Boolean).join(' · ') : ''
          return <article className="approval-card" key={approval.id}>
          <div className="approval-top"><div><Badge tone="danger">{req.action_name === 'database.query.execute' ? '查询成本审批' : `${approval.risk}风险 · 受治理`}</Badge><h2>{approval.action} · {approval.resource}</h2><p>{approval.reason}</p></div><span className="approval-expire"><Clock3 size={15} />{approval.expires}</span></div>
          <div className="approval-context">
            <div><span>申请 Agent</span><strong>{approval.requestor}</strong></div>
            <div><span>目标资源</span><strong>{req.resource_type ? resourceTypeLabel(req.resource_type) : '资源'} · <Badge tone={resourceOK ? 'success' : 'warning'}>{req.resource_status ? (resourceOK ? '正常' : req.resource_status) : '未知'}</Badge></strong></div>
            <div><span>执行参数</span><strong>{approval.parameters}</strong></div>
            <div><span>授权范围</span><strong>单资源 · 单次使用 · 15 分钟</strong></div>
          </div>
          {recent.length > 0 && (
            <div className="approval-history">
              <span className="approval-history-head"><History size={14} />该资源最近执行</span>
              {recent.map((e) => (
                <span key={e.id} className="approval-history-item">
                  <Badge tone={e.status === 'SUCCEEDED' ? 'success' : 'danger'}>{mapExecutionStatus(e.status)}</Badge>
                  <code>{e.actionDisplayName || e.actionName || e.actionId}</code>
                  <small>{new Date(e.createdAt).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })}</small>
                </span>
              ))}
            </div>
          )}
          <div className="decision-note"><ShieldAlert size={18} /><span>{req.action_name === 'database.query.execute' ? `查询预检触发成本门禁：${queryCostSummary || '成本信息不可用'}。审批仅对当前 previewHash、参数和资源生效；查询变化后必须重新预检。` : '策略要求租户审批。审批通过只生成本次任务的限时 Grant，不会修改 Agent 的长期权限。'}</span></div>
          <div className="approval-actions">
            <button className="danger-button" disabled={acting === approval.id} onClick={() => decide(approval, false)}><XCircle size={16} />拒绝</button>
            <button className="primary-button" disabled={acting === approval.id} onClick={() => decide(approval, true)}>{acting === approval.id ? <Loader2 size={16} className="spin" /> : <Check size={17} />}批准并执行</button>
          </div>
        </article>
        })}</div>
      )}
    </section>
  )
}
