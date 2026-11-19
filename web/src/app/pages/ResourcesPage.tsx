import { useDeferredValue, useMemo, useState } from 'react'
import { useSearchParams } from '../../shared/router'
import { AlertTriangle, FolderKanban, History, RefreshCw, Search } from 'lucide-react'
import type { ApiMyProject, ApiResource } from '../../shared/api'
import { canUser, getProjectMembershipPolicy, getResourceStats, listConnectors, listIntegrations, listMyProjects, listProjects, listResources } from '../../shared/api'
import { ErrorState, LoadingState } from '../components/States'
import { ActionPanel } from '../components/ActionPanel'
import { KubernetesResources } from '../components/resources/KubernetesResources'
import { LinuxResources } from '../components/resources/LinuxResources'
import { JenkinsResources } from '../components/resources/JenkinsResources'
import { GrafanaResources } from '../components/resources/GrafanaResources'
import { DatabaseResources } from '../components/resources/DatabaseResources'
import { HuaweiResources } from '../components/resources/HuaweiResources'
import { NetworkResources } from '../components/resources/NetworkResources'
import { ProjectManagePanel } from '../components/resources/ProjectManagePanel'
import { ResourceProjectPanel } from '../components/resources/ResourceProjectPanel'
import { resourceDomain, type ResourceDomain } from '../lib/resources'
import { EMPTY_ARRAY, EMPTY_RECORD, useApiQuery } from '../lib/useApiQuery'
import { useApp } from '../components/Toast'

type Tab = { key: ResourceDomain; label: string }
type ResourceView = 'current' | 'abnormal' | 'history'
type StatusCounts = Record<string, number>

const TABS: Tab[] = [
  { key: 'kubernetes', label: 'Kubernetes' },
  { key: 'linux', label: 'Linux 主机' },
  { key: 'jenkins', label: 'Jenkins' },
  { key: 'grafana', label: 'Grafana' },
  { key: 'database', label: '数据库' },
  { key: 'huawei', label: '云厂商' },
  { key: 'network', label: '网络设备' },
]

const DOMAIN_STORAGE_KEY = 'warden.resources.domain'
const isDomainKey = (v: string | null): v is ResourceDomain => TABS.some((t) => t.key === v)
const countStatuses = (counts: StatusCounts | undefined, view: ResourceView) =>
  Object.entries(counts ?? {}).reduce((total, [status, count]) => {
    if (view === 'history') return total + (status === 'ORPHANED' ? count : 0)
    if (view === 'abnormal') return total + (status !== 'ACTIVE' && status !== 'ORPHANED' ? count : 0)
    return total + (status !== 'ORPHANED' ? count : 0)
  }, 0)

const mergeStatusCounts = (domains: Record<string, StatusCounts> | undefined) => {
  const result: StatusCounts = {}
  for (const counts of Object.values(domains ?? {})) {
    for (const [status, count] of Object.entries(counts)) result[status] = (result[status] ?? 0) + count
  }
  return result
}

const mergeProjectStatusCounts = (projects: Array<Record<string, StatusCounts> | undefined>) => {
  const result: StatusCounts = {}
  for (const domains of projects) {
    for (const [status, count] of Object.entries(mergeStatusCounts(domains))) result[status] = (result[status] ?? 0) + count
  }
  return result
}

