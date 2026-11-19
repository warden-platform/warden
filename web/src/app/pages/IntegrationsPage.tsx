import { useMemo, useState } from 'react'
import { useNavigate } from '../../shared/router'
import { AlertTriangle, Boxes, CheckCircle2, ChevronDown, ListFilter, Plus, Search, ServerCog, Trash2 } from 'lucide-react'
import type { ApiError, Integration } from '../../shared/api'
import { deleteIntegration, listConnectors, listIntegrations, listResources } from '../../shared/api'
import { connectorTypeLabel, mapIntegrationStatus, mapProtocolLabel } from '../../shared/types'
import { useApp } from '../components/Toast'
import { Badge } from '../components/Badge'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { useApiQuery } from '../lib/useApiQuery'

// Top-level integrations list with a "+ 接入中间件" create entry. Each
// integration is a card showing the bound connector and discovered resource
// counts. Clicking a card opens the integration detail page.
export function IntegrationsPage() {
  const navigate = useNavigate()
  const { notify } = useApp()
  const [query, setQuery] = useState('')
  const [protocol, setProtocol] = useState('全部协议')
  const [deletingId, setDeletingId] = useState('')

  const integrationsQuery = useApiQuery(() => Promise.all([listIntegrations(), listConnectors(), listResources()]), [])
  const integrations = integrationsQuery.data?.[0] ?? []
  const connectors = integrationsQuery.data?.[1] ?? []
  const resources = integrationsQuery.data?.[2] ?? []
  const loading = integrationsQuery.loading
  const error = integrationsQuery.error
  const load = () => integrationsQuery.reload()

  const connectorName = useMemo(() => (id: string) => connectors.find((c) => c.id === id)?.name ?? id.slice(0, 8), [connectors])
  const connectorById = useMemo(() => (id: string) => connectors.find((c) => c.id === id), [connectors])

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={load} />

  const protocols = [...new Set(integrations.map((ig) => ig.protocol.toLowerCase()))].sort()
  const filtered = integrations.filter(
    (ig) =>
      (ig.name + ig.endpoint + connectorName(ig.connector_id)).toLowerCase().includes(query.toLowerCase()) &&
      (protocol === '全部协议' || ig.protocol.toLowerCase() === protocol.toLowerCase()),
  )
  const databaseCount = integrations.filter((ig) => ig.protocol === 'postgresql' || ig.protocol === 'mysql').length
  const activeCount = integrations.filter((ig) => ig.status === 'ACTIVE').length

  const remove = (ig: Integration) => {
    if (!window.confirm(`确认删除 Integration ${ig.name}？关联的资源会从目录移除。`)) return
    setDeletingId(ig.id)
    deleteIntegration(ig.connector_id, ig.id)
      .then(() => {
        notify('Integration 已删除')
        load()
      })
      .catch((e: ApiError) => notify(`删除失败：${e.message}`, 'error'))
      .finally(() => setDeletingId(''))
  }

  return (
    <div className="view-stack">
      <div className="summary-strip">
        <div>
          <ServerCog size={19} />
          <span>Integration 总数</span>
          <strong>{String(integrations.length)}</strong>
        </div>
        <div>
          <Boxes size={19} />
          <span>数据库集成</span>
          <strong>{String(databaseCount)}</strong>
        </div>
        <div>
          <CheckCircle2 size={19} />
          <span>运行中</span>
          <strong>{String(activeCount)}</strong>
        </div>
        <div>
          <AlertTriangle size={19} />
          <span>异常/停用</span>
          <strong>{String(integrations.length - activeCount)}</strong>
        </div>
      </div>
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button className="primary-button" onClick={() => navigate('/integrations/new')}>
          <Plus size={16} />
          接入中间件
        </button>
      </div>
      <section className="panel">
        <div className="ops-ph">
          <span>Integrations · 中间件</span>
          <span className="font-mono text-[10px] text-content-subtle dark:text-content-dark-subtle">
            {activeCount}/{integrations.length} 运行中
          </span>
        </div>
        <div className="toolbar">
          <div className="search-box">
            <Search size={17} />
            <input
              aria-label="搜索 Integration"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="搜索名称、Endpoint 或执行通道"
            />
          </div>
          <div className="toolbar-actions">
            <label className="select-wrap">
              <span className="sr-only">协议</span>
              <select value={protocol} onChange={(e) => setProtocol(e.target.value)}>
                <option>全部协议</option>
                {protocols.map((item) => (
                  <option key={item} value={item}>
                    {mapProtocolLabel(item)}
                  </option>
                ))}
              </select>
              <ChevronDown size={15} />
            </label>
            <button className="quiet-button" onClick={load}>
              <ListFilter size={16} />
              刷新
            </button>
          </div>
        </div>
        {filtered.length === 0 ? (
          <EmptyState icon={ServerCog} title="没有匹配的 Integration" text="点击右上角「接入中间件」来接入 Jenkins 等目标服务。" />
        ) : (
          <div className="integration-card-list">
            {filtered.map((ig) => {
              const conn = connectorById(ig.connector_id)
              const igResources = resources.filter((r) => r.integration_id === ig.id)
              const jobCount = igResources.filter((r) => r.resource_type === 'jenkins_job').length
              const buildCount = igResources.filter((r) => r.resource_type === 'jenkins_build').length
              const dashboardCount = igResources.filter((r) => r.resource_type === 'grafana_dashboard').length
              const datasourceCount = igResources.filter((r) => r.resource_type === 'grafana_datasource').length
              const schemaCount = igResources.filter((r) => r.resource_type === 'database_schema').length
              const tableCount = igResources.filter((r) => r.resource_type === 'database_table').length
              const ecsCount = igResources.filter((r) => r.resource_type === 'huawei_ecs_instance').length
              const cloudOtherCount = igResources.filter((r) =>
                ['huawei_rds_instance', 'huawei_elb_loadbalancer', 'huawei_obs_bucket'].includes(r.resource_type),
              ).length
              const ltsStreamCount = igResources.filter((r) => r.resource_type === 'huawei_lts_log_stream').length
              const slsProjectCount = igResources.filter((r) => r.resource_type === 'aliyun_sls_account').length
              const logstoreCount = igResources.filter((r) => r.resource_type === 'aliyun_sls_logstore').length
              const slsRegionCount = new Set(igResources.map((r) => String(r.metadata.region ?? '')).filter(Boolean)).size
              const stats =
                ig.protocol === 'grafana'
                  ? [`${dashboardCount} dashboards`, `${datasourceCount} datasources`]
                  : ig.protocol === 'postgresql' || ig.protocol === 'mysql'
                    ? [`${schemaCount} schemas`, `${tableCount} tables`]
                    : ig.protocol === 'huaweicloud'
                      ? [`${ecsCount} ECS · ${ltsStreamCount} LTS 日志流`, `${cloudOtherCount} RDS/ELB/OBS`]
                      : ig.protocol === 'aliyun_sls'
                        ? [`${slsProjectCount} Project · ${logstoreCount} Logstore`, `${slsRegionCount} Region`]
                        : [`${jobCount} jobs`, `${buildCount} builds`]
              return (
                <article className="integration-detail-card" key={ig.id} onClick={() => navigate(`/integrations/${ig.id}`)}>
                  <div className="integration-detail-head">
                    <div className="integration-detail-title">
                      <Badge tone="info">{mapProtocolLabel(ig.protocol)}</Badge>
                      <strong>{ig.name}</strong>
                      <span className={`exec-led ${ig.status === 'ACTIVE' && !ig.last_error ? 'ok' : ig.status === 'FAILED' || ig.last_error ? 'deny' : 'wait'}`}>
                        <i aria-hidden="true" />
                        <Badge tone={ig.status === 'ACTIVE' ? 'success' : ig.status === 'FAILED' ? 'danger' : 'neutral'}>
                          {mapIntegrationStatus(ig.status)}
                        </Badge>
                      </span>
                      {ig.last_error && <Badge tone="danger">探活异常</Badge>}
                    </div>
                    <code className="integration-endpoint">{ig.endpoint}</code>
                  </div>
                  <div className="integration-detail-meta">
                    <span>执行通道：</span>
                    <button
                      className="link-button"
                      onClick={(e) => {
                        e.stopPropagation()
                        navigate(`/connectors/${ig.connector_id}`)
                      }}
                    >
                      {connectorName(ig.connector_id)}
                    </button>
                    {conn && (
                      <Badge tone={conn.status === 'ACTIVE' ? 'success' : 'warning'}>{connectorTypeLabel(conn.connector_type)}</Badge>
                    )}
                    <span className="dot">·</span>
                    <span>{stats[0]}</span>
                    <span className="dot">·</span>
                    <span>{stats[1]}</span>
                    {ig.last_probe_at && (
                      <>
                        <span className="dot">·</span>
                        <span>探测 {new Date(ig.last_probe_at).toLocaleTimeString('zh-CN', { hour12: false })}</span>
                      </>
                    )}
                  </div>
                  <div className="integration-detail-actions">
                    <button
                      className="quiet-button"
                      onClick={(e) => {
                        e.stopPropagation()
                        navigate(`/integrations/${ig.id}`)
                      }}
                    >
                      管理
                    </button>
                    <button
                      className="icon-button"
                      aria-label="删除"
                      disabled={deletingId === ig.id}
                      onClick={(e) => {
                        e.stopPropagation()
                        remove(ig)
                      }}
                    >
                      <Trash2 size={15} />
                    </button>
                  </div>
                </article>
              )
            })}
          </div>
        )}
      </section>
    </div>
  )
}
