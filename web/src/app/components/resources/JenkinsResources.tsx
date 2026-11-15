// Jenkins domain: group by Jenkins instance (domain), show Controllers, Nodes and Jobs.
// The domain label is extracted from the jenkins_controller resource's
// metadata.endpoint. Queue and credential resources remain API-only.
// Clicking a Job expands its recent 10 builds (grouped via metadata.job).
import { useMemo, useState } from 'react'
import { ChevronRight, Hash, Network, Play, Search, ServerCog } from 'lucide-react'
import type { ApiResource } from '../../../shared/api'
import { listResources } from '../../../shared/api'
import { EmptyState } from '../States'
import { ResourceCard, ResourceOwnershipButton } from './ResourceCard'
import { TypeFilterBar } from './TypeFilterBar'
import { useToggleSet } from '../../lib/useToggleSet'
import { buildNumber, buildTimestamp, formatBuildTime, jobBuildable } from '../../lib/resources'

const RECENT_BUILDS = 10

// Controllers provide the governed create-Job entry point. Queue and
// credential resources remain API-only.
const DISPLAY_TYPES = new Set(['jenkins_controller', 'jenkins_node', 'jenkins_job'])

// Group key = integration_id (one per Jenkins instance); fallback to connector_id.
const groupKey = (r: ApiResource) => r.integration_id || r.connector_id

const hostnameFromEndpoint = (endpoint: string): string => {
  try {
    return new URL(endpoint).hostname
  } catch {
    return endpoint
  }
}

