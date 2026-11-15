import { useMemo } from 'react'
import { ChevronRight, Database, Search, Server, Table2 } from 'lucide-react'
import type { ApiResource } from '../../../shared/api'
import { EmptyState } from '../States'
import { ResourceCard } from './ResourceCard'
import { TypeFilterBar } from './TypeFilterBar'
import { useToggleSet } from '../../lib/useToggleSet'

export function DatabaseResources({
  resources,
  connectorName,
  query,
  onAction,
  onProject,
}: {
  resources: ApiResource[]
  connectorName: (id: string) => string
  query: string
  onAction: (resource: ApiResource) => void
  onProject: (resource: ApiResource) => void
}) {
  const q = query.trim().toLowerCase()
  const matches = (r: ApiResource) => !q || `${r.name} ${r.namespace ?? ''} ${r.metadata.protocol ?? ''}`.toLowerCase().includes(q)
  const searched = useMemo(() => resources.filter(matches), [resources, q])

  const [hiddenTypes, toggleType] = useToggleSet()
  const filtered = hiddenTypes.size === 0 ? searched : searched.filter((r) => !hiddenTypes.has(r.resource_type))

  const typeChips = [
    {
      type: 'database_instance',
      label: '实例',
      icon: <Database size={14} />,
      count: searched.filter((r) => r.resource_type === 'database_instance').length,
    },
    {
      type: 'database_schema',
      label: 'Schema',
      icon: <Database size={14} />,
      count: searched.filter((r) => r.resource_type === 'database_schema').length,
    },
    {
      type: 'database_table',
      label: 'Table',
      icon: <Table2 size={14} />,
      count: searched.filter((r) => r.resource_type === 'database_table').length,
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
      <EmptyState icon={Database} title="暂无数据库资源" text="接入 PostgreSQL 或 MySQL 后，实例、Schema、Table 和 View 会在这里展示。" />
    )
  }
  if (searched.length === 0) {
    return <EmptyState icon={Search} title="没有匹配的资源" text="尝试调整搜索条件。" />
  }

  const renderSections = (connId: string, items: ApiResource[]) => {
    const instances = items.filter((r) => r.resource_type === 'database_instance')
    const schemas = items.filter((r) => r.resource_type === 'database_schema')
    const tables = items.filter((r) => r.resource_type === 'database_table')
    const sections = [
      {
        type: 'database_instance',
        label: '数据库实例',
        icon: <Server size={14} />,
        items: instances,
        cardIcon: <Database size={20} />,
        meta: (r: ApiResource) => (
          <div className="res-card-meta">
            <span>协议</span>
            <strong>{String(r.metadata.protocol ?? '-')}</strong>
          </div>
        ),
      },
      {
        type: 'database_schema',
        label: 'Schema / Database',
        icon: <Database size={14} />,
        items: schemas,
        cardIcon: <Database size={20} />,
        meta: () => null,
      },
      {
        type: 'database_table',
        label: 'Table / View',
        icon: <Table2 size={14} />,
        items: tables,
        cardIcon: <Table2 size={20} />,
        meta: (r: ApiResource) => (
          <>
            <div className="res-card-meta">
              <span>Schema</span>
              <strong>{r.namespace ?? '-'}</strong>
            </div>
            <div className="res-card-meta">
              <span>类型</span>
              <strong>{String(r.metadata.kind ?? 'TABLE')}</strong>
            </div>
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
          {!collapsed &&
            (s.type === 'database_table' && s.items.length === 0 ? (
              <EmptyState icon={Search} title="没有匹配的表" text="尝试调整搜索条件或检查 Schema scope。" />
            ) : (
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
            ))}
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
                <Database size={14} />
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
