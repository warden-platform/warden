// Linux host domain: group linux_host + systemd_service + docker_container by
// host (connector). Host sections are collapsible; within each host, resources
// are sub-grouped by type. Type filter chips toggle visibility per resource type.
import { ChevronRight, Container, HardDrive, Search, Server } from 'lucide-react'
import { useMemo } from 'react'
import type { ReactNode } from 'react'
import type { ApiResource, ApiConnector } from '../../../shared/api'
import { EmptyState } from '../States'
import { ResourceCard } from './ResourceCard'
import { TypeFilterBar } from './TypeFilterBar'
import { useToggleSet } from '../../lib/useToggleSet'

export function LinuxResources({
  resources,
  connectors,
  query,
  onAction,
  onProject,
}: {
  resources: ApiResource[]
  connectors: ApiConnector[]
  query: string
  onAction: (r: ApiResource) => void
  onProject: (r: ApiResource) => void
}) {
  const connectorName = (id: string) => connectors.find((c) => c.id === id)?.name ?? ''
  const q = query.trim().toLowerCase()
  const searched = useMemo(
    () => (q ? resources.filter((r) => (r.name + r.resource_type + connectorName(r.connector_id)).toLowerCase().includes(q)) : resources),
    [resources, q, connectorName],
  )

  const [hiddenTypes, toggleType] = useToggleSet()
  const filtered = hiddenTypes.size === 0 ? searched : searched.filter((r) => !hiddenTypes.has(r.resource_type))

  const typeChips = [
    {
      type: 'linux_host',
      label: '主机',
      icon: <HardDrive size={14} />,
      count: searched.filter((r) => r.resource_type === 'linux_host').length,
    },
    {
      type: 'systemd_service',
      label: 'systemd 服务',
      icon: <Server size={14} />,
      count: searched.filter((r) => r.resource_type === 'systemd_service').length,
    },
    {
      type: 'docker_container',
      label: 'Docker 容器',
      icon: <Container size={14} />,
      count: searched.filter((r) => r.resource_type === 'docker_container').length,
    },
  ]

  const [collapsedHosts, toggleHost] = useToggleSet(() => {
    const ids = new Set<string>()
    for (const r of resources) ids.add(r.connector_id)
    return ids
  })
  // Type sub-group collapse within each host. Keyed by `${connectorId}:${type}`.
  const [collapsedTypes, toggleTypeInHost] = useToggleSet(() => {
    const collapsed = new Set<string>()
    for (const r of resources) collapsed.add(`${r.connector_id}:${r.resource_type}`)
    return collapsed
  })

  // Group filtered results by connector (sorted: hosts with linux_host first, then by name).
  const byConnector = useMemo(() => {
    const m = new Map<string, ApiResource[]>()
    for (const r of filtered) {
      ;(m.get(r.connector_id) ?? m.set(r.connector_id, []).get(r.connector_id)!).push(r)
    }
    return [...m.entries()].sort(([ida, ra], [idb, rb]) => {
      const ha = ra.some((r) => r.resource_type === 'linux_host') ? 0 : 1
      const hb = rb.some((r) => r.resource_type === 'linux_host') ? 0 : 1
      if (ha !== hb) return ha - hb
      return connectorName(ida).localeCompare(connectorName(idb))
    })
  }, [filtered, connectorName])

  if (resources.length === 0) {
    return (
      <EmptyState
        icon={HardDrive}
        title="暂无 Linux 主机资源"
        text="接入 Linux 执行通道后，主机、systemd 服务与 Docker 容器会在此按主机分组展示。"
      />
    )
  }
  if (searched.length === 0) {
    return <EmptyState icon={Search} title="没有匹配的资源" text="尝试调整搜索条件。" />
  }

  const renderTypeSubgroup = (
    connId: string,
    type: string,
    label: string,
    icon: ReactNode,
    items: ApiResource[],
    cardIcon: ReactNode,
    meta?: (r: ApiResource) => ReactNode,
  ) => {
    if (items.length === 0) return null
    const key = `${connId}:${type}`
    const collapsed = collapsedTypes.has(key)
    return (
      <div key={type} className="subgroup">
        <button type="button" className="subgroup-title" onClick={() => toggleTypeInHost(key)} aria-expanded={!collapsed}>
          <ChevronRight size={12} className={`chevron${collapsed ? '' : ' chevron-open'}`} />
          <span>{label}</span>
          <small className="group-count">{items.length}</small>
        </button>
        {!collapsed && (
          <div className="res-list">
            {items.map((r) => (
              <ResourceCard
                key={r.id}
                resource={r}
                connectorName={connectorName(r.connector_id)}
                onAction={onAction}
                onProject={onProject}
                icon={cardIcon}
              >
                {meta?.(r)}
              </ResourceCard>
            ))}
          </div>
        )}
      </div>
    )
  }

  return (
    <div>
      <TypeFilterBar chips={typeChips} hiddenTypes={hiddenTypes} onToggle={toggleType} />
      {filtered.length === 0 ? (
        <EmptyState icon={Search} title="没有匹配的资源" text="当前类型筛选已隐藏所有结果，请启用至少一个类型。" />
      ) : (
        byConnector.map(([connId, items]) => {
          const host = items.find((r) => r.resource_type === 'linux_host')
          const hostName = host?.name ?? connectorName(connId) ?? connId.slice(0, 8)
          const collapsed = collapsedHosts.has(connId)
          const hosts = items.filter((r) => r.resource_type === 'linux_host')
          const services = items.filter((r) => r.resource_type === 'systemd_service')
          const containers = items.filter((r) => r.resource_type === 'docker_container')
          const others = items.filter(
            (r) => r.resource_type !== 'linux_host' && r.resource_type !== 'systemd_service' && r.resource_type !== 'docker_container',
          )
          return (
            <section key={connId} className="group-section">
              <button
                type="button"
                className="group-header group-header-button"
                onClick={() => toggleHost(connId)}
                aria-expanded={!collapsed}
              >
                <ChevronRight size={14} className={`chevron${collapsed ? '' : ' chevron-open'}`} />
                <Server size={14} />
                <span>{hostName}</span>
                <small className="group-count">{items.length}</small>
              </button>
              {!collapsed && (
                <>
                  {renderTypeSubgroup(connId, 'linux_host', '主机', <HardDrive size={12} />, hosts, <HardDrive size={20} />)}
                  {renderTypeSubgroup(
                    connId,
                    'systemd_service',
                    'systemd 服务',
                    <Server size={12} />,
                    services,
                    <Server size={20} />,
                    (r) => (
                      <div className="res-card-meta">
                        <span>类型</span>
                        <strong>systemd 服务</strong>
                      </div>
                    ),
                  )}
                  {renderTypeSubgroup(
                    connId,
                    'docker_container',
                    'Docker 容器',
                    <Container size={12} />,
                    containers,
                    <Container size={20} />,
                    (r) => (
                      <div className="res-card-meta">
                        <span>类型</span>
                        <strong>Docker 容器</strong>
                      </div>
                    ),
                  )}
                  {renderTypeSubgroup(connId, 'other', '其他', <HardDrive size={12} />, others, <HardDrive size={20} />)}
                </>
              )}
            </section>
          )
        })
      )}
    </div>
  )
}