export function JenkinsResources({
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
  // Build a lookup of endpoint per integration from jenkins_controller resources.
  // The controller is always discovered but not displayed — we only use it for
  // the domain label.
  const endpointByGroup = useMemo(() => {
    const m = new Map<string, string>()
    for (const r of resources) {
      if (r.resource_type === 'jenkins_controller') {
        const ep = r.metadata?.endpoint as string
        if (ep) m.set(groupKey(r), ep)
      }
    }
    return m
  }, [resources])

  const q = query.trim().toLowerCase()
  const searched = useMemo(
    () =>
      q
        ? resources.filter((r) => {
            const ep = endpointByGroup.get(groupKey(r)) ?? ''
            return (r.name + r.resource_type + connectorName(r.connector_id) + ep).toLowerCase().includes(q)
          })
        : resources,
    [resources, q, connectorName, endpointByGroup],
  )

  const [hiddenTypes, toggleType] = useToggleSet()
  const filtered = hiddenTypes.size === 0 ? searched : searched.filter((r) => !hiddenTypes.has(r.resource_type))
  const displayFiltered = filtered.filter((r) => DISPLAY_TYPES.has(r.resource_type))

  const typeChips = [
    {
      type: 'jenkins_controller',
      label: 'Controller',
      icon: <ServerCog size={14} />,
      count: searched.filter((r) => r.resource_type === 'jenkins_controller').length,
    },
    {
      type: 'jenkins_node',
      label: 'Node',
      icon: <Network size={14} />,
      count: searched.filter((r) => r.resource_type === 'jenkins_node').length,
    },
    {
      type: 'jenkins_job',
      label: 'Job',
      icon: <ServerCog size={14} />,
      count: searched.filter((r) => r.resource_type === 'jenkins_job').length,
    },
  ]

  const [collapsedGroups, toggleGroup] = useToggleSet(() => {
    const ids = new Set<string>()
    for (const r of resources) ids.add(groupKey(r))
    return ids
  })
  const [collapsedSections, toggleSection] = useToggleSet(() => {
    const collapsed = new Set<string>()
    for (const r of resources) {
      if (DISPLAY_TYPES.has(r.resource_type)) collapsed.add(`${groupKey(r)}:${r.resource_type}`)
    }
    return collapsed
  })
  const [expandedJob, setExpandedJob] = useState<string | null>(null)
  const [buildsByJob, setBuildsByJob] = useState<Map<string, ApiResource[]>>(new Map())
  const [loadingBuilds, setLoadingBuilds] = useState<Set<string>>(new Set())
  const [buildErrors, setBuildErrors] = useState<Map<string, string>>(new Map())

  const toggleJob = (job: ApiResource, gk: string) => {
    const key = `${gk}:${job.name}`
    if (expandedJob === key) {
      setExpandedJob(null)
      return
    }
    setExpandedJob(key)
    if (buildsByJob.has(key) || loadingBuilds.has(key)) return
    setLoadingBuilds((current) => new Set(current).add(key))
    setBuildErrors((current) => {
      const next = new Map(current)
      next.delete(key)
      return next
    })
    listResources({ type: 'jenkins_build', namespace: job.name, integrationId: job.integration_id, limit: RECENT_BUILDS })
      .then((items) => setBuildsByJob((current) => new Map(current).set(key, items ?? [])))
      .catch((error: Error) => setBuildErrors((current) => new Map(current).set(key, error.message || '加载构建记录失败')))
      .finally(() =>
        setLoadingBuilds((current) => {
          const next = new Set(current)
          next.delete(key)
          return next
        }),
      )
  }

  const renderControllerSection = (gk: string, controllers: ApiResource[]) => {
    if (controllers.length === 0) return null
    const sectionKey = `${gk}:jenkins_controller`
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
          <ServerCog size={14} />
          <span>Controllers</span>
          <small className="group-count">{controllers.length}</small>
        </button>
        {!collapsed && (
          <div className="res-list">
            {controllers.map((controller) => (
              <ResourceCard
                key={controller.id}
                resource={controller}
                connectorName={connectorName(controller.connector_id)}
                onAction={(resource) => onAction(resource, 'jenkins.job.create')}
                onProject={onProject}
                icon={<ServerCog size={20} />}
                actionLabel="创建 Job"
              >
                <div className="res-card-meta">
                  <span>Endpoint</span>
                  <strong>{String(controller.metadata?.endpoint ?? controller.name)}</strong>
                </div>
              </ResourceCard>
            ))}
          </div>
        )}
      </section>
    )
  }

  // Group displayFiltered by integration, sort by domain label.
  const byGroup = useMemo(() => {
    const m = new Map<string, ApiResource[]>()
    for (const r of displayFiltered) {
      const key = groupKey(r)
      ;(m.get(key) ?? m.set(key, []).get(key)!).push(r)
    }
    return [...m.entries()]
      .map(([key, items]) => {
        const ep = endpointByGroup.get(key)
        const label = ep ? hostnameFromEndpoint(ep) : connectorName(items[0]?.connector_id) || key.slice(0, 8)
        return { key, items, label }
      })
      .sort((a, b) => a.label.localeCompare(b.label))
  }, [displayFiltered, endpointByGroup, connectorName])

  if (resources.length === 0) {
    return (
      <EmptyState icon={ServerCog} title="暂无 Jenkins 资源" text="接入 Jenkins 中间件后，Controller、节点与 Job 会在此按域名分组展示。" />
    )
  }
  if (searched.length === 0) {
    return <EmptyState icon={Search} title="没有匹配的资源" text="尝试调整搜索条件。" />
  }

  const renderNodeSection = (gk: string, nodes: ApiResource[]) => {
    if (nodes.length === 0) return null
    const sectionKey = `${gk}:jenkins_node`
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
          <Network size={14} />
          <span>Nodes</span>
          <small className="group-count">{nodes.length}</small>
        </button>
        {!collapsed && (
          <div className="res-list">
            {nodes.map((r) => (
              <ResourceCard
                key={r.id}
                resource={r}
                connectorName={connectorName(r.connector_id)}
                onAction={onAction}
                onProject={onProject}
                icon={<Network size={20} />}
              >
                <div className="res-card-meta">
                  <span>状态</span>
                  <strong>{r.metadata?.temporarilyOffline ? '离线' : '在线'}</strong>
                </div>
                <div className="res-card-meta">
                  <span>执行器</span>
                  <strong>{String(r.metadata?.numExecutors ?? '-')}</strong>
                </div>
              </ResourceCard>
            ))}
          </div>
        )}
      </section>
    )
  }

  const renderJobSection = (gk: string, jobs: ApiResource[]) => {
    if (jobs.length === 0) return null
    const sectionKey = `${gk}:jenkins_job`
    const collapsed = collapsedSections.has(sectionKey)
    const sortedJobs = [...jobs].sort((a, b) => a.name.localeCompare(b.name))
    return (
      <section key={sectionKey} className="group-section">
        <button
          type="button"
          className="group-header group-header-button"
          onClick={() => toggleSection(sectionKey)}
          aria-expanded={!collapsed}
        >
          <ChevronRight size={14} className={`chevron${collapsed ? '' : ' chevron-open'}`} />
          <ServerCog size={14} />
          <span>Jobs</span>
          <small className="group-count">{jobs.length}</small>
        </button>
        {!collapsed && (
          <div className="res-list">
            {sortedJobs.map((job) => {
              const jobKey = `${gk}:${job.name}`
              const jobBuilds = buildsByJob.get(jobKey)
              const loadedBuilds = jobBuilds ?? []
              const recent = loadedBuilds.slice(0, RECENT_BUILDS)
              const expanded = expandedJob === jobKey
              return (
                <div key={job.id} className="job-block">
                  <div
                    className={`res-card res-card-clickable ${expanded ? 'res-card-selected' : ''}`}
                    onClick={() => toggleJob(job, gk)}
                    role="button"
                  >
                    <div className="resource-icon jenkins">
                      <ServerCog size={20} />
                    </div>
                    <div className="res-card-main">
                      <strong>{job.name}</strong>
                      <span>Jenkins Job</span>
                    </div>
                    <div className="res-card-meta">
                      <span>状态</span>
                      <strong>{jobBuildable(job) ? '可构建' : '已禁用'}</strong>
                    </div>
                    <div className="res-card-meta">
                      <span>构建记录</span>
                      <strong>{jobBuilds ? jobBuilds.length : '按需加载'}</strong>
                    </div>
                    <ResourceOwnershipButton resource={job} onProject={onProject} />
                    <div className="res-card-actions">
                      <ChevronRight size={16} className={`chevron ${expanded ? 'chevron-open' : ''}`} />
                      <button
                        className="secondary-button"
                        onClick={(e) => {
                          e.stopPropagation()
                          onAction(job)
                        }}
                      >
                        <Play size={15} />
                        请求动作
                      </button>
                    </div>
                  </div>
                  {expanded && (
                    <div className="build-list">
                      {loadingBuilds.has(jobKey) ? (
                        <div className="build-empty">正在加载构建记录...</div>
                      ) : buildErrors.has(jobKey) ? (
                        <div className="build-empty">{buildErrors.get(jobKey)}</div>
                      ) : recent.length === 0 ? (
                        <div className="build-empty">暂无构建记录</div>
                      ) : (
                        recent.map((b) => (
                          <div className="build-row" key={b.id}>
                            <Hash size={13} />
                            <strong>#{buildNumber(b) ?? b.name}</strong>
                            <span className="build-time">{formatBuildTime(buildTimestamp(b))}</span>
                            <button className="quiet-button" onClick={() => onAction(b)}>
                              <Play size={13} />
                              请求动作
                            </button>
                          </div>
                        ))
                      )}
                      {loadedBuilds.length > RECENT_BUILDS && (
                        <div className="build-more">
                          仅显示最近 {RECENT_BUILDS} 次，共 {loadedBuilds.length} 次构建
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </section>
    )
  }

  return (
    <div>
      <TypeFilterBar chips={typeChips} hiddenTypes={hiddenTypes} onToggle={toggleType} />
      {displayFiltered.length === 0 ? (
        <EmptyState icon={Search} title="没有匹配的资源" text="当前类型筛选已隐藏所有结果，请启用至少一个类型。" />
      ) : (
        byGroup.map(({ key, items, label }) => {
          const collapsed = collapsedGroups.has(key)
          const controllers = items.filter((r) => r.resource_type === 'jenkins_controller')
          const nodes = items.filter((r) => r.resource_type === 'jenkins_node')
          const jobs = items.filter((r) => r.resource_type === 'jenkins_job')
          const summary = [
            controllers.length > 0 ? `${controllers.length} Controller` : '',
            nodes.length > 0 ? `${nodes.length} Node` : '',
            jobs.length > 0 ? `${jobs.length} Job` : '',
          ]
            .filter(Boolean)
            .join(' · ')
          return (
            <section key={key} className="group-section cluster-section">
              <button
                type="button"
                className="group-header group-header-button cluster-header"
                onClick={() => toggleGroup(key)}
                aria-expanded={!collapsed}
              >
                <ChevronRight size={14} className={`chevron${collapsed ? '' : ' chevron-open'}`} />
                <ServerCog size={14} />
                <span>{label}</span>
                <small className="group-count">{items.length}</small>
                {collapsed && summary && <span className="cluster-summary">{summary}</span>}
              </button>
              {!collapsed && (
                <>
                  {renderControllerSection(key, controllers)}
                  {renderNodeSection(key, nodes)}
                  {renderJobSection(key, jobs)}
                </>
              )}
            </section>
          )
        })
      )}
    </div>
  )
}
