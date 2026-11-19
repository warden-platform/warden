import { useMemo, useState } from 'react'
import { useNavigate } from '../../shared/router'
import { ArrowLeft, Boxes, Cloud, Database, LayoutDashboard, Network, ServerCog, ShieldCheck } from 'lucide-react'
import { listConnectors } from '../../shared/api'
import { useApp } from '../components/Toast'
import { Badge } from '../components/Badge'
import { ErrorState, LoadingState } from '../components/States'
import { IntegrationForm } from '../components/IntegrationForm'
import { connectorTypeLabel } from '../../shared/types'
import { useApiQuery } from '../lib/useApiQuery'

// Integration-centric creation: user fills connection info, then picks a
// connector (execution channel) to bind. Replaces the old flow where
// integrations were created from inside a connector's detail drawer.
export function IntegrationCreatePage() {
  const navigate = useNavigate()
  const { notify } = useApp()
  const [selectedConnectorId, setSelectedConnectorId] = useState('')
  const [protocol, setProtocol] = useState('jenkins')

  const connectorsQuery = useApiQuery(() => listConnectors(), [])
  const connectors = connectorsQuery.data ?? []
  const loading = connectorsQuery.loading
  const error = connectorsQuery.error
  const load = () => connectorsQuery.reload()

  const activeConnectors = useMemo(
    () =>
      connectors.filter(
        (c) => c.status === 'ACTIVE' && (protocol === 'h3c_comware_ssh' ? c.connector_type === 'network' : c.connector_type !== 'network'),
      ),
    [connectors, protocol],
  )
  // Derived selection: fall back to the first active connector whenever the
  // current choice is not in the filtered list (no sync effect needed).
  const effectiveConnectorId = activeConnectors.some((c) => c.id === selectedConnectorId)
    ? selectedConnectorId
    : (activeConnectors[0]?.id ?? '')
  const selectedConnector = useMemo(
    () => activeConnectors.find((c) => c.id === effectiveConnectorId) ?? activeConnectors[0],
    [effectiveConnectorId, activeConnectors],
  )

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={load} />

  if (activeConnectors.length === 0) {
    return (
      <div className="detail-page">
        <div className="detail-page-header">
          <button className="quiet-button" onClick={() => navigate('/integrations')}>
            <ArrowLeft size={16} />
            返回中间件
          </button>
        </div>
        <div className="empty-state">
          <div>
            <ServerCog size={28} />
          </div>
          <h2>没有可用的执行通道</h2>
          <p>请先接入一个 ACTIVE 状态的 Connector，再创建 Integration。</p>
          <button className="primary-button" style={{ marginTop: 14 }} onClick={() => navigate('/connectors')}>
            前往执行通道
          </button>
        </div>
      </div>
    )
  }

  const connectorSelector = (
    <div className="connector-selector">
      <span className="form-label">选择执行通道</span>
      <div className="connector-radio-list">
        {activeConnectors.map((c) => (
          <label key={c.id} className={`connector-radio ${effectiveConnectorId === c.id ? 'selected' : ''}`}>
            <input
              type="radio"
              name="connector"
              value={c.id}
              checked={effectiveConnectorId === c.id}
              onChange={() => setSelectedConnectorId(c.id)}
            />
            <div className="connector-radio-icon">
              {c.connector_type === 'network' ? (
                <ShieldCheck size={18} />
              ) : c.connector_type === 'linux' ? (
                <ServerCog size={18} />
              ) : (
                <Boxes size={18} />
              )}
            </div>
            <div className="connector-radio-info">
              <strong>{c.name}</strong>
              <span>
                {connectorTypeLabel(c.connector_type)} · {c.environment}
              </span>
            </div>
            <Badge tone="success">ACTIVE</Badge>
          </label>
        ))}
      </div>
      <small className="selector-hint">首次连接前还没有可选择的资源；创建时确认临时允许全部，资源同步后可在中间件详情页缩小范围。</small>
    </div>
  )

  return (
    <div className="detail-page">
      <div className="detail-page-header">
        <button className="quiet-button" onClick={() => navigate('/integrations')}>
          <ArrowLeft size={16} />
          返回中间件
        </button>
        <h1>接入中间件</h1>
      </div>
      <div className="detail-page-body">
        <div className="protocol-selector">
          <span className="form-label">协议</span>
          <div className="segmented">
            <button className={protocol === 'jenkins' ? 'selected' : ''} onClick={() => setProtocol('jenkins')}>
              <ServerCog size={15} />
              Jenkins
            </button>
            <button className={protocol === 'grafana' ? 'selected' : ''} onClick={() => setProtocol('grafana')}>
              <LayoutDashboard size={15} />
              Grafana
            </button>
            <button className={protocol === 'postgresql' ? 'selected' : ''} onClick={() => setProtocol('postgresql')}>
              <Database size={15} />
              PostgreSQL
            </button>
            <button className={protocol === 'mysql' ? 'selected' : ''} onClick={() => setProtocol('mysql')}>
              <Database size={15} />
              MySQL
            </button>
            <button className={protocol === 'huaweicloud' ? 'selected' : ''} onClick={() => setProtocol('huaweicloud')}>
              <Cloud size={15} />
              华为云
            </button>
            <button className={protocol === 'aliyun_sls' ? 'selected' : ''} onClick={() => setProtocol('aliyun_sls')}>
              <Cloud size={15} />
              阿里云 SLS
            </button>
            <button className={protocol === 'h3c_comware_ssh' ? 'selected' : ''} onClick={() => setProtocol('h3c_comware_ssh')}>
              <Network size={15} />
              H3C 网络设备
            </button>
          </div>
          <small className="selector-hint">
            {protocol === 'huaweicloud'
              ? '使用 IAM 用户 AK/SK 接入，自动发现 ECS、RDS、ELB、OBS，并支持有界 LTS 查询。'
              : protocol === 'aliyun_sls'
                ? '使用 RAM 用户 AK/SK 账号级接入，自动发现全部有权访问的 Region、Project 和 Logstore。'
                : protocol === 'h3c_comware_ssh'
                  ? '通过内网 Network Connector 发现和审计 H3C 交换机、AP、路由器等设备；交换机接口变更需显式启用，并经过预检、审批和写后复核。'
                  : '数据库接入发现实例、Schema、Table/View，并提供受控的单表只读行查询；不开放原始 SQL。'}
          </small>
        </div>
        {selectedConnector && (
          <IntegrationForm
            connector={selectedConnector}
            protocol={protocol}
            onCancel={() => navigate('/integrations')}
            onSaved={() => {
              notify('Integration 已创建')
              navigate('/integrations')
            }}
            extraContent={connectorSelector}
          />
        )}
      </div>
    </div>
  )
}
