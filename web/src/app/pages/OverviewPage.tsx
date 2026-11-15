import { useEffect, useState } from 'react'
import { Activity, ArrowRight, Clock3, FileClock, Network, Plus, ShieldCheck } from 'lucide-react'
import type { ApiAccessRequest, ApiConnector, ApiError, ApiExecution, ApiResourceStats } from '../../shared/api'
import {
  canUser,
  connectorHealthLabel,
  getResourceStats,
  isConnectorHealthy,
  listAccessRequests,
  listConnectors,
  listExecutions,
} from '../../shared/api'
import { useNavigate } from '../../shared/router'
import { useApp } from '../components/Toast'
import { Kpi } from '../components/Kpi'
import { Badge } from '../components/Badge'
import { LoadingState, ErrorState, EmptyState } from '../components/States'
import { ExecutionTable } from '../components/ExecutionTable'
import { apiExecutionToView } from '../lib/mappers'
import { useAutoRefresh } from '../lib/useAutoRefresh'
import { connectorTypeLabel, mapActionName } from '../../shared/types'

// Overview: real-data KPIs + recent executions + pending approvals +
// connector health. No fake charts or hardcoded scores.
export function OverviewPage() {
  const navigate = useNavigate()
  const { setPendingApprovals, auth } = useApp()
  const [executions, setExecutions] = useState<ApiExecution[]>([])
  const [requests, setRequests] = useState<ApiAccessRequest[]>([])
  const [connectors, setConnectors] = useState<ApiConnector[]>([])
  const [stats, setStats] = useState<ApiResourceStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [now, setNow] = useState(() => Date.now())
  const canApprove = canUser(auth.user, 'pam:approve')
  const canManage = canUser(auth.user, 'pam:tenant:admin')

  const load = (showSpinner = true) => {
    if (showSpinner) setLoading(true)
    setError('')
    Promise.all([
      listExecutions(),
      canApprove ? listAccessRequests('PENDING') : Promise.resolve([]),
      canManage ? listConnectors() : Promise.resolve([]),
      getResourceStats(),
    ])
      .then(([execs, reqs, conns, resStats]) => {
        setExecutions(execs ?? [])
        setRequests(reqs ?? [])
        setConnectors(conns ?? [])
        setStats(resStats)
        setPendingApprovals((reqs ?? []).length)
        setNow(Date.now())
      })
      .catch((e: ApiError) => setError(e.message || '加载失败'))
      .finally(() => setLoading(false))
  }

  useEffect(() => load(), []) // eslint-disable-line react-hooks/exhaustive-deps
  useAutoRefresh(() => load(false), 15000)

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  const activeConnectors = connectors.filter((c) => isConnectorHealthy(c, now)).length
  const todayExecs = executions.filter((e) => new Date(e.createdAt).toDateString() === new Date().toDateString()).length
  const viewExecs = executions.slice(0, 6).map((e) => apiExecutionToView(e))

  const heartbeatAgo = (c: ApiConnector) => {
    if (!c.last_heartbeat) return '尚未心跳'
    const secs = Math.max(0, Math.round((Date.now() - new Date(c.last_heartbeat).getTime()) / 1000))
    return secs < 60 ? `${secs}秒前` : `${Math.round(secs / 60)}分钟前`
  }

  return (
    <div className="view-stack">
      <section className="kpi-grid">
        <Kpi label="今日受控动作" value={String(todayExecs)} detail={`总计 ${executions.length} 次`} tone="blue" icon={Activity} />
        <Kpi
          label="活跃资源"
          value={String(stats?.active ?? 0)}
          detail={`当前 ${stats?.current ?? stats?.total ?? 0} · 历史 ${stats?.history ?? 0}`}
          tone="green"
          icon={ShieldCheck}
        />
        <Kpi
          label="待处理审批"
          value={String(requests.length)}
          detail={requests.length ? '需要人工确认' : '当前无待处理'}
          tone="amber"
          icon={FileClock}
        />
        <Kpi
          label="执行通道"
          value={`${activeConnectors}/${connectors.length}`}
          detail={connectors.length - activeConnectors ? `${connectors.length - activeConnectors} 个离线或未激活` : '全部正常'}
          tone="red"
          icon={Network}
        />
      </section>

      <section className="panel">
        <div className="panel-header">
          <div>
            <h2>最近执行</h2>
            <p>所有 Agent 的受控动作</p>
          </div>
          <div className="header-actions">
            <button className="quiet-button" onClick={() => navigate('/executions')}>
              查看全部
              <ArrowRight size={15} />
            </button>
            <button className="primary-button compact" onClick={() => navigate('/executions')}>
              <Plus size={16} />
              新建执行
            </button>
          </div>
        </div>
        {viewExecs.length === 0 ? (
          <EmptyState icon={Activity} title="暂无执行记录" text="新提交的受控动作会出现在这里。" />
        ) : (
          <ExecutionTable executions={viewExecs} />
        )}
      </section>

      <section className="overview-grid">
        {canApprove && (
          <div className="panel">
            <div className="panel-header">
              <div>
                <h2>待处理审批</h2>
                <p>需要人工确认的生产动作</p>
              </div>
              <button className="quiet-button" onClick={() => navigate('/approvals')}>
                查看全部
                <ArrowRight size={15} />
              </button>
            </div>
            {requests.length === 0 ? (
              <div style={{ padding: 30, textAlign: 'center', color: '#85919a', fontSize: 11 }}>当前没有待处理的审批</div>
            ) : (
              <div className="approval-list">
                {requests.slice(0, 3).map((r) => {
                  return (
                    <div className="approval-mini" key={r.id}>
                      <div>
                        <Badge tone="danger">高风险</Badge>
                        <strong>{r.action_display_name || mapActionName(r.action_name || r.action_id)}</strong>
                        <span>{r.resource_name ?? r.resource_id.slice(0, 8)}</span>
                      </div>
                      <span className="approval-expire">
                        <Clock3 size={14} />
                        {r.expires_at ? `${Math.max(0, Math.round((new Date(r.expires_at).getTime() - Date.now()) / 60000))}分钟` : '不限'}
                      </span>
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        )}
        {canManage && (
          <div className="panel">
            <div className="panel-header">
              <div>
                <h2>执行通道健康</h2>
                <p>Connector 心跳状态</p>
              </div>
              <button className="quiet-button" onClick={() => navigate('/connectors')}>
                管理
                <ArrowRight size={15} />
              </button>
            </div>
            {connectors.length === 0 ? (
              <div style={{ padding: 30, textAlign: 'center', color: '#85919a', fontSize: 11 }}>尚未接入执行通道</div>
            ) : (
              <div className="connector-health-list">
                {connectors.map((c) => (
                  <div className="connector-health-row" key={c.id} onClick={() => navigate(`/connectors/${c.id}`)}>
                    <span className={`health-dot ${isConnectorHealthy(c, now) ? 'healthy' : 'down'}`} />
                    <strong>{c.name}</strong>
                    <Badge tone={isConnectorHealthy(c, now) ? 'success' : 'warning'}>{connectorHealthLabel(c, now)}</Badge>
                    <small>
                      {connectorTypeLabel(c.connector_type)} · {heartbeatAgo(c)}
                    </small>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </section>
    </div>
  )
}
