import { useMemo, useState } from 'react'
import { ArrowRight, Boxes, Clock3, Plus, ServerCog, ShieldCheck } from 'lucide-react'
import { useNavigate } from '../../shared/router'
import type { ApiConnector } from '../../shared/api'
import { connectorHealthLabel, isConnectorHealthy, listConnectors, listProjects, listResources } from '../../shared/api'
import { mapEnvironment } from '../../shared/types'
import { Badge } from '../components/Badge'
import { OnboardModal } from '../components/OnboardModal'
import { NetworkConnectorProvisionModal } from '../components/NetworkConnectorProvisionModal'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { useApiQuery } from '../lib/useApiQuery'

type TargetType = 'kubernetes' | 'linux' | 'windows' | 'macos' | 'network'

function heartbeatAge(lastHeartbeat?: string, now = Date.now()) {
  if (!lastHeartbeat) return '尚未上报'
  const seconds = Math.max(0, Math.floor((now - Date.parse(lastHeartbeat)) / 1000))
  if (seconds < 60) return `${seconds} 秒前`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  return `${Math.floor(seconds / 3600)} 小时前`
}

export function TargetsPage() {
  const navigate = useNavigate()
  const [targetType, setTargetType] = useState<TargetType>('kubernetes')
  const [onboardType, setOnboardType] = useState<TargetType | null>(null)
  const [provisionNetwork, setProvisionNetwork] = useState(false)
  const [now, setNow] = useState(() => Date.now())

  // 资源/项目目录低频变化，挂载时拉一次；通道列表 15s 静默刷新并同步心跳时间戳。
  const catalogQuery = useApiQuery(() => Promise.all([listResources(), listProjects()]), [])
  const connectorsQuery = useApiQuery(() => listConnectors(), [], {
    intervalMs: 15000,
    onSuccess: () => setNow(Date.now()),
  })
  const resources = catalogQuery.data?.[0] ?? []
  const projects = catalogQuery.data?.[1] ?? []
  const connectors = connectorsQuery.data ?? []
  const loading = catalogQuery.loading || connectorsQuery.loading
  const error = catalogQuery.error || connectorsQuery.error
  const load = () => {
    catalogQuery.reload()
    connectorsQuery.reload()
  }

  const visible = connectors.filter((connector) => connector.connector_type === targetType)
  const defaultProject = projects.find((project) => project.is_default)
  const projectName = (connector: ApiConnector) =>
    projects.find((project) => project.id === connector.project_id)?.name ?? defaultProject?.name ?? '系统默认项目'
  const resourceCounts = useMemo(() => {
    const counts = new Map<string, Record<string, number>>()
    for (const resource of resources) {
      const current = counts.get(resource.connector_id) ?? {}
      current[resource.resource_type] = (current[resource.resource_type] ?? 0) + 1
      counts.set(resource.connector_id, current)
    }
    return counts
  }, [resources])

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  return (
    <div className="view-stack">
      <div className="target-toolbar panel">
        <div>
          <strong>管理被 Warden 接入的基础设施</strong>
          <span>主机、集群和网络设备是被管理对象；执行代理负责发现资源并执行受控动作。</span>
        </div>
        <div className="target-actions">
          <button className="secondary-button" onClick={() => setOnboardType('linux')}>
            <Plus size={16} />
            接入 Linux 主机
          </button>
          <button className="secondary-button" onClick={() => setOnboardType('windows')}>
            <Plus size={16} />
            接入 Windows 主机
          </button>
          <button className="secondary-button" onClick={() => setOnboardType('macos')}>
            <Plus size={16} />
            接入 macOS 主机
          </button>
          <button className="primary-button" onClick={() => setOnboardType('kubernetes')}>
            <Plus size={16} />
            接入 Kubernetes 集群
          </button>
          <button className="secondary-button" onClick={() => setProvisionNetwork(true)}>
            <Plus size={16} />
            配置 H3C 网络通道
          </button>
        </div>
      </div>
      <div className="segmented target-tabs" role="tablist" aria-label="目标类型">
        <button className={targetType === 'kubernetes' ? 'selected' : ''} onClick={() => setTargetType('kubernetes')}>
          <Boxes size={16} />
          Kubernetes 集群 <small>{connectors.filter((item) => item.connector_type === 'kubernetes').length}</small>
        </button>
        <button className={targetType === 'linux' ? 'selected' : ''} onClick={() => setTargetType('linux')}>
          <ServerCog size={16} />
          Linux 主机 <small>{connectors.filter((item) => item.connector_type === 'linux').length}</small>
        </button>
        <button className={targetType === 'windows' ? 'selected' : ''} onClick={() => setTargetType('windows')}>
          <ServerCog size={16} />
          Windows 主机 <small>{connectors.filter((item) => item.connector_type === 'windows').length}</small>
        </button>
        <button className={targetType === 'macos' ? 'selected' : ''} onClick={() => setTargetType('macos')}>
          <ServerCog size={16} />
          macOS 主机 <small>{connectors.filter((item) => item.connector_type === 'macos').length}</small>
        </button>
        <button className={targetType === 'network' ? 'selected' : ''} onClick={() => setTargetType('network')}>
          <ShieldCheck size={16} />
          网络设备 <small>{connectors.filter((item) => item.connector_type === 'network').length}</small>
        </button>
      </div>
      {visible.length === 0 ? (
        <EmptyState
          icon={targetType === 'linux' ? ServerCog : targetType === 'network' ? ShieldCheck : Boxes}
          title={`尚未接入${targetType === 'linux' ? ' Linux 主机' : targetType === 'network' ? '网络设备通道' : ' Kubernetes 集群'}`}
          text={
            targetType === 'network'
              ? '点击“配置 H3C 网络通道”，在能访问设备管理网的内网主机部署 Network Connector。'
              : '从上方接入入口安装执行代理并完成配对。'
          }
        />
      ) : (
        <div className="connector-grid">
          {visible.map((connector) => {
            const counts = resourceCounts.get(connector.id) ?? {}
            const linux = connector.connector_type === 'linux'
            const host = connector.connector_type === 'windows' || connector.connector_type === 'macos'
            const healthy = isConnectorHealthy(connector, now)
            const network = connector.connector_type === 'network'
            const summary = network
              ? `${counts.network_device ?? 0} 台设备 · ${counts.network_interface ?? 0} 个接口 · ${counts.network_vlan ?? 0} 个 VLAN`
              : linux
                ? `${counts.linux_host ?? 0} 台主机 · ${counts.systemd_service ?? 0} 个 systemd 服务 · ${counts.docker_container ?? 0} 个容器`
                : host
                  ? `${counts.host_machine ?? 0} 台主机 · 受控命令白名单`
                  : `${counts.node ?? 0} 个节点 · ${counts.namespace ?? 0} 个 Namespace · ${(counts.deployment ?? 0) + (counts.statefulset ?? 0) + (counts.daemonset ?? 0)} 个工作负载`
            return (
              <article className="connector-card target-card" key={connector.id}>
                <div className="connector-head">
                  <div className={`connector-logo ${network ? 'network' : linux || host ? 'jenkins' : 'kubernetes'}`}>
                    {network ? <ShieldCheck size={22} /> : linux || host ? <ServerCog size={22} /> : <Boxes size={22} />}
                  </div>
                  <div>
                    <h2>{connector.name}</h2>
                    <p>
                      {network
                        ? 'H3C 网络设备'
                        : linux
                          ? 'Linux 主机'
                          : connector.connector_type === 'windows'
                            ? 'Windows 主机'
                            : connector.connector_type === 'macos'
                              ? 'macOS 主机'
                              : 'Kubernetes 集群'}{' '}
                      · {mapEnvironment(connector.environment)} · {projectName(connector)}
                    </p>
                  </div>
                  <span className="flex items-center gap-2">
                    <span className={`health-dot ${healthy ? 'healthy' : 'down'}`} aria-hidden="true" />
                    <Badge tone={healthy ? 'success' : 'warning'}>{healthy ? '目标可管理' : '连接异常'}</Badge>
                  </span>
                </div>
                <div className="target-resource-counts">{summary}</div>
                <div className="target-agent-summary">
                  <ShieldCheck size={17} />
                  <div>
                    <strong>执行代理 · {connectorHealthLabel(connector, now)}</strong>
                    <span className="font-mono">
                      v{connector.version} · 最后心跳 {heartbeatAge(connector.last_heartbeat, now)}
                    </span>
                  </div>
                  <button className="link-button" onClick={() => navigate(`/connectors/${connector.id}`)}>
                    查看通道
                  </button>
                </div>
                <div className="connector-footer">
                  <span className="target-last-seen">
                    <Clock3 size={14} />
                    资源由关联执行代理持续同步
                  </span>
                  <button className="primary-button compact" onClick={() => navigate(`/targets/${connector.id}`)}>
                    查看与管理
                    <ArrowRight size={15} />
                  </button>
                </div>
              </article>
            )
          })}
        </div>
      )}
      {onboardType && onboardType !== 'network' && (
        <OnboardModal
          initialTargetType={onboardType}
          onClose={() => setOnboardType(null)}
          onComplete={(connectorId) => {
            setOnboardType(null)
            load()
            navigate(`/targets/${connectorId}`)
          }}
        />
      )}
      {provisionNetwork && (
        <NetworkConnectorProvisionModal projects={projects} onClose={() => setProvisionNetwork(false)} onComplete={() => load()} />
      )}
    </div>
  )
}