// Resource catalog: project bar (connector-level assignment + namespace
// overrides, default project as fallback) on top, then per-domain tabs, each
// rendered with a layout matching its data shape.
export function ResourcesPage() {
  const { auth, notify } = useApp()
  const isTenantAdmin = canUser(auth.user, 'warden:tenant:admin')
  const [query, setQuery] = useState('')
  // Defer search filtering so typing stays responsive with thousands of resources.
  const deferredQuery = useDeferredValue(query)
  const [searchParams, setSearchParams] = useSearchParams()
  // Domain is derived from the URL (deep links work without a sync effect);
  // localStorage only provides the fallback when the URL has no valid domain.
  const [fallbackDomain] = useState<ResourceDomain>(() => {
    const saved = localStorage.getItem(DOMAIN_STORAGE_KEY)
    return isDomainKey(saved) ? saved : 'kubernetes'
  })
  const domainParam = searchParams.get('domain')
  const domain: ResourceDomain = isDomainKey(domainParam) ? domainParam : fallbackDomain
  const setDomain = (d: ResourceDomain) => {
    localStorage.setItem(DOMAIN_STORAGE_KEY, d)
    const next = new URLSearchParams(searchParams)
    next.set('domain', d)
    setSearchParams(next, { replace: true })
  }
  const [resourceView, setResourceView] = useState<ResourceView>('current')
  const [projectFilter, setProjectFilterState] = useState(() => searchParams.get('project') || 'all')
  const setProjectFilter = (project: string) => {
    setProjectFilterState(project)
    const next = new URLSearchParams(searchParams)
    if (project === 'all') next.delete('project')
    else next.set('project', project)
    setSearchParams(next, { replace: true })
  }
  const [panelOpen, setPanelOpen] = useState(false)
  const [manageOpen, setManageOpen] = useState(false)
  const [selectedResource, setSelectedResource] = useState<ApiResource | null>(null)
  const [selectedAction, setSelectedAction] = useState<string | undefined>()
  const [projectResource, setProjectResource] = useState<ApiResource | null>(null)
  // Member visibility: which projects the caller may actually execute in, and
  // whether project membership is enforced at all. Used to explain "view-only"
  // resources up front instead of letting members discover it via API errors.
  const [myProjects, setMyProjects] = useState<ApiMyProject[]>([])
  const [membershipEnforced, setMembershipEnforced] = useState(false)

  const catalogQuery = useApiQuery(
    () =>
      Promise.all([
        listResources({ domain, excludeType: domain === 'jenkins' ? 'jenkins_build' : undefined }),
        isTenantAdmin ? listConnectors() : Promise.resolve([]),
        listProjects(),
        isTenantAdmin ? listIntegrations() : Promise.resolve([]),
        getResourceStats(),
        isTenantAdmin ? Promise.resolve([] as ApiMyProject[]) : listMyProjects(),
        isTenantAdmin ? Promise.resolve({ enforced: false }) : getProjectMembershipPolicy(),
      ]),
    [domain, isTenantAdmin],
    {
      onSuccess: ([, , , , , myProjs, policy]) => {
        setMyProjects(myProjs ?? [])
        setMembershipEnforced(Boolean(policy?.enforced))
      },
    },
  )
  const apiResources = catalogQuery.data?.[0] ?? EMPTY_ARRAY
  const connectors = catalogQuery.data?.[1] ?? EMPTY_ARRAY
  const projects = catalogQuery.data?.[2] ?? EMPTY_ARRAY
  const integrations = catalogQuery.data?.[3] ?? EMPTY_ARRAY
  const stats = catalogQuery.data?.[4] ?? null
  const loading = catalogQuery.loading
  const error = catalogQuery.error
  const load = (showSpinner = true) => catalogQuery.reload(showSpinner)

  const connectorName = useMemo(() => (id: string) => connectors.find((c) => c.id === id)?.name ?? '', [connectors])

  const viewResources = useMemo(
    () =>
      apiResources.filter((resource) => {
        if (resourceView === 'history') return resource.status === 'ORPHANED'
        if (resourceView === 'abnormal') return resource.status !== 'ACTIVE' && resource.status !== 'ORPHANED'
        return resource.status !== 'ORPHANED'
      }),
    [apiResources, resourceView],
  )

  const catalogStats = useMemo(() => stats?.catalogByProjectDomainStatus ?? EMPTY_RECORD, [stats])
  const projectCounts = useMemo(() => {
    const counts = new Map<string, number>()
    for (const [projectID, domains] of Object.entries(catalogStats)) {
      counts.set(projectID, countStatuses(mergeStatusCounts(domains), resourceView))
    }
    return counts
  }, [catalogStats, resourceView])

  const projectFiltered = useMemo(
    () => (projectFilter === 'all' ? viewResources : viewResources.filter((r) => (r.project_ids ?? []).includes(projectFilter))),
    [viewResources, projectFilter],
  )

  const selectedProjectStatusCounts = useMemo(
    () =>
      projectFilter === 'all' ? mergeStatusCounts(stats?.catalogByDomainStatus) : mergeProjectStatusCounts([catalogStats[projectFilter]]),
    [catalogStats, projectFilter, stats?.catalogByDomainStatus],
  )
  const activeCount = selectedProjectStatusCounts.ACTIVE ?? 0
  const abnormalCount = Object.entries(selectedProjectStatusCounts).reduce(
    (total, [status, count]) => total + (status !== 'ACTIVE' && status !== 'ORPHANED' ? count : 0),
    0,
  )
  const historyCount = selectedProjectStatusCounts.ORPHANED ?? 0
  const currentCount = activeCount + abnormalCount

  const byDomain = useMemo(() => {
    const groups: Record<ResourceDomain, ApiResource[]> = {
      kubernetes: [],
      linux: [],
      jenkins: [],
      grafana: [],
      database: [],
      huawei: [],
      network: [],
    }
    for (const r of projectFiltered) groups[resourceDomain(r)].push(r)
    return groups
  }, [projectFiltered])

  const visibleIntegrations = useMemo(
    () =>
      projectFilter === 'all'
        ? integrations
        : integrations.filter(
            (integration) =>
              integration.project_id === projectFilter || projectFiltered.some((resource) => resource.integration_id === integration.id),
          ),
    [integrations, projectFilter, projectFiltered],
  )

  const domainCounts = useMemo(() => {
    const counts: Record<ResourceDomain, number> = { kubernetes: 0, linux: 0, jenkins: 0, grafana: 0, database: 0, huawei: 0, network: 0 }
    const projectsToCount = projectFilter === 'all' ? [stats?.catalogByDomainStatus ?? {}] : [catalogStats[projectFilter] ?? {}]
    for (const domains of projectsToCount) {
      for (const tab of TABS) counts[tab.key] += countStatuses(domains[tab.key], resourceView)
    }
    return counts
  }, [catalogStats, projectFilter, resourceView, stats?.catalogByDomainStatus])

  const allProjectCount = countStatuses(mergeStatusCounts(stats?.catalogByDomainStatus), resourceView)

  const namespacesByConnector = useMemo(() => {
    const m = new Map<string, string[]>()
    for (const r of apiResources) {
      if (!r.namespace || resourceDomain(r) !== 'kubernetes') continue
      const list = m.get(r.connector_id) ?? []
      if (!list.includes(r.namespace)) {
        list.push(r.namespace)
        m.set(r.connector_id, list)
      }
    }
    for (const [, list] of m) list.sort()
    return m
  }, [apiResources])

  if (loading) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  const executableProjectIds = new Set(myProjects.filter((p) => p.my_role !== 'VIEWER').map((p) => p.id))
  const resourceExecutable = (resource: ApiResource) => {
    const ids = resource.project_ids?.length ? resource.project_ids : resource.project_id ? [resource.project_id] : []
    return ids.some((id) => executableProjectIds.has(id))
  }
  const openAction = (resource: ApiResource, action?: string) => {
    if (!isTenantAdmin && membershipEnforced && !resourceExecutable(resource)) {
      notify('该资源所属项目你只有查看权限（VIEWER 或未加入），不能发起执行。请联系组织管理员调整项目角色。', 'error')
      return
    }
    setSelectedResource(resource)
    setSelectedAction(action)
    setPanelOpen(true)
  }
  const openProject = (resource: ApiResource) => {
    if (!isTenantAdmin) {
      notify('资源项目范围由租户管理员维护', 'error')
      return
    }
    setProjectResource(resource)
  }

  return (
    <div className="view-stack">
      <div className="panel">
        <div className="ops-ph">
          <span>Resource Catalog · 资源目录</span>
          <span className="font-mono text-[10px] text-content-subtle dark:text-content-dark-subtle">
            {activeCount} 正常 · {abnormalCount} 异常 · {historyCount} 历史
          </span>
        </div>
        <div className="toolbar">
          <div className="search-box">
            <Search size={17} />
            <input
              aria-label="搜索资源"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="搜索资源名称、类型或服务目标"
            />
          </div>
          <div className="resource-overview" aria-label="资源概览">
            <span>
              当前 <strong>{currentCount}</strong> 个资源
            </span>
            <span>
              正常 <strong className="overview-ok">{activeCount}</strong>
            </span>
            <button
              type="button"
              className={`overview-abnormal${resourceView === 'abnormal' ? ' selected' : ''}`}
              onClick={() => setResourceView((current) => (current === 'abnormal' ? 'current' : 'abnormal'))}
              title={resourceView === 'abnormal' ? '点击显示当前资源' : '点击只看需要处理的异常资源'}
            >
              <AlertTriangle size={13} />
              异常 <strong>{abnormalCount}</strong>
            </button>
            <button
              type="button"
              className={`overview-history${resourceView === 'history' ? ' selected' : ''}`}
              onClick={() => setResourceView((current) => (current === 'history' ? 'current' : 'history'))}
              title={resourceView === 'history' ? '点击显示当前资源' : '点击查看已从目标环境消失的历史资源'}
            >
              <History size={13} />
              历史 <strong>{historyCount}</strong>
            </button>
          </div>
          <div className="toolbar-actions">
            {isTenantAdmin && (
              <button className="quiet-button" onClick={() => setManageOpen(true)}>
                <FolderKanban size={16} />
                项目管理
              </button>
            )}
            <button className="quiet-button" onClick={() => load()}>
              <RefreshCw size={16} />
              刷新
            </button>
          </div>
        </div>
        <div className="project-filter-bar" role="tablist" aria-label="按项目筛选">
          <button className={`project-chip${projectFilter === 'all' ? ' selected' : ''}`} onClick={() => setProjectFilter('all')}>
            全部项目<small>{allProjectCount}</small>
          </button>
          {projects.map((project) => (
            <button
              key={project.id}
              className={`project-chip${projectFilter === project.id ? ' selected' : ''}${project.is_default ? ' project-chip-default' : ''}`}
              onClick={() => setProjectFilter(project.id)}
              title={project.description || project.name}
            >
              {project.name}
              <small>{projectCounts.get(project.id) ?? 0}</small>
            </button>
          ))}
        </div>
        <div className="segmented domain-tabs">
          {TABS.map((t) => (
            <button key={t.key} className={domain === t.key ? 'selected' : ''} onClick={() => setDomain(t.key)}>
              {t.label}
              <small className="tab-count">{domainCounts[t.key]}</small>
            </button>
          ))}
        </div>
      </div>

      {!isTenantAdmin && membershipEnforced && (
        <p className="resource-access-note">
          成员视图：可浏览本组织全部资源；仅当你以「操作员 / 项目管理员」身份加入资源所属项目时才能发起执行，其余资源为仅查看。
        </p>
      )}
      <div className="panel resources-panel">
        {domain === 'kubernetes' && (
          <KubernetesResources
            resources={byDomain.kubernetes}
            connectorName={connectorName}
            query={deferredQuery}
            onAction={openAction}
            onProject={openProject}
          />
        )}
        {domain === 'linux' && (
          <LinuxResources
            resources={byDomain.linux}
            connectors={connectors}
            query={deferredQuery}
            onAction={openAction}
            onProject={openProject}
          />
        )}
        {domain === 'jenkins' && (
          <JenkinsResources
            resources={byDomain.jenkins}
            connectorName={connectorName}
            query={deferredQuery}
            onAction={openAction}
            onProject={openProject}
          />
        )}
        {domain === 'grafana' && (
          <GrafanaResources
            resources={byDomain.grafana}
            connectorName={connectorName}
            query={deferredQuery}
            onAction={openAction}
            onProject={openProject}
          />
        )}
        {domain === 'database' && (
          <DatabaseResources
            resources={byDomain.database}
            connectorName={connectorName}
            query={deferredQuery}
            onAction={openAction}
            onProject={openProject}
          />
        )}
        {domain === 'huawei' && (
          <HuaweiResources
            resources={byDomain.huawei}
            integrations={visibleIntegrations}
            connectorName={connectorName}
            query={deferredQuery}
            onAction={openAction}
            onProject={openProject}
          />
        )}
        {domain === 'network' && (
          <NetworkResources
            resources={byDomain.network}
            connectorName={connectorName}
            query={deferredQuery}
            onAction={openAction}
            onProject={openProject}
          />
        )}
      </div>

      {panelOpen && selectedResource && (
        <ActionPanel
          initialResource={selectedResource}
          initialAction={selectedAction}
          initialActingProjectId={projectFilter === 'all' ? undefined : projectFilter}
          onClose={() => {
            setPanelOpen(false)
            setSelectedAction(undefined)
          }}
        />
      )}
      {isTenantAdmin && projectResource && (
        <ResourceProjectPanel
          resource={projectResource}
          projects={projects}
          onChanged={() => load(false)}
          onClose={() => setProjectResource(null)}
        />
      )}
      {isTenantAdmin && manageOpen && (
        <ProjectManagePanel
          projects={projects}
          connectors={connectors}
          namespacesByConnector={namespacesByConnector}
          resources={apiResources}
          onChanged={() => load(false)}
          onClose={() => setManageOpen(false)}
        />
      )}
    </div>
  )
}
