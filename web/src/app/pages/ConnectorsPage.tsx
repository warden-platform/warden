import { useEffect, useState } from 'react'
import { Activity, AlertTriangle, ArrowRight, Boxes, CheckCircle2, Loader2, Network, ServerCog, ShieldCheck } from 'lucide-react'
import { useNavigate } from '../../shared/router'
import type { ApiConnector, ApiError, ConnectorCheckResponse } from '../../shared/api'
import { checkConnector, connectorHealthLabel, isConnectorHealthy, listConnectors } from '../../shared/api'
import { connectorChannelPrefix, connectorTypeLabel, mapEnvironment } from '../../shared/types'
import { Badge } from '../components/Badge'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { useApp } from '../components/Toast'
import { useAutoRefresh } from '../lib/useAutoRefresh'

const channelName = (connector: ApiConnector) => `${connectorChannelPrefix(connector.connector_type)}-${connector.id.slice(0, 8)}`

export function ConnectorsPage() {
  const navigate = useNavigate()
  const { notify } = useApp()
  const [connectors, setConnectors] = useState<ApiConnector[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [checkingId, setCheckingId] = useState('')
  const [checkResults, setCheckResults] = useState<Record<string, ConnectorCheckResponse>>({})
  const [now, setNow] = useState(() => Date.now())

  const load = () => {
    setLoading(true); setError('')
    listConnectors().then((items) => { setConnectors(items ?? []); setNow(Date.now()) })
      .catch((e: ApiError) => setError(e.message || '加载执行通道失败'))
      .finally(() => setLoading(false))
  }
  useEffect(load, [])
  useAutoRefresh(() => listConnectors().then((items) => setConnectors(items ?? [])).catch(() => undefined).finally(() => setNow(Date.now())), 15000)

  const runCheck = (connector: ApiConnector) => {
    setCheckingId(connector.id)
    checkConnector(connector.id).then((result) => setCheckResults((current) => ({ ...current, [connector.id]: result })))
      .catch((e: ApiError) => notify(`检查失败：${e.message}`)).finally(() => setCheckingId(''))
  }

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={load} />

  return <div className="view-stack">
    <div className="connector-explainer panel"><Network size={22} /><div><strong>执行通道是目标侧的受控技术代理</strong><span>主机、集群和网络设备是被管理对象；执行通道负责资源发现和动作执行。接入新目标请前往「主机与集群」。</span></div><button className="secondary-button" onClick={() => navigate('/targets')}>前往主机与集群<ArrowRight size={15} /></button></div>
    {connectors.length === 0 ? <EmptyState icon={Network} title="尚无执行通道" text="接入第一台主机、集群或网络设备后，系统会自动创建关联执行通道。" /> : <div className="connector-channel-list">
      {connectors.map((connector) => {
        const linux = connector.connector_type === 'linux'
        const network = connector.connector_type === 'network'
        const healthy = isConnectorHealthy(connector, now)
        const check = checkResults[connector.id]
        return <article className="connector-channel-row" key={connector.id}>
          <div className={`connector-logo ${network ? 'network' : linux ? 'jenkins' : 'kubernetes'}`}>{network ? <ShieldCheck size={21} /> : linux ? <ServerCog size={21} /> : <Boxes size={21} />}</div>
          <div className="channel-identity"><span>执行通道</span><strong>{channelName(connector)}</strong><code>{connector.id}</code></div>
          <div className="channel-target"><span>服务目标</span><button className="link-button" onClick={() => navigate(`/targets/${connector.id}`)}>{connector.name}</button><small>{connectorTypeLabel(connector.connector_type)} · {mapEnvironment(connector.environment)}</small></div>
          <div className="channel-health"><span>代理健康</span><Badge tone={healthy ? 'success' : 'warning'}>{connectorHealthLabel(connector, now)}</Badge><small>v{connector.version} · {connector.last_heartbeat ? new Date(connector.last_heartbeat).toLocaleString('zh-CN', { hour12: false }) : '尚未心跳'}</small></div>
          <div className="channel-actions">
            <button className="quiet-button" disabled={checkingId === connector.id} onClick={() => runCheck(connector)}>{checkingId === connector.id ? <Loader2 size={15} className="spin" /> : <Activity size={15} />}检查执行代理</button>
            <button className="primary-button compact" onClick={() => navigate(`/connectors/${connector.id}`)}>技术详情<ArrowRight size={15} /></button>
          </div>
          {check && <div className={`connector-check-result ${check.healthy ? 'healthy' : 'unhealthy'}`}><div>{check.healthy ? <CheckCircle2 size={17} /> : <AlertTriangle size={17} />}<strong>{check.healthy ? '连接正常' : '连接异常'}</strong></div><span>最后心跳 {check.heartbeatAgeSeconds >= 0 ? `${check.heartbeatAgeSeconds} 秒前` : '未知'} · 检查于 {new Date(check.checkedAt).toLocaleTimeString('zh-CN', { hour12: false })}</span></div>}
        </article>
      })}
    </div>}
  </div>
}
