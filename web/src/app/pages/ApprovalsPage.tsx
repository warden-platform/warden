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
import { useApiQuery } from '../lib/useApiQuery'

// Mission Control approval queue: each request is a sign-off dossier with the
// full context (who / why / scope / deadline) and a live countdown. Approving
// still only issues a short-lived, single-use Grant — never long-term rights.
export function ApprovalsPage() {
  const { notify, setPendingApprovals } = useApp()
  const [acting, setActing] = useState('')
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [now, setNow] = useState(() => Date.now())

  const { data, error, loading, reload } = useApiQuery(() => Promise.all([listAccessRequests('PENDING'), listExecutions()]), [], {
    intervalMs: autoRefresh ? 5000 : 0,
    onSuccess: ([reqs]) => setPendingApprovals((reqs ?? []).length),
  })
  const [requests, executions] = data ?? [[], []]

  // 1s ticker for the sign-off countdowns.
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [])

  // Recent finished executions per resource, so approvers see the target's
  // track record instead of deciding blind.
  const recentByResource = useMemo(() => {
    const m = new Map<string, ApiExecution[]>()
    for (const e of executions) {
      if (e.status !== 'SUCCEEDED' && e.status !== 'FAILED') continue
      const list = m.get(e.resourceId) ?? []
      if (list.length < 3) {
        list.push(e)
        m.set(e.resourceId, list)
      }
    }
    return m
  }, [executions])

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={() => reload()} />

  const decide = (approval: Approval, allowed: boolean) => {
    setActing(approval.id)
    const call = allowed ? approveRequest(approval.id) : rejectRequest(approval.id)
    call
      .then(() => {
        reload(false)
        notify(allowed ? '审批已通过，Connector 开始执行' : '审批已拒绝，授权申请已关闭')
      })
      .catch((e: ApiError) => notify(`操作失败：${e.message}`))
      .finally(() => setActing(''))
  }

  const countdown = (req: ApiAccessRequest) => {
    if (!req.expires_at) return null
    const secs = Math.max(0, Math.round((new Date(req.expires_at).getTime() - now) / 1000))
    return `${String(Math.floor(secs / 60)).padStart(2, '0')}:${String(secs % 60).padStart(2, '0')}`
  }

  return (
    <section className="panel">
      <div className="ops-ph">
        <span>Approval Queue · 待命队列{requests.length ? ` · ${requests.length} 单待签署` : ''}</span>
        {autoRefresh && (
          <span className="topbar-live">
            <i aria-hidden="true" />
            LIVE · 5s
          </span>
        )}
      </div>
      <div className="toolbar">
        <div className="auto-refresh-toggle">
          <label>
            <input type="checkbox" checked={autoRefresh} onChange={(e) => setAutoRefresh(e.target.checked)} />
            <span>自动刷新（5秒）</span>
          </label>
        </div>
        <button className="quiet-button" onClick={() => reload()}>
          <Clock3 size={16} />
          刷新
        </button>
      </div>
      {!requests.length ? (
        <EmptyState
          icon={FileClock}
          title="没有待处理的审批"
          text="需要人工确认的生产动作会出现在这里，并展示完整资源、参数和风险上下文。"
        />
      ) : (
        <div className="approval-list" style={{ padding: 16 }}>
          {requests.map((req) => {
            const approval = apiRequestToApproval(req)
            const recent = recentByResource.get(req.resource_id) ?? []
            const resourceOK = req.resource_status === 'ACTIVE'
            const queryCost = req.action_name === 'database.query.execute' ? req.approval_context : undefined
            const queryCostSummary = queryCost
              ? [
                  `LIMIT ${String(queryCost.limit ?? '-')}`,
                  `预估扫描 ${String(queryCost.estimatedRows ?? '-')} 行`,
                  `成本 ${String(queryCost.estimatedCost ?? '-')}`,
                  queryCost.fullScan === true ? '全表扫描' : '',
                  queryCost.usingTempTable === true ? '临时表' : '',
                  queryCost.usingFilesort === true ? '文件排序' : '',
                ]
                  .filter(Boolean)
                  .join(' · ')
              : ''
            const cd = countdown(req)
            return (
              <article className="approval-card" key={approval.id}>
                <div className="approval-top">
                  <div>
                    <span className="approval-eyebrow">
                      待签署 · Access Request
                      <Badge tone="danger">
                        {req.action_name === 'database.query.execute' ? '查询成本审批' : `${approval.risk}风险 · 受治理`}
                      </Badge>
                    </span>
                    <h2>
                      {approval.action} · {approval.resource}
                    </h2>
                    <p>{approval.reason}</p>
                  </div>
                  <span className="approval-countdown" title="申请过期倒计时">
                    <b>{cd ?? '不限制'}</b>
                    <small>{cd ? '后过期' : '有效期'}</small>
                  </span>
                </div>
                <dl className="approval-dossier">
                  <div>
                    <dt>申请 Agent</dt>
                    <dd>{approval.requestor}</dd>
                  </div>
                  <div>
                    <dt>目标资源</dt>
                    <dd>
                      {req.resource_type ? resourceTypeLabel(req.resource_type) : '资源'} ·{' '}
                      <Badge tone={resourceOK ? 'success' : 'warning'}>
                        {req.resource_status ? (resourceOK ? '正常' : req.resource_status) : '未知'}
                      </Badge>
                    </dd>
                  </div>
                  <div>
                    <dt>执行参数</dt>
                    <dd>
                      <code>{approval.parameters}</code>
                    </dd>
                  </div>
                  {queryCostSummary && (
                    <div>
                      <dt>查询预检</dt>
                      <dd>{queryCostSummary}</dd>
                    </div>
                  )}
                  <div>
                    <dt>授权范围</dt>
                    <dd>单资源 · 单次使用 · 批准后 15 分钟内有效</dd>
                  </div>
                </dl>
                {recent.length > 0 && (
                  <div className="approval-history">
                    <span className="approval-history-head">
                      <History size={14} />
                      该资源最近执行
                    </span>
                    {recent.map((e) => (
                      <span key={e.id} className="approval-history-item">
                        <Badge tone={e.status === 'SUCCEEDED' ? 'success' : 'danger'}>{mapExecutionStatus(e.status)}</Badge>
                        <code>{e.actionDisplayName || e.actionName || e.actionId}</code>
                        <small>
                          {new Date(e.createdAt).toLocaleString('zh-CN', {
                            month: '2-digit',
                            day: '2-digit',
                            hour: '2-digit',
                            minute: '2-digit',
                            hour12: false,
                          })}
                        </small>
                      </span>
                    ))}
                  </div>
                )}
                <div className="decision-note" style={{ marginTop: 8 }}>
                  <ShieldAlert size={18} />
                  <span>
                    {req.action_name === 'database.query.execute'
                      ? `查询预检触发成本门禁：${queryCostSummary || '成本信息不可用'}。审批仅对当前 previewHash、参数和资源生效；查询变化后必须重新预检。`
                      : '策略要求租户审批。审批通过只生成本次任务的限时 Grant，不会修改 Agent 的长期权限。'}
                  </span>
                </div>
                <div className="approval-actions">
                  <button className="danger-button" disabled={acting === approval.id} onClick={() => decide(approval, false)}>
                    <XCircle size={16} />
                    拒绝
                  </button>
                  <button className="primary-button" disabled={acting === approval.id} onClick={() => decide(approval, true)}>
                    {acting === approval.id ? <Loader2 size={16} className="spin" /> : <Check size={17} />}批准并执行
                  </button>
                </div>
              </article>
            )
          })}
        </div>
      )}
    </section>
  )
}