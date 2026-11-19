import { Network, Search, Server } from 'lucide-react'
import { useMemo } from 'react'
import type { ApiResource } from '../../../shared/api'
import { EmptyState } from '../States'
import { ResourceCard } from './ResourceCard'

export function NetworkResources({
  resources,
  connectorName,
  query,
  onAction,
  onProject,
}: {
  resources: ApiResource[]
  connectorName: (id: string) => string
  query: string
  onAction: (r: ApiResource) => void
  onProject: (r: ApiResource) => void
}) {
  const q = query.trim().toLowerCase()
  const searched = useMemo(
    () =>
      q
        ? resources.filter((r) =>
            `${r.name} ${r.resource_type} ${r.external_id} ${connectorName(r.connector_id)}`.toLowerCase().includes(q),
          )
        : resources,
    [resources, q, connectorName],
  )
  if (resources.length === 0)
    return (
      <EmptyState
        icon={Network}
        title="暂无网络设备资源"
        text="在 Network Connector 中创建 H3C Integration 并完成首次只读同步后，设备、接口和 VLAN 会显示在这里。"
      />
    )
  if (searched.length === 0) return <EmptyState icon={Search} title="没有匹配的资源" text="尝试调整搜索条件。" />
  const devices = searched.filter((r) => r.resource_type === 'network_device')
  const children = searched.filter((r) => r.resource_type !== 'network_device')
  return (
    <div>
      {devices.map((device) => {
        const related = children.filter((r) => r.external_id.startsWith(`${device.external_id}/`))
        return (
          <section key={device.id} className="group-section">
            <div className="group-header">
              <Server size={14} />
              <span>{device.name}</span>
              <small className="group-count">{related.length + 1}</small>
            </div>
            <div className="res-list">
              <ResourceCard
                resource={device}
                connectorName={connectorName(device.connector_id)}
                onAction={onAction}
                onProject={onProject}
                icon={<Network size={20} />}
              >
                <div className="res-card-meta">
                  <span>设备</span>
                  <strong>{String(device.metadata?.model ?? device.metadata?.role ?? 'H3C')}</strong>
                </div>
              </ResourceCard>
              {related.map((r) => (
                <ResourceCard
                  key={r.id}
                  resource={r}
                  connectorName={connectorName(r.connector_id)}
                  onAction={onAction}
                  onProject={onProject}
                  icon={<Network size={20} />}
                />
              ))}
            </div>
          </section>
        )
      })}
      {children
        .filter((r) => !devices.some((d) => r.external_id.startsWith(`${d.external_id}/`)))
        .map((r) => (
          <ResourceCard
            key={r.id}
            resource={r}
            connectorName={connectorName(r.connector_id)}
            onAction={onAction}
            onProject={onProject}
            icon={<Network size={20} />}
          />
        ))}
    </div>
  )
}
