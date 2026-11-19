import { useEffect, useMemo, useState } from 'react'
import { Activity, AlertTriangle, ArrowRight, FileClock, Network, Plus, ShieldCheck } from 'lucide-react'
import type { ApiConnector } from '../../shared/api'
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
import { LoadingState, ErrorState, EmptyState } from '../components/States'
import { apiExecutionToView } from '../lib/mappers'
import { EMPTY_ARRAY, useApiQuery } from '../lib/useApiQuery'
import { connectorTypeLabel, executionLedClass, executionStatusGroup, mapActionName } from '../../shared/types'

// Mission Control overview: a situation wall built only from real API data —
// hourly execution sparkline, status distribution, connector heartbeat wall.
// No fake charts or hardcoded scores.

function Spark({ values, tone = '' }: { values: number[]; tone?: string }) {
  const max = Math.max(1, ...values)
  return (
    <div className={`ops-spark ${tone}`} aria-hidden="true">
      {values.map((v, i) => (
        <i key={i} className={i === values.length - 1 ? 'hi' : ''} style={{ height: `${v ? Math.max(14, (v / max) * 100) : 8}%` }} />
      ))}
    </div>
  )
}

export function OverviewPage() {
  const navigate = useNavigate()
  const { setPendingApprovals, auth } = useApp()
  const [now, setNow] = useState(() => Date.now())
  const canApprove = canUser(auth.user, 'warden:approve')
  const canManage = canUser(auth.user, 'warden:tenant:admin')

  const overviewQuery = useApiQuery(
    () =>
      Promise.all([
        listExecutions(),
        canApprove ? listAccessRequests('PENDING') : Promise.resolve([]),
        canManage ? listConnectors() : Promise.resolve([]),
        getResourceStats(),
      ]),
    [canApprove, canManage],
    {
      intervalMs: 15000,
      onSuccess: ([, reqs], fetchedAt) => {
        setPendingApprovals((reqs ?? []).length)
        setNow(fetchedAt)
      },
    },
  )
  const executions = overviewQuery.data?.[0] ?? EMPTY_ARRAY
  const requests = overviewQuery.data?.[1] ?? EMPTY_ARRAY
  const connectors = overviewQuery.data?.[2] ?? EMPTY_ARRAY
  const stats = overviewQuery.data?.[3] ?? null
  const loading = overviewQuery.loading
  const error = overviewQuery.error
  const load = (showSpinner = true) => overviewQuery.reload(showSpinner)

  // 1s ticker drives the approval countdown and heartbeat ages.
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [])

  const hourly = useMemo(() => {
    const buckets = new Array(8).fill(0) as number[]
    const hourMs = 3600_000
    const currentHour = Math.floor(now / hourMs) * hourMs
    for (const e of executions) {
      const t = Date.parse(e.createdAt)
      if (!Number.isFinite(t)) continue
      const idx = 7 - Math.floor((currentHour - Math.floor(t / hourMs) * hourMs) / hourMs)
      if (idx >= 0 && idx < 8) buckets[idx] += 1
    }
    return buckets
  }, [executions, now])

  const statusCounts = useMemo(() => {
    const counts = { 成功: 0, 执行中: 0, 等待审批: 0, 失败: 0 }
    for (const e of executions) {
      const g = executionStatusGroup(e.status)
      if (g === 'success') counts['成功'] += 1
      else if (g === 'running') counts['执行中'] += 1
      else if (g === 'waiting') counts['等待审批'] += 1
      else if (g === 'failed') counts['失败'] += 1
    }
    return counts
  }, [executions])

  const resourceMix = useMemo(() => {
    const entries = Object.entries(stats?.byType ?? {}).sort((a, b) => b[1] - a[1])
    return entries.slice(0, 8).map(([, n]) => n)
  }, [stats])

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  const activeConnectors = connectors.filter((c) => isConnectorHealthy(c, now)).length
  const todayExecs = executions.filter((e) => new Date(e.createdAt).toDateString() === new Date(now).toDateString()).length
  const viewExecs = executions.slice(0, 8).map((e) => apiExecutionToView(e))
  const totalStatus = Math.max(1, executions.length)
  const firstPending = requests[0]
  const pendingCountdown = firstPending?.expires_at
    ? Math.max(0, Math.round((new Date(firstPending.expires_at).getTime() - now) / 1000))
    : null

  const heartbeatAgo = (c: ApiConnector) => {
    if (!c.last_heartbeat) return '尚未心跳'
    const secs = Math.max(0, Math.round((now - new Date(c.last_heartbeat).getTime()) / 1000))
    return secs < 60 ? `${secs}s 前` : `${Math.round(secs / 60)}m 前`
  }

  const fmtCountdown = (secs: number) => `${String(Math.floor(secs / 60)).padStart(2, '0')}:${String(secs % 60).padStart(2, '0')}`

  return (
    <div className="view-stack">
      <section className="ops-kpi-grid">
        <div className="ops-kpi tone-cyan">
          <div className="ops-kpi-head">
            <span>今日受控动作</span>
            <Activity />
          </div>
          <strong className="ops-kpi-value">{todayExecs}</strong>
          <span className="ops-kpi-detail">总计 {executions.length} 次 · 近 8 小时分布</span>
          <Spark values={hourly} />
        </div>
        <div className="ops-kpi tone-green">
          <div className="ops-kpi-head">
            <span>活跃资源</span>
            <ShieldCheck />
          </div>
          <strong className="ops-kpi-value">{stats?.active ?? 0}</strong>
          <span className="ops-kpi-detail">
            当前 {stats?.current ?? stats?.total ?? 0} · 历史 {stats?.history ?? 0}
          </span>
          {resourceMix.length > 0 ? <Spark values={resourceMix} tone="green" /> : <div className="ops-spark" aria-hidden="true" />}
        </div>
        <div className="ops-kpi tone-amber">
          <div className="ops-kpi-head">
            <span>待处理审批</span>
            <FileClock />
          </div>
          <strong className="ops-kpi-value">{canApprove ? requests.length : '—'}</strong>
          <span className="ops-kpi-detail">{!canApprove ? '无审批权限' : requests.length ? '需要人工确认' : '当前无待处理'}</span>
        </div>
        <div className={`ops-kpi ${connectors.length && activeConnectors < connectors.length ? 'tone-red' : 'tone-green'}`}>
          <div className="ops-kpi-head">
            <span>执行通道</span>
            <Network />
          </div>
          <strong className="ops-kpi-value">
            {canManage ? activeConnectors : '—'}
            {canManage && <small>/ {connectors.length}</small>}
          </strong>
          <span className="ops-kpi-detail">
            {!canManage ? '无管理权限' : connectors.length - activeConnectors ? `${connectors.length - activeConnectors} 个离线或未激活` : '全部正常'}
          </span>
          {canManage && connectors.length > 0 ? (
            <div className="ops-channel-strip" aria-hidden="true">
              {connectors.slice(0, 12).map((c) => (
                <i key={c.id} className={isConnectorHealthy(c, now) ? '' : 'off'} />
              ))}
            </div>
          ) : (
            <div className="ops-channel-strip" aria-hidden="true" />
          )}
        </div>
      </section>

      {canApprove && firstPending && (
        <section className="ops-alert" role="status">
          <AlertTriangle />
          <strong>待审批</strong>
          <span className="ops-alert-copy">
            {firstPending.action_display_name || mapActionName(firstPending.action_name || firstPending.action_id)} ·{' '}
            {firstPending.resource_name ?? firstPending.resource_id.slice(0, 8)}
            {requests.length > 1 ? ` · 另有 ${requests.length - 1} 单` : ''}
          </span>
          {pendingCountdown !== null && <span className="cd">{fmtCountdown(pendingCountdown)}</span>}
          <button className="primary-button" onClick={() => navigate('/approvals')}>
            处理
          </button>
        </section>
      )}

      <section className="overview-grid">
        <div className="panel">
          <div className="ops-ph">
            <span>Execution Stream · 执行流</span>
            <div className="header-actions">
              <button className="quiet-button" onClick={() => navigate('/executions')}>
                查看全部
                <ArrowRight size={14} />
              </button>
              <button className="primary-button compact" onClick={() => navigate('/executions')}>
                <Plus size={15} />
                新建执行
              </button>
            </div>
          </div>
          {viewExecs.length === 0 ? (
            <EmptyState icon={Activity} title="暂无执行记录" text="新提交的受控动作会出现在这里。" />
          ) : (
            <div>
              {viewExecs.map((e) => (
                <div className="stream-row" key={e.id} onClick={() => navigate('/executions')} title={`${e.resource} · ${e.actor}`}>
                  <span className={`stream-dot ${executionLedClass(e.statusKey)}`} />
                  <span className="stream-id">{e.id.slice(0, 8)}</span>
                  <span className="stream-act">{e.action}</span>
                  <span className="stream-who">{e.actor}</span>
                  <span className={`stream-st ${executionLedClass(e.statusKey)}`}>{e.status}</span>
                  <span className="stream-time">{e.createdAt}</span>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="view-stack">
          <div className="panel">
            <div className="ops-ph">
              <span>Status Distribution · 状态分布</span>
            </div>
            <div className="ops-funnel">
              {(
                [
                  ['成功', statusCounts['成功'], 'bg-green-500 dark:bg-signal-green'],
                  ['执行中', statusCounts['执行中'], 'bg-brand dark:bg-brand-dark'],
                  ['等待审批', statusCounts['等待审批'], 'bg-amber-500 dark:bg-signal-amber'],
                  ['失败 / 拒绝', statusCounts['失败'], 'bg-red-500 dark:bg-signal-red'],
                ] as const
              ).map(([label, count, color]) => (
                <div className="ops-frow" key={label}>
                  <div className="lbl">
                    <span>{label}</span>
                    <b>{count}</b>
                  </div>
                  <div className="ops-track">
                    <i className={color} style={{ width: `${(count / totalStatus) * 100}%` }} />
                  </div>
                </div>
              ))}
            </div>
          </div>

          {canManage && (
            <div className="panel">
              <div className="ops-ph">
                <span>Connector Heartbeat · 通道心跳</span>
                <button className="link-button" onClick={() => navigate('/connectors')}>
                  管理
                </button>
              </div>
              {connectors.length === 0 ? (
                <div className="empty-inline">尚未接入执行通道</div>
              ) : (
                <div className="ops-hb">
                  {connectors.map((c) => (
                    <div
                      className={`ops-hrow ${isConnectorHealthy(c, now) ? '' : 'off'}`}
                      key={c.id}
                      onClick={() => navigate(`/connectors/${c.id}`)}
                      title={`${connectorTypeLabel(c.connector_type)} · ${connectorHealthLabel(c, now)}`}
                    >
                      <i aria-hidden="true" />
                      <strong>{c.name}</strong>
                      <small>
                        {connectorHealthLabel(c, now)} · {heartbeatAgo(c)}
                      </small>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      </section>
    </div>
  )
}