// Grafana domain: instance, datasource, folder, dashboard as collapsible type
// sections. Connector (cluster) layer appears when 2+ Grafana connectors exist.
import { useMemo } from 'react'
import { ChevronRight, Database, Folder, LayoutDashboard, Search, Server } from 'lucide-react'
import type { ApiResource } from '../../../shared/api'
import { EmptyState } from '../States'
import { ResourceCard } from './ResourceCard'
import { TypeFilterBar } from './TypeFilterBar'
import { useToggleSet } from '../../lib/useToggleSet'

export function GrafanaResources({
  resources,
  connectorName,
  query,
  onAction,
  onProject,
}: {
  resources: ApiResource[]
  connectorName: (id: string) => string
  query: string
  onAction: (r: ApiResource, action?: string) => void
  onProject: (r: ApiResource) => void
}) {
  const q = query.trim().toLowerCase()
  const searched = useMemo(
    () =>
      q
        ? resources.filter((r) =>
            (r.name + r.resource_type + (r.metadata.uid ?? '') + connectorName(r.connector_id)).toLowerCase().includes(q),
          )
        : resources,
    [resources, q, connectorName],
  )

  const [hiddenTypes, toggleType] = useToggleSet()
  const filtered = hiddenTypes.size === 0 ? searched : searched.filter((r) => !hiddenTypes.has(r.resource_type))

  const typeChips = [
    {
      type: 'grafana_instance',
      label: '实例',
      icon: <Server size={14} />,
      count: searched.filter((r) => r.resource_type === 'grafana_instance').length,
    },
    {
      type: 'grafana_datasource',
      label: '数据源',
      icon: <Database size={14} />,
      count: searched.filter((r) => r.resource_type === 'grafana_datasource').length,
    },
    {
      type: 'grafana_folder',
      label: 'Folders',
      icon: <Folder size={14} />,
      count: searched.filter((r) => r.resource_type === 'grafana_folder').length,
    },
    {
      type: 'grafana_dashboard',
      label: 'Dashboards',
      icon: <LayoutDashboard size={14} />,
      count: searched.filter((r) => r.resource_type === 'grafana_dashboard').length,
    },
  ]

  const multiConnector = useMemo(() => {
    const ids = new Set<string>()
    for (const r of resources) ids.add(r.connector_id)
    return ids.size > 1
  }, [resources])

  const [collapsedConnectors, toggleConnector] = useToggleSet(() => {
    const ids = new Set<string>()
    for (const r of resources) ids.add(r.connector_id)
    return ids
  })
  const [collapsedSections, toggleSection] = useToggleSet(() => {
    const collapsed = new Set<string>()
    for (const r of resources) collapsed.add(`${r.connector_id}:${r.resource_type}`)
    return collapsed
  })

  const byConnector = useMemo(() => {
    const m = new Map<string, ApiResource[]>()
    for (const r of filtered) {
      ;(m.get(r.connector_id) ?? m.set(r.connector_id, []).get(r.connector_id)!).push(r)
    }
    return [...m.entries()].sort(([a], [b]) => connectorName(a).localeCompare(connectorName(b)))
  }, [filtered, connectorName])

  if (resources.length === 0) {
    return (
      <EmptyState
        icon={LayoutDashboard}
        title="暂无 Grafana 资源"
        text="接入 Grafana 中间件后，实例、Dashboard、Folder 与 Datasource 会在此分块展示。"
      />
    )
  }
  if (searched.length === 0) {
    return <EmptyState icon={Search} title="没有匹配的资源" text="尝试调整搜索条件。" />
  }

  const renderSections = (connId: string, items: ApiResource[]) => {
    const sections = [
      {
        key: 'instance',
        type: 'grafana_instance',
        label: '实例',
        icon: <Server size={14} />,
        items: items.filter((r) => r.resource_type === 'grafana_instance'),
        cardIcon: <Server size={20} />,
        meta: () => null,
      },
      {
        key: 'datasource',
        type: 'grafana_datasource',
        label: '数据源',
        icon: <Database size={14} />,
        items: items.filter((r) => r.resource_type === 'grafana_datasource'),
        cardIcon: <Database size={20} />,
        meta: (r: ApiResource) => <GrafanaCapabilities resource={r} onAction={onAction} />,
      },
      {
        key: 'folder',
        type: 'grafana_folder',
        label: 'Folders',
        icon: <Folder size={14} />,
        items: items.filter((r) => r.resource_type === 'grafana_folder'),
        cardIcon: <Folder size={20} />,
        meta: (r: ApiResource) => <GrafanaCapabilities resource={r} onAction={onAction} />,
      },
      {
        key: 'dashboard',
        type: 'grafana_dashboard',
        label: 'Dashboards',
        icon: <LayoutDashboard size={14} />,
        items: items.filter((r) => r.resource_type === 'grafana_dashboard'),
        cardIcon: <LayoutDashboard size={20} />,
        meta: (r: ApiResource) => (
          <>
            <div className="res-card-meta">
              <span>UID</span>
              <strong>{(r.metadata.uid as string) ?? r.name}</strong>
            </div>
            <div className="res-card-meta">
              <span>Folder</span>
              <strong>{(r.metadata.folderTitle as string) || 'General'}</strong>
            </div>
            <GrafanaCapabilities resource={r} onAction={onAction} />
          </>
        ),
      },
    ].filter((s) => s.items.length > 0)

    return sections.map((s) => {
      const sectionKey = `${connId}:${s.type}`
      const collapsed = collapsedSections.has(sectionKey)
      return (
        <section key={sectionKey} className="group-section">
          <button
            type="button"
            className="group-header group-header-button"
            onClick={() => toggleSection(sectionKey)}
            aria-expanded={!collapsed}
          >
            <ChevronRight size={14} className={`chevron${collapsed ? '' : ' chevron-open'}`} />
            {s.icon}
            <span>{s.label}</span>
            <small className="group-count">{s.items.length}</small>
          </button>
          {!collapsed && (
            <div className="res-list">
              {s.items.map((r) => (
                <ResourceCard
                  key={r.id}
                  resource={r}
                  connectorName={connectorName(r.connector_id)}
                  onAction={onAction}
                  onProject={onProject}
                  icon={s.cardIcon}
                >
                  {s.meta(r)}
                </ResourceCard>
              ))}
            </div>
          )}
        </section>
      )
    })
  }

  return (
    <div>
      <TypeFilterBar chips={typeChips} hiddenTypes={hiddenTypes} onToggle={toggleType} />
      {filtered.length === 0 ? (
        <EmptyState icon={Search} title="没有匹配的资源" text="当前类型筛选已隐藏所有结果，请启用至少一个类型。" />
      ) : multiConnector ? (
        byConnector.map(([connId, items]) => {
          const name = connectorName(connId) || connId.slice(0, 8)
          const collapsed = collapsedConnectors.has(connId)
          return (
            <section key={connId} className="group-section cluster-section">
              <button
                type="button"
                className="group-header group-header-button cluster-header"
                onClick={() => toggleConnector(connId)}
                aria-expanded={!collapsed}
              >
                <ChevronRight size={14} className={`chevron${collapsed ? '' : ' chevron-open'}`} />
                <LayoutDashboard size={14} />
                <span>{name}</span>
                <small className="group-count">{items.length}</small>
              </button>
              {!collapsed && renderSections(connId, items)}
            </section>
          )
        })
      ) : (
        byConnector.flatMap(([connId, items]) => renderSections(connId, items))
      )}
    </div>
  )
}

function GrafanaCapabilities({
  resource,
  onAction,
}: {
  resource: ApiResource
  onAction: (resource: ApiResource, action?: string) => void
}) {
  const prefix =
    resource.resource_type === 'grafana_folder'
      ? 'grafana.folder'
      : resource.resource_type === 'grafana_dashboard'
        ? 'grafana.dashboard'
        : 'grafana.datasource'
  const actions = [
    { label: resource.resource_type === 'grafana_datasource' ? '查看配置' : '查看', action: `${prefix}.get` },
    { label: '更新', action: `${prefix}.update` },
    { label: '删除', action: `${prefix}.delete` },
  ]
  const disabled = resource.status === 'ORPHANED' || resource.available_action_count === 0
  return (
    <div className="res-card-capabilities" aria-label={`${resource.name} 支持的动作`}>
      {actions.map((item) => (
        <button
          type="button"
          key={item.action}
          disabled={disabled}
          title={disabled ? '历史或不可用资源不能发起动作' : `${item.label} ${resource.name}`}
          onClick={(event) => {
            event.stopPropagation()
            onAction(resource, item.action)
          }}
        >
          {item.label}
        </button>
      ))}
    </div>
  )
}
