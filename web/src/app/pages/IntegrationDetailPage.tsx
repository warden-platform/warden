import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from '../../shared/router'
import { ArrowLeft, Loader2, Trash2 } from 'lucide-react'
import type { ApiConnector, ApiError, ApiResource, Integration } from '../../shared/api'
import { deleteIntegration, listConnectors, listIntegrations, listResources, updateIntegration } from '../../shared/api'
import { mapEnvironment, mapIntegrationStatus, mapProtocolLabel } from '../../shared/types'
import { useApp } from '../components/Toast'
import { Badge } from '../components/Badge'
import { ErrorState, LoadingState } from '../components/States'
import { JobScopePicker } from '../components/JobScopePicker'
import { DashboardScopePicker } from '../components/DashboardScopePicker'
import { SchemaScopePicker } from '../components/SchemaScopePicker'
import { HuaweiServiceScopePicker } from '../components/HuaweiServiceScopePicker'
import { AliyunSLSScopePicker } from '../components/AliyunSLSScopePicker'
import { IntegrationForm } from '../components/IntegrationForm'
import { IntegrationProjectScopePanel } from '../components/IntegrationProjectScopePanel'
import { DatabaseQueryTemplatesPanel } from '../components/DatabaseQueryTemplatesPanel'
import { aliyunRegionLabel, cloudRegionLabel, resourceTypeLabel } from '../lib/labels'

type Tab = 'overview' | 'projects' | 'jobs' | 'templates' | 'resources' | 'settings'

// Full-page integration detail with 4 tabs. Replaces the old in-drawer
// integration management (which was cramped in the 480px connector drawer).
export function IntegrationDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { notify } = useApp()
  const [integration, setIntegration] = useState<Integration | null>(null)
  const [connector, setConnector] = useState<ApiConnector | null>(null)
  const [resources, setResources] = useState<ApiResource[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [tab, setTab] = useState<Tab>('overview')
  const [acting, setActing] = useState('')
  const [editing, setEditing] = useState(false)

  const load = () => {
    if (!id) return
    setLoading(true)
    setError('')
    Promise.all([listIntegrations(), listConnectors(), listResources()])
      .then(([ints, conns, res]) => {
        const match = (ints ?? []).find((i) => i.id === id)
        if (!match) {
          setError('未找到 Integration')
          return
        }
        setIntegration(match)
        setConnector((conns ?? []).find((c) => c.id === match.connector_id) ?? null)
        setResources((res ?? []).filter((r) => r.integration_id === id))
      })
      .catch((e: ApiError) => setError(e.message))
      .finally(() => setLoading(false))
  }
  useEffect(load, [id])

  const jobs = useMemo(() => resources.filter((r) => r.resource_type === 'jenkins_job').map((r) => r.name), [resources])
  const dashboards = useMemo(
    () =>
      resources
        .filter((r) => r.resource_type === 'grafana_dashboard')
        .map((r) => ({ uid: (r.metadata.uid as string) ?? r.name, title: r.name })),
    [resources],
  )
  const schemas = useMemo(() => resources.filter((r) => r.resource_type === 'database_schema').map((r) => r.name), [resources])
  const isDatabase = integration?.protocol === 'postgresql' || integration?.protocol === 'mysql'
  const isHuawei = integration?.protocol === 'huaweicloud'
  const isAliyunSLS = integration?.protocol === 'aliyun_sls'

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={load} />
  if (!integration || !connector) return <ErrorState message="未找到 Integration" onRetry={load} />

  const saveScope = (scope: {
    allowAll?: boolean
    allowedJobs?: string[]
    allowedDashboards?: string[]
    allowedSchemas?: string[]
    allowedServices?: string[]
    allowedProjects?: string[]
    regionPolicy?: { mode: 'single' | 'all' | 'allowlist'; regions: string[]; deniedRegions: string[] }
  }) => {
    setActing(integration.id)
    updateIntegration(connector.id, integration.id, {
      protocol: integration.protocol,
      name: integration.name,
      endpoint: integration.endpoint,
      username: integration.username ?? '',
      credentialRef: integration.credential_ref,
      projectId: integration.project_id ?? '',
      environment: integration.environment ?? '',
      scope,
    })
      .then((updated) => {
        setIntegration(updated)
        notify(
          `${integration.protocol === 'grafana' ? 'Dashboard' : isDatabase ? 'Schema' : isHuawei ? '云服务' : isAliyunSLS ? 'Region / Project' : 'Job'} 范围已更新，Connector 正在同步`,
        )
      })
      .catch((e: ApiError) => notify(`保存失败：${e.message}`, 'error'))
      .finally(() => setActing(''))
  }

  const toggleStatus = () => {
    setActing(integration.id)
    const next = integration.status === 'ACTIVE' ? 'DISABLED' : 'ACTIVE'
    const scope = integration.scope as {
      allowAll?: boolean
      allowedJobs?: string[]
      allowedDashboards?: string[]
      allowedSchemas?: string[]
      allowedServices?: string[]
      allowedProjects?: string[]
    }
    updateIntegration(connector.id, integration.id, {
      protocol: integration.protocol,
      name: integration.name,
      endpoint: integration.endpoint,
      username: integration.username ?? '',
      credentialRef: integration.credential_ref,
      projectId: integration.project_id ?? '',
      environment: integration.environment ?? '',
      scope,
      status: next,
    })
      .then((updated) => {
        setIntegration(updated)
        notify(next === 'ACTIVE' ? 'Integration 已启用' : 'Integration 已停用')
      })
      .catch((e: ApiError) => notify(`操作失败：${e.message}`, 'error'))
      .finally(() => setActing(''))
  }

  const remove = () => {
    if (!window.confirm(`确认删除 Integration ${integration.name}？`)) return
    setActing(integration.id)
    deleteIntegration(connector.id, integration.id)
      .then(() => {
        notify('Integration 已删除')
        navigate('/integrations')
      })
      .catch((e: ApiError) => notify(`删除失败：${e.message}`, 'error'))
      .finally(() => setActing(''))
  }

  const tabs: { id: Tab; label: string }[] = [
    { id: 'overview', label: '概览' },
    { id: 'projects', label: '项目归属' },
    { id: 'jobs', label: '范围' },
    ...(integration.protocol === 'mysql' ? [{ id: 'templates' as Tab, label: '查询模板' }] : []),
    { id: 'resources', label: '资源' },
    { id: 'settings', label: '设置' },
  ]

  const scopeCount =
    integration.protocol === 'grafana'
      ? ((integration.scope.allowedDashboards as string[] | undefined) ?? []).length
      : isDatabase
        ? ((integration.scope.allowedSchemas as string[] | undefined) ?? []).length
        : isHuawei
          ? ((integration.scope.allowedServices as string[] | undefined) ?? []).length
          : isAliyunSLS
            ? ((integration.scope.allowedProjects as string[] | undefined) ?? []).length
            : ((integration.scope.allowedJobs as string[] | undefined) ?? []).length
  const scopeLabel =
    integration.protocol === 'grafana' ? 'Dashboard' : isDatabase ? 'Schema' : isHuawei ? '云服务' : isAliyunSLS ? 'Project' : 'Job'
  const displayedResources = isHuawei ? resources.filter((resource) => resource.resource_type !== 'huawei_cloud_account') : resources
  const regionCount =
    isHuawei || isAliyunSLS ? new Set(resources.map((resource) => String(resource.metadata.region ?? '')).filter(Boolean)).size : 0
  const slsProjectCount = isAliyunSLS ? resources.filter((resource) => resource.resource_type === 'aliyun_sls_account').length : 0

  return (
    <div className="detail-page">
      <div className="detail-page-header">
        <button className="quiet-button" onClick={() => navigate('/integrations')}>
          <ArrowLeft size={16} />
          返回中间件
        </button>
        <div className="detail-page-title">
          <Badge tone="info">{mapProtocolLabel(integration.protocol)}</Badge>
          <h1>{integration.name}</h1>
          <Badge tone={integration.status === 'ACTIVE' ? 'success' : integration.status === 'FAILED' ? 'danger' : 'neutral'}>
            {mapIntegrationStatus(integration.status)}
          </Badge>
        </div>
        <div className="detail-page-subtitle">
          <span>
            {isHuawei || isAliyunSLS ? '种子 Region' : 'endpoint'}: <code>{integration.endpoint}</code>
          </span>
          <span className="dot">·</span>
          <span>执行通道: </span>
          <button className="link-button" onClick={() => navigate(`/connectors/${connector.id}`)}>
            {connector.name}
          </button>
          {integration.last_probe_at && (
            <>
              <span className="dot">·</span>
              <span>最近探测: {new Date(integration.last_probe_at).toLocaleString('zh-CN', { hour12: false })}</span>
            </>
          )}
        </div>
      </div>

      <div className="detail-tabs">
        {tabs.map((t) => (
          <button key={t.id} className={tab === t.id ? 'selected' : ''} onClick={() => setTab(t.id)}>
            {t.label}
          </button>
        ))}
      </div>

      <div className="detail-page-body">
        {tab === 'overview' && (
          <div className="detail-section">
            <div className="status-rows">
              <div>
                <span>Integration ID</span>
                <code>{integration.id}</code>
              </div>
              <div>
                <span>状态</span>
                <Badge tone={integration.status === 'ACTIVE' ? 'success' : 'warning'}>{mapIntegrationStatus(integration.status)}</Badge>
              </div>
              <div>
                <span>协议</span>
                <strong>{mapProtocolLabel(integration.protocol)}</strong>
              </div>
              <div>
                <span>默认环境</span>
                <Badge
                  tone={integration.environment === 'production' ? 'warning' : integration.environment === 'testing' ? 'info' : 'neutral'}
                >
                  {integration.environment
                    ? mapEnvironment(integration.environment)
                    : `继承执行通道（${mapEnvironment(connector.environment)}）`}
                </Badge>
              </div>
              <div>
                <span>{isHuawei ? 'IAM 接入身份' : isAliyunSLS ? 'RAM 接入身份' : '服务账号'}</span>
                <strong>{integration.username || (isHuawei || isAliyunSLS ? '未标注，可在设置中补充' : '-')}</strong>
              </div>
              <div>
                <span>{isHuawei || isAliyunSLS ? 'AK/SK 凭据' : 'Credential Ref'}</span>
                {isHuawei || isAliyunSLS ? <strong>已在执行通道配置（不展示明文）</strong> : <code>{integration.credential_ref}</code>}
              </div>
              {(isHuawei || isAliyunSLS) && (
                <div>
                  <span>可用 Region</span>
                  <strong>{regionCount} 个</strong>
                </div>
              )}
              {isAliyunSLS && (
                <div>
                  <span>已发现 SLS Project</span>
                  <strong>{slsProjectCount} 个</strong>
                </div>
              )}
              <div>
                <span>范围</span>
                <strong>{integration.scope.allowAll === true ? '全部允许' : `${scopeCount} 个 ${scopeLabel}`}</strong>
              </div>
              <div>
                <span>最近探测</span>
                <strong>
                  {integration.last_probe_at ? new Date(integration.last_probe_at).toLocaleString('zh-CN', { hour12: false }) : '尚未探测'}
                </strong>
                {integration.last_probe_at && (
                  <Badge tone={integration.last_error ? 'danger' : 'success'}>{integration.last_error ? '探活异常' : '探活正常'}</Badge>
                )}
              </div>
              {integration.last_error && (
                <div>
                  <span>最近错误</span>
                  <strong className="error-text">{integration.last_error}</strong>
                </div>
              )}
              {(() => {
                const ageDays = Math.floor((Date.now() - new Date(integration.updated_at).getTime()) / 86400000)
                return ageDays >= 90 ? (
                  <div>
                    <span>凭证提示</span>
                    <strong className="error-text">
                      配置已 {ageDays} 天未更新，建议轮换 {integration.credential_ref} 凭据并更新 Integration
                    </strong>
                  </div>
                ) : null
              })()}
            </div>
          </div>
        )}

        {tab === 'jobs' && (
          <div className="detail-section">
            {integration.protocol === 'grafana' ? (
              <DashboardScopePicker
                integration={integration}
                dashboards={dashboards}
                acting={acting === integration.id}
                onSave={saveScope}
              />
            ) : isDatabase ? (
              <SchemaScopePicker integration={integration} schemas={schemas} acting={acting === integration.id} onSave={saveScope} />
            ) : isHuawei ? (
              <HuaweiServiceScopePicker integration={integration} acting={acting === integration.id} onSave={saveScope} />
            ) : isAliyunSLS ? (
              <AliyunSLSScopePicker integration={integration} resources={resources} acting={acting === integration.id} onSave={saveScope} />
            ) : (
              <JobScopePicker integration={integration} jobs={jobs} acting={acting === integration.id} onSave={saveScope} />
            )}
          </div>
        )}

        {tab === 'projects' && (
          <div className="detail-section">
            <IntegrationProjectScopePanel
              integration={integration}
              onSaved={(updated, message) => {
                setIntegration(updated)
                notify(message)
                load()
              }}
            />
          </div>
        )}

        {tab === 'templates' && integration.protocol === 'mysql' && (
          <DatabaseQueryTemplatesPanel integration={integration} resources={resources} />
        )}

        {tab === 'resources' && (
          <div className="detail-section">
            {displayedResources.length === 0 ? (
              <div className="empty-inline">Connector 尚未发现该 Integration 的服务资源</div>
            ) : (
              <>
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>资源名称</th>
                        <th>类型</th>
                        <th>环境</th>
                        <th>状态</th>
                        <th>{isHuawei || isAliyunSLS ? '区域' : '命名空间/路径'}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {displayedResources.map((r) => (
                        <tr key={r.id}>
                          <td>
                            <strong>{r.name}</strong>
                          </td>
                          <td>{resourceTypeLabel(r.resource_type)}</td>
                          <td>{mapEnvironment(r.environment)}</td>
                          <td>
                            <Badge tone={r.status === 'ACTIVE' ? 'success' : 'warning'}>{r.status}</Badge>
                          </td>
                          <td>
                            {isHuawei
                              ? cloudRegionLabel(String(r.metadata.region ?? ''))
                              : isAliyunSLS
                                ? aliyunRegionLabel(String(r.metadata.region ?? ''))
                                : (r.namespace ?? '-')}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {isHuawei && <div className="empty-inline">{regionCount} 个区域作为执行范围管理，不再重复显示为云账号资源。</div>}
              </>
            )}
          </div>
        )}

        {tab === 'settings' && (
          <div className="detail-section">
            {editing ? (
              <IntegrationForm
                connector={connector}
                existing={integration}
                protocol={integration.protocol}
                onCancel={() => setEditing(false)}
                onSaved={(msg) => {
                  notify(msg)
                  setEditing(false)
                  load()
                }}
              />
            ) : (
              <>
                <div className="settings-row">
                  <div>
                    <strong>编辑连接信息</strong>
                    <span>修改 endpoint、服务账号、Credential Ref</span>
                  </div>
                  <button className="secondary-button" onClick={() => setEditing(true)}>
                    编辑
                  </button>
                </div>
                <div className="settings-row">
                  <div>
                    <strong>{integration.status === 'ACTIVE' ? '停用 Integration' : '启用 Integration'}</strong>
                    <span>{integration.status === 'ACTIVE' ? '暂停后该 Integration 的资源不可执行' : '恢复后资源可正常执行'}</span>
                  </div>
                  <button className="secondary-button" disabled={acting === integration.id} onClick={toggleStatus}>
                    {integration.status === 'ACTIVE' ? '停用' : '启用'}
                  </button>
                </div>
                <div className="settings-row danger">
                  <div>
                    <strong>删除 Integration</strong>
                    <span>删除后关联资源从目录移除，审计记录保留</span>
                  </div>
                  <button className="danger-button" disabled={acting === integration.id} onClick={remove}>
                    {acting === integration.id ? <Loader2 size={15} className="spin" /> : <Trash2 size={15} />}删除
                  </button>
                </div>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
