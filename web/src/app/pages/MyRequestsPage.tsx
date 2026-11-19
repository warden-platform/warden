import { useState } from 'react'
import { FileClock } from 'lucide-react'
import type { ApiAccessRequest } from '../../shared/api'
import { listMyAccessRequests } from '../../shared/api'
import { resourceTypeLabel } from '../lib/labels'
import { Badge } from '../components/Badge'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { useApiQuery } from '../lib/useApiQuery'

// Initiator-facing view: members track the access requests they raised without
// holding the approve capability. Approvers keep the sign-off queue separately.
const statusMeta: Record<ApiAccessRequest['status'], { label: string; tone: 'success' | 'warning' | 'danger' | 'info' | 'neutral' }> = {
  PENDING: { label: '等待审批', tone: 'warning' },
  APPROVED: { label: '已通过', tone: 'success' },
  REJECTED: { label: '已拒绝', tone: 'danger' },
  EXPIRED: { label: '已过期', tone: 'neutral' },
  CANCELLED: { label: '已取消', tone: 'neutral' },
}

const filters = [
  { key: '', label: '全部' },
  { key: 'PENDING', label: '等待审批' },
  { key: 'APPROVED', label: '已通过' },
  { key: 'REJECTED', label: '已拒绝' },
] as const

const fmt = (iso?: string) => (iso ? new Date(iso).toLocaleString('zh-CN', { hour12: false }) : '—')

export function MyRequestsPage() {
  const [status, setStatus] = useState('')
  const { data, error, loading, reload } = useApiQuery(() => listMyAccessRequests(status || undefined), [status], {
    intervalMs: 10000,
  })
  const requests = data ?? []

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={() => reload()} />

  return (
    <section className="panel">
      <div className="ops-ph">
        <span>
          My Requests · 我的申请{requests.length ? ` · ${requests.length} 条` : ''}
        </span>
        <span className="ops-ph-note">你发起的授权申请及其审批进度</span>
      </div>
      <div className="my-requests-filters" role="tablist" aria-label="按状态筛选">
        {filters.map((f) => (
          <button
            key={f.key}
            role="tab"
            aria-selected={status === f.key}
            className={`segment ${status === f.key ? 'segment-active' : ''}`}
            onClick={() => setStatus(f.key)}
          >
            {f.label}
          </button>
        ))}
      </div>
      {requests.length === 0 ? (
        <EmptyState
          icon={FileClock}
          title="暂无申请记录"
          text="当你对资源发起需要审批的操作时，申请会出现在这里，可随时跟踪审批进度。"
        />
      ) : (
        <div className="table-wrap">
          <table className="execution-table">
            <thead>
              <tr>
                <th>状态</th>
                <th>资源</th>
                <th>操作</th>
                <th>申请理由</th>
                <th>提交时间</th>
                <th>最近更新</th>
              </tr>
            </thead>
            <tbody>
              {requests.map((req) => {
                const meta = statusMeta[req.status] ?? { label: req.status, tone: 'neutral' as const }
                return (
                  <tr key={req.id}>
                    <td>
                      <Badge tone={meta.tone}>{meta.label}</Badge>
                    </td>
                    <td title={req.resource_name || req.resource_id}>
                      <strong>{req.resource_name || req.resource_id.slice(0, 8)}</strong>
                      {req.resource_type && <small className="cell-sub">{resourceTypeLabel(req.resource_type)}</small>}
                    </td>
                    <td title={req.action_name}>
                      {req.action_display_name || req.action_name || req.action_id.slice(0, 8)}
                      {req.risk_level && (
                        <small className="cell-sub">
                          风险：{{ low: '低', medium: '中', high: '高', critical: '极高' }[req.risk_level] ?? req.risk_level}
                        </small>
                      )}
                    </td>
                    <td className="cell-reason" title={req.reason}>
                      {req.reason || '—'}
                    </td>
                    <td className="mono">{fmt(req.created_at)}</td>
                    <td className="mono">{fmt(req.updated_at)}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}