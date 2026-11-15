import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from '../../shared/router'
import { ArrowLeft, Check, Loader2, ShieldAlert, Trash2 } from 'lucide-react'
import type { ApiConnector, ApiError, ApiProject, ApiResource, Integration } from '../../shared/api'
import {
  archiveConnector,
  connectorHealthLabel,
  deleteConnector,
  isConnectorHealthy,
  listConnectors,
  listConnectorIntegrations,
  listProjects,
  listResources,
  setConnectorEnvironment,
  setConnectorProject,
  updateConnectorNamespaces,
  updateConnectorServices,
} from '../../shared/api'
import { connectorChannelPrefix, connectorTypeLabel, mapEnvironment, mapIntegrationStatus, mapProtocolLabel } from '../../shared/types'
import { useApp } from '../components/Toast'
import { Badge } from '../components/Badge'
import { ErrorState, LoadingState } from '../components/States'
import { resourceTypeLabel } from '../lib/labels'
import { useAutoRefresh } from '../lib/useAutoRefresh'

type Tab = 'overview' | 'integrations' | 'resources' | 'agent' | 'scope' | 'settings'

const connectorDeleteEnabled = import.meta.env.VITE_CONNECTOR_DELETE_ENABLED !== 'false'

// Full-page connector detail replacing the old 480px drawer. 5 tabs:
// overview, integrations (read-only), resources, scope (namespaces/services),
// settings (delete/archive).
export function ConnectorDetailPage({ mode = 'connector' }: { mode?: 'connector' | 'target' }) {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { notify } = useApp()
  const [connector, setConnector] = useState<ApiConnector | null>(null)
  const [integrations, setIntegrations] = useState<Integration[]>([])
  const [resources, setResources] = useState<ApiResource[]>([])
  const [projects, setProjects] = useState<ApiProject[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [tab, setTab] = useState<Tab>('overview')
  const [saving, setSaving] = useState(false)
  const [environment, setEnvironment] = useState<'development' | 'testing' | 'production'>('testing')
  const [projectId, setProjectId] = useState('')
  const [deletingId, setDeletingId] = useState('')
  const [now, setNow] = useState(() => Date.now())
  const targetMode = mode === 'target'
  useEffect(() => setTab('overview'), [id, mode])

  const load = () => {
    if (!id) return
    setLoading(true)
    setError('')
    Promise.all([listResources(), listConnectorIntegrations(id), listConnectors(), listProjects()])
      .then(([res, ints, conns, projs]) => {
        const c = (conns ?? []).find((item) => item.id === id)
        if (!c) {
          setError(targetMode ? '未找到主机或集群' : '未找到执行通道')
          return
        }
        setConnector(c)
        setIntegrations(ints ?? [])
        setResources((res ?? []).filter((r) => r.connector_id === id))
        setProjects(projs ?? [])
      })
      .catch((e: ApiError) => setError(e.message))
      .finally(() => setLoading(false))
  }
  useEffect(load, [id])
  // Keep the connector heartbeat fresh without reloading the heavier resource
  // and integration datasets or showing the full-page loading state.
  useAutoRefresh(
    () => {
      if (!id) return
      listConnectors()
        .then((items) => {
          const current = (items ?? []).find((item) => item.id === id)
          if (current) {
            setConnector(current)
          }
        })
        .catch(() => undefined)
        .finally(() => setNow(Date.now()))
    },
    15000,
    Boolean(id),
  )

  const linux = connector?.connector_type === 'linux'
  const network = connector?.connector_type === 'network'
  const configuredScope = useMemo(() => {
    if (!connector) return [] as string[]
    if (network) return []
    const raw = linux ? connector.config.allowedServices : connector.config.allowedNamespaces
    return Array.isArray(raw) ? (raw as string[]) : []
  }, [connector, linux, network])

  const [selected, setSelected] = useState<string[]>(configuredScope)
  const configuredContainers = useMemo(() => {
    const raw = connector?.config.allowedContainers
    return Array.isArray(raw) ? (raw as string[]) : []
  }, [connector])
  const [selectedContainers, setSelectedContainers] = useState<string[]>(configuredContainers)
  useEffect(() => setSelected(configuredScope), [configuredScope.join('\n')])
  useEffect(() => setSelectedContainers(configuredContainers), [configuredContainers.join('\n')])
  useEffect(() => {
    if (connector) {
      setEnvironment(connector.environment as typeof environment)
      setProjectId(connector.project_id ?? '')
    }
  }, [connector?.id, connector?.environment, connector?.project_id])

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={load} />
  if (!connector) return <ErrorState message={targetMode ? '未找到主机或集群' : '未找到执行通道'} onRetry={load} />

  const options = network ? [] : ((linux ? connector.available_services : connector.available_namespaces) ?? configuredScope)
  const containerOptions = connector.available_containers ?? configuredContainers
  const healthy = isConnectorHealthy(connector, now)
  const healthLabel = connectorHealthLabel(connector, now)
  const deleting = connector.deletion_status === 'DELETE_REQUESTED'
  const deleted = connector.deletion_status === 'DELETED'
  const deletionIdle = !deleting && !deleted
  const defaultProject = projects.find((project) => project.is_default)
  const connectorProjectName =
    projects.find((project) => project.id === connector.project_id)?.name ?? defaultProject?.name ?? '系统默认项目'
  const targetTypeLabel = connectorTypeLabel(connector.connector_type)
  const channelName = `${connectorChannelPrefix(connector.connector_type)}-${connector.id.slice(0, 8)}`
  const targetNoun = network ? '网络设备' : linux ? '主机' : '集群'
  const counts = resources.reduce<Record<string, number>>((current, resource) => {
    current[resource.resource_type] = (current[resource.resource_type] ?? 0) + 1
    return current
  }, {})
  const targetResourceSummary = network
    ? `${counts.network_device ?? 0} 台设备 · ${counts.network_interface ?? 0} 个接口 · ${counts.network_vlan ?? 0} 个 VLAN`
    : linux
      ? `${counts.systemd_service ?? 0} 个服务 · ${counts.docker_container ?? 0} 个容器`
      : `${counts.node ?? 0} 个节点 · ${(counts.deployment ?? 0) + (counts.statefulset ?? 0) + (counts.daemonset ?? 0)} 个工作负载`
  const saveScope = () => {
    if (!connector) return
    if (network) {
      notify('网络 Connector 的设备范围与接口变更开关在各 H3C Integration 中配置')
      return
    }
    setSaving(true)
    const update = linux
      ? updateConnectorServices(connector.id, selected, selectedContainers)
      : updateConnectorNamespaces(connector.id, selected)
    update
      .then(() => notify(linux ? '服务范围已更新，Linux Connector 正在热更新' : 'Namespace 范围已更新，Operator 正在调谐权限'))
      .catch((e: ApiError) => notify(`保存失败：${e.message}`))
      .finally(() => setSaving(false))
  }

  const saveEnvironment = () => {
    setSaving(true)
    setConnectorEnvironment(connector.id, environment)
      .then(() => {
        notify(`${targetTypeLabel}环境已更新为${mapEnvironment(environment)}`)
        load()
      })
      .catch((e: ApiError) => notify(`保存失败：${e.message}`))
      .finally(() => setSaving(false))
  }

  const saveProject = () => {
    setSaving(true)
    setConnectorProject(connector.id, projectId)
      .then(() => {
        notify(
          `${targetTypeLabel}默认项目已更新为${projects.find((project) => project.id === projectId)?.name ?? defaultProject?.name ?? '系统默认项目'}`,
        )
        load()
      })
      .catch((e: ApiError) => notify(`保存失败：${e.message}`))
      .finally(() => setSaving(false))
  }

  const remove = () => {
    if (!connector) return
    if (!window.confirm(`确认卸载 ${connector.name}？执行代理、凭据和专属权限将从该${targetNoun}清理，审计记录会保留。`)) return
    setDeletingId(connector.id)
    deleteConnector(connector.id)
      .then(() => {
        notify(`${connector.name} 已进入安全下线流程`)
        load()
      })
      .catch((e: ApiError) => notify(`移除失败：${e.message}`))
      .finally(() => setDeletingId(''))
  }

  const archive = () => {
    if (!connector) return
    const uninstallAction = network ? '卸载 Network Connector' : linux ? '卸载程序' : 'Helm 卸载'
    if (
      !window.confirm(
        `确认已经在目标${targetNoun}完成${uninstallAction}？确认后 ${connector.name} 将从主机与集群列表隐藏，历史审计仍会保留。`,
      )
    )
      return
    setDeletingId(connector.id)
    archiveConnector(connector.id)
      .then(() => {
        notify(`${connector.name} 已完成下线并归档`)
        navigate(targetMode ? '/targets' : '/connectors')
      })
      .catch((e: ApiError) => notify(`归档失败：${e.message}`))
      .finally(() => setDeletingId(''))
  }

  const tabs: { id: Tab; label: string }[] = targetMode
    ? [
        { id: 'overview', label: '概览' },
        { id: 'resources', label: '资源' },
        { id: 'integrations', label: '中间件' },
        { id: 'agent', label: '执行代理' },
        { id: 'settings', label: '设置' },
      ]
    : [
        { id: 'overview', label: '通道概览' },
        { id: 'scope', label: '能力与权限' },
        { id: 'settings', label: '维护' },
      ]

  return (
    <div className="detail-page">
      <div className="detail-page-header">
        <button className="quiet-button" onClick={() => navigate(targetMode ? '/targets' : '/connectors')}>
          <ArrowLeft size={16} />
          返回{targetMode ? '主机与集群' : '执行通道'}
        </button>
        <div className="detail-page-title">
          <h1>{targetMode ? connector.name : channelName}</h1>
          <Badge tone={healthy ? 'success' : 'warning'}>{healthLabel}</Badge>
        </div>
        <div className="detail-page-subtitle">
          <span>{targetMode ? targetTypeLabel : `${targetTypeLabel} Connector`}</span>
          <span className="dot">·</span>
          <span>{connectorProjectName}</span>
          <span className="dot">·</span>
          <span>{mapEnvironment(connector.environment)}</span>
          <span className="dot">·</span>
          <span>
            心跳 {connector.last_heartbeat ? new Date(connector.last_heartbeat).toLocaleTimeString('zh-CN', { hour12: false }) : '尚未上报'}
          </span>
          <span className="dot">·</span>
          <span>
            {targetMode ? '执行代理' : '服务目标'}：
            {targetMode ? (
              channelName
            ) : (
              <button className="link-button" onClick={() => navigate(`/targets/${connector.id}`)}>
                {connector.name}
              </button>
            )}
          </span>
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
              {!targetMode && (
                <div>
                  <span>执行通道 ID</span>
                  <code>{connector.id}</code>
                </div>
              )}
              <div>
                <span>{targetMode ? '目标状态' : '执行代理状态'}</span>
                <Badge tone={healthy ? 'success' : 'warning'}>{healthLabel}</Badge>
              </div>
              {!targetMode && (
                <div>
                  <span>版本</span>
                  <strong>{connector.version}</strong>
                </div>
              )}
              <div>
                <span>类型</span>
                <strong>{targetTypeLabel}</strong>
              </div>
              <div>
                <span>默认项目</span>
                <strong>{connectorProjectName}</strong>
              </div>
              <div>
                <span>环境</span>
                <strong>{mapEnvironment(connector.environment)}</strong>
              </div>
              {targetMode && (
                <div>
                  <span>{network ? '设备、接口与 VLAN' : linux ? '服务与容器' : '节点与工作负载'}</span>
                  <strong>{targetResourceSummary}</strong>
                </div>
              )}
              {!targetMode && (
                <div>
                  <span>最后心跳</span>
                  <strong>
                    {connector.last_heartbeat ? new Date(connector.last_heartbeat).toLocaleString('zh-CN', { hour12: false }) : '尚未上报'}
                  </strong>
                </div>
              )}
              {(deleting || deleted) && (
                <div>
                  <span>删除状态</span>
                  <Badge tone="warning">{connector.deletion_status}</Badge>
                </div>
              )}
            </div>
            {!targetMode && (
              <div className="detail-section">
                <h3>能力</h3>
                <div className="detail-tags">
                  {connector.capabilities.map((item) => (
                    <code key={item}>{item}</code>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}

        {tab === 'agent' && (
          <div className="detail-section">
            <div className="detail-section-head">
              <div>
                <h3>关联执行代理</h3>
                <small>执行代理安装在目标侧，负责资源发现和受控动作执行。</small>
              </div>
              <button className="link-button" onClick={() => navigate(`/connectors/${connector.id}`)}>
                打开执行通道详情
              </button>
            </div>
            <div className="status-rows">
              <div>
                <span>通道名称</span>
                <strong>{channelName}</strong>
              </div>
              <div>
                <span>Connector ID</span>
                <code>{connector.id}</code>
              </div>
              <div>
                <span>代理状态</span>
                <Badge tone={healthy ? 'success' : 'warning'}>{healthLabel}</Badge>
              </div>
              <div>
                <span>版本</span>
                <strong>{connector.version}</strong>
              </div>
              <div>
                <span>最后心跳</span>
                <strong>
                  {connector.last_heartbeat ? new Date(connector.last_heartbeat).toLocaleString('zh-CN', { hour12: false }) : '尚未上报'}
                </strong>
              </div>
            </div>
            {!network && (
              <div className="detail-section">
                <h3>能力</h3>
                <div className="detail-tags">
                  {connector.capabilities.map((item) => (
                    <code key={item}>{item}</code>
                  ))}
                </div>
              </div>
            )}
            {network ? (
              <div className="detail-section">
                <h3>网络设备能力</h3>
                <p>
                  Network Connector 通过内网访问已登记的 H3C 设备。发现和审计默认只读；交换机接口变更由 Integration
                  显式启用，并强制经过预检、审批和写后复核。
                </p>
                <div className="detail-tags">
                  {connector.capabilities.map((item) => (
                    <code key={item}>{item}</code>
                  ))}
                </div>
              </div>
            ) : (
              <div className="detail-section">
                <h3>{linux ? '允许访问的 systemd 服务' : '允许访问的 Namespace'}</h3>
                <div className="namespace-picker">
                  <div>
                    {options.map((item) => (
                      <label key={item}>
                        <input
                          type="checkbox"
                          checked={selected.includes(item)}
                          onChange={() =>
                            setSelected((current) =>
                              current.includes(item) ? current.filter((value) => value !== item) : [...current, item],
                            )
                          }
                        />
                        <span>{item}</span>
                      </label>
                    ))}
                  </div>
                  <small>权限范围只约束执行代理能够发现和操作的资源。</small>
                </div>
                {linux && containerOptions.length > 0 && (
                  <>
                    <h3>允许访问的 Docker 容器</h3>
                    <div className="namespace-picker">
                      <div>
                        {containerOptions.map((item) => (
                          <label key={item}>
                            <input
                              type="checkbox"
                              checked={selectedContainers.includes(item)}
                              onChange={() =>
                                setSelectedContainers((current) =>
                                  current.includes(item) ? current.filter((value) => value !== item) : [...current, item],
                                )
                              }
                            />
                            <span>{item.slice(0, 12)}</span>
                          </label>
                        ))}
                      </div>
                      <small>按稳定容器 ID 授权；同名重建不会继承权限。</small>
                    </div>
                  </>
                )}
                <button
                  className="primary-button"
                  style={{ marginTop: 12 }}
                  disabled={saving || (!linux && selected.length === 0)}
                  onClick={saveScope}
                >
                  {saving ? <Loader2 size={16} className="spin" /> : <Check size={16} />}保存代理权限
                </button>
              </div>
            )}
          </div>
        )}

        {tab === 'integrations' && (
          <div className="detail-section">
            <div className="detail-section-head">
              <h3>中间件（只读）</h3>
            </div>
            {integrations.length === 0 ? (
              <div className="empty-inline">该执行通道尚未接入任何中间件。接入新中间件请前往「中间件」页面。</div>
            ) : (
              <div className="integration-list">
                {integrations.map((ig) => (
                  <div className="integration-row" key={ig.id}>
                    <div className="integration-row-main">
                      <Badge tone="info">{mapProtocolLabel(ig.protocol)}</Badge>
                      <strong>{ig.name}</strong>
                      <code className="integration-endpoint">{ig.endpoint}</code>
                    </div>
                    <div className="integration-row-meta">
                      <Badge tone={ig.status === 'ACTIVE' ? 'success' : ig.status === 'FAILED' ? 'danger' : 'neutral'}>
                        {mapIntegrationStatus(ig.status)}
                      </Badge>
                      <button className="link-button" onClick={() => navigate(`/integrations/${ig.id}`)}>
                        详情
                      </button>
                    </div>
                  </div>
                ))}
              </div>
            )}
            <small className="empty-integrations">
              注：接入新中间件请前往「
              <button className="link-button" onClick={() => navigate('/integrations')}>
                中间件
              </button>
              」页面
            </small>
          </div>
        )}

        {tab === 'resources' && (
          <div className="detail-section">
            {resources.length === 0 ? (
              <div className="empty-inline">该{targetMode ? targetTypeLabel : '执行通道'}尚未发现资源</div>
            ) : (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>资源名称</th>
                      <th>类型</th>
                      <th>命名空间/路径</th>
                      <th>状态</th>
                    </tr>
                  </thead>
                  <tbody>
                    {resources.map((r) => (
                      <tr key={r.id}>
                        <td>
                          <strong>{r.name}</strong>
                        </td>
                        <td>{resourceTypeLabel(r.resource_type)}</td>
                        <td>{r.namespace ?? '-'}</td>
                        <td>
                          <Badge tone={r.status === 'ACTIVE' ? 'success' : 'warning'}>{r.status}</Badge>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        )}

        {tab === 'scope' && (
          <div className="detail-section">
            {network ? (
              <>
                <h3>网络设备能力</h3>
                <p>
                  网络 Connector 不使用 Kubernetes Namespace 或 Linux 服务范围。设备连接和变更开关在对应 H3C Integration
                  中配置；接口写操作始终需要审批。
                </p>
                <div className="detail-tags">
                  {connector.capabilities.map((item) => (
                    <code key={item}>{item}</code>
                  ))}
                </div>
              </>
            ) : (
              <>
                <h3>{linux ? '允许访问的 systemd 服务' : '允许访问的 Namespace'}</h3>
                <div className="namespace-picker">
                  <div>
                    {options.map((item) => (
                      <label key={item}>
                        <input
                          type="checkbox"
                          checked={selected.includes(item)}
                          onChange={() =>
                            setSelected((current) =>
                              current.includes(item) ? current.filter((value) => value !== item) : [...current, item],
                            )
                          }
                        />
                        <span>{item}</span>
                      </label>
                    ))}
                  </div>
                  <small>保存后 Connector 会自动收敛资源发现和执行范围。</small>
                </div>
                {linux && containerOptions.length > 0 && (
                  <>
                    <h3>允许访问的 Docker 容器</h3>
                    <div className="namespace-picker">
                      <div>
                        {containerOptions.map((item) => (
                          <label key={item}>
                            <input
                              type="checkbox"
                              checked={selectedContainers.includes(item)}
                              onChange={() =>
                                setSelectedContainers((current) =>
                                  current.includes(item) ? current.filter((value) => value !== item) : [...current, item],
                                )
                              }
                            />
                            <span>{item.slice(0, 12)}</span>
                          </label>
                        ))}
                      </div>
                      <small>按稳定容器 ID 授权；同名重建不会继承权限。</small>
                    </div>
                  </>
                )}
                <button
                  className="primary-button"
                  style={{ marginTop: 12 }}
                  disabled={saving || (!linux && selected.length === 0)}
                  onClick={saveScope}
                >
                  {saving ? <Loader2 size={16} className="spin" /> : <Check size={16} />}保存范围
                </button>
              </>
            )}
          </div>
        )}

        {tab === 'settings' && (
          <div className="detail-section">
            <div className="settings-row connector-environment-settings">
              <div>
                <strong>{targetMode ? `${targetTypeLabel}默认项目` : '服务目标默认项目'}</strong>
                <span>原生资源默认继承该项目；中间件继续使用自己的独立项目归属。</span>
              </div>
              <div className="settings-control">
                <label htmlFor="connector-project">
                  <span className="sr-only">目标默认项目</span>
                  <select id="connector-project" value={projectId} disabled={saving} onChange={(event) => setProjectId(event.target.value)}>
                    <option value="">系统默认项目{defaultProject ? `（${defaultProject.name}）` : ''}</option>
                    {projects
                      .filter((project) => !project.is_default)
                      .map((project) => (
                        <option key={project.id} value={project.id}>
                          {project.name}
                        </option>
                      ))}
                  </select>
                </label>
                <button className="primary-button" disabled={saving || projectId === (connector.project_id ?? '')} onClick={saveProject}>
                  {saving ? <Loader2 size={15} className="spin" /> : <Check size={15} />}保存项目
                </button>
              </div>
            </div>
            <div className="settings-row connector-environment-settings">
              <div>
                <strong>{targetMode ? `${targetTypeLabel}环境` : '服务目标环境'}</strong>
                <span>未单独覆盖的原生资源继承目标环境；中间件环境归属保持独立。</span>
                {environment === 'production' && (
                  <small className="settings-warning">
                    <ShieldAlert size={14} />
                    生产环境的高风险动作可能进入审批。
                  </small>
                )}
              </div>
              <div className="settings-control">
                <label htmlFor="connector-environment">
                  <span className="sr-only">目标环境</span>
                  <select
                    id="connector-environment"
                    value={environment}
                    disabled={saving}
                    onChange={(event) => setEnvironment(event.target.value as typeof environment)}
                  >
                    <option value="development">开发环境</option>
                    <option value="testing">测试环境</option>
                    <option value="production">生产环境</option>
                  </select>
                </label>
                <button className="primary-button" disabled={saving || environment === connector.environment} onClick={saveEnvironment}>
                  {saving ? <Loader2 size={15} className="spin" /> : <Check size={15} />}保存环境
                </button>
              </div>
            </div>
            {deleting && (
              <div className="connector-uninstall">
                <strong>正在移除执行代理</strong>
                <span>
                  {network ? 'Network Connector' : linux ? 'Linux 卸载流程' : 'Operator'}
                  正在删除执行代理、凭据和专属权限，完成后会显示卸载命令。
                </span>
                <div className="pairing-status">
                  <Loader2 size={18} className="spin" />
                  <span>等待清理完成</span>
                </div>
              </div>
            )}
            {deleted && (
              <div className="connector-uninstall">
                <strong>完成目标环境卸载</strong>
                <span>动态资源和凭据已清理，请执行卸载命令，然后确认完成。</span>
                <button className="primary-button" disabled={deletingId === connector.id} onClick={archive}>
                  {deletingId === connector.id ? <Loader2 size={16} className="spin" /> : <Check size={16} />}我已完成卸载
                </button>
              </div>
            )}
            {deletionIdle && (
              <div className="settings-row danger">
                <div>
                  <strong>{targetMode ? `卸载${targetTypeLabel}` : '移除执行代理'}</strong>
                  <span>
                    执行代理和专属权限将从目标{targetNoun}清理，审计记录会保留。清理完成后会显示
                    {network ? 'Network Connector 卸载' : linux ? '主机卸载' : ' Helm 卸载'}命令。
                  </span>
                </div>
                {connectorDeleteEnabled ? (
                  <button
                    className="danger-button"
                    disabled={deletingId === connector.id || connector.status === 'DISABLED'}
                    onClick={remove}
                  >
                    {deletingId === connector.id ? <Loader2 size={15} className="spin" /> : <Trash2 size={15} />}移除
                  </button>
                ) : (
                  <Badge tone="neutral">未启用</Badge>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
