// Kubernetes domain: group resources by cluster (connector) -> namespace -> type.
// The cluster layer only renders when 2+ K8s connectors are present, keeping
// the single-cluster view compact. ORPHANED / DISABLED resources are dimmed so
// active ones stand out. Type filter chips at top toggle visibility per
// resource_type. Namespace and type sub-groups are collapsible.
import { Boxes, CircleDot, Bell, Database, HardDrive, Layers, Briefcase, Clock, Network, Share2, Globe, FileText, Lock, Server, Folder, Search, ChevronRight } from 'lucide-react'
import { useMemo } from 'react'
import type { ReactNode } from 'react'
import type { ApiResource } from '../../../shared/api'
import { EmptyState } from '../States'
import { ResourceCard } from './ResourceCard'
import { TypeFilterBar } from './TypeFilterBar'
import { useToggleSet } from '../../lib/useToggleSet'

const TYPE_ORDER = ['deployment', 'statefulset', 'daemonset', 'replicaset', 'pod', 'job', 'cronjob', 'service', 'endpoints', 'ingress', 'configmap', 'secret', 'event', 'node', 'namespace'] as const

const TYPE_LABEL: Record<string, string> = {
  deployment: 'Deployment', statefulset: 'StatefulSet', daemonset: 'DaemonSet', replicaset: 'ReplicaSet',
  pod: 'Pod', job: 'Job', cronjob: 'CronJob',
  service: 'Service', endpoints: 'Endpoints', ingress: 'Ingress',
  configmap: 'ConfigMap', secret: 'Secret',
  event: '事件', node: 'Node', namespace: 'Namespace',
}
const TYPE_PLURAL: Record<string, string> = {
  deployment: 'Deployments', statefulset: 'StatefulSets', daemonset: 'DaemonSets', replicaset: 'ReplicaSets',
  pod: 'Pods', job: 'Jobs', cronjob: 'CronJobs',
  service: 'Services', endpoints: 'Endpoints', ingress: 'Ingresses',
  configmap: 'ConfigMaps', secret: 'Secrets',
  event: '事件', node: 'Nodes', namespace: 'Namespaces',
}
const TYPE_ICON: Record<string, ReactNode> = {
  deployment: <Boxes size={20} />, pod: <CircleDot size={20} />, event: <Bell size={20} />,
  statefulset: <Database size={20} />, daemonset: <HardDrive size={20} />, replicaset: <Layers size={20} />,
  job: <Briefcase size={20} />, cronjob: <Clock size={20} />,
  service: <Network size={20} />, endpoints: <Share2 size={20} />, ingress: <Globe size={20} />,
  configmap: <FileText size={20} />, secret: <Lock size={20} />,
  node: <Server size={20} />, namespace: <Folder size={20} />,
}
const TYPE_ICON_SMALL: Record<string, ReactNode> = {
  deployment: <Boxes size={14} />, pod: <CircleDot size={14} />, event: <Bell size={14} />,
  statefulset: <Database size={14} />, daemonset: <HardDrive size={14} />, replicaset: <Layers size={14} />,
  job: <Briefcase size={14} />, cronjob: <Clock size={14} />,
  service: <Network size={14} />, endpoints: <Share2 size={14} />, ingress: <Globe size={14} />,
  configmap: <FileText size={14} />, secret: <Lock size={14} />,
  node: <Server size={14} />, namespace: <Folder size={14} />,
}

const sortTypes = (types: string[]) => types.sort((a, b) => {
  const ia = TYPE_ORDER.indexOf(a as typeof TYPE_ORDER[number])
  const ib = TYPE_ORDER.indexOf(b as typeof TYPE_ORDER[number])
  return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib)
})

const sortNamespaces = (entries: [string, ApiResource[]][]) =>
  entries.sort(([a], [b]) => (a === '__none' ? 1 : b === '__none' ? -1 : a.localeCompare(b)))

const groupByNamespace = (items: ApiResource[]): [string, ApiResource[]][] => {
  const m = new Map<string, ApiResource[]>()
  for (const r of items) {
    const key = r.namespace ?? '__none'
    ;(m.get(key) ?? m.set(key, []).get(key)!).push(r)
  }
  return sortNamespaces([...m.entries()])
}

export function KubernetesResources({
  resources, connectorName, query, onAction, onProject,
}: {
  resources: ApiResource[]
  connectorName: (id: string) => string
  query: string
  onAction: (r: ApiResource) => void
  onProject: (r: ApiResource) => void
}) {
  const q = query.trim().toLowerCase()
  const searched = q
    ? resources.filter((r) =>
        (r.name + r.resource_type + (r.namespace ?? '') + connectorName(r.connector_id)).toLowerCase().includes(q))
    : resources

  const [hiddenTypes, toggleType] = useToggleSet()
  const filtered = hiddenTypes.size === 0 ? searched : searched.filter((r) => !hiddenTypes.has(r.resource_type))

  const typeCounts = useMemo(() => {
    const m = new Map<string, number>()
    for (const r of searched) m.set(r.resource_type, (m.get(r.resource_type) ?? 0) + 1)
    return m
  }, [searched])
  const typeChips = useMemo(() => sortTypes([...typeCounts.keys()]).map((t) => ({
    type: t, label: TYPE_PLURAL[t] ?? t, icon: TYPE_ICON_SMALL[t] ?? <CircleDot size={14} />, count: typeCounts.get(t) ?? 0,
  })), [typeCounts])

  const multiCluster = useMemo(() => {
    const ids = new Set<string>()
    for (const r of resources) ids.add(r.connector_id)
    return ids.size > 1
  }, [resources])

  const [collapsedClusters, toggleCluster] = useToggleSet(() => {
    const ids = new Set<string>()
    for (const r of resources) ids.add(r.connector_id)
    return ids
  })

  const [collapsedNs, toggleNs] = useToggleSet(() => {
    const collapsed = new Set<string>()
    for (const r of resources) collapsed.add(`${r.connector_id}:${r.namespace ?? '__none'}`)
    return collapsed
  })

  const [collapsedTypes, toggleTypeInNs] = useToggleSet(() => {
    const collapsed = new Set<string>()
    for (const r of resources) collapsed.add(`${r.connector_id}:${r.namespace ?? '__none'}:${r.resource_type}`)
    return collapsed
  })

  const byConnector = useMemo(() => {
    const m = new Map<string, ApiResource[]>()
    for (const r of filtered) {
      ;(m.get(r.connector_id) ?? m.set(r.connector_id, []).get(r.connector_id)!).push(r)
    }
    return [...m.entries()].sort(([ida], [idb]) =>
      connectorName(ida).localeCompare(connectorName(idb)))
  }, [filtered, connectorName])

  if (resources.length === 0) {
    return <EmptyState icon={Boxes} title="暂无 Kubernetes 资源" text="接入 Kubernetes 执行通道后，Deployment 与 Pod 会在此按命名空间分组展示。" />
  }
  if (searched.length === 0) {
    return <EmptyState icon={Search} title="没有匹配的资源" text="尝试调整搜索条件。" />
  }

  const renderTypeSubgroup = (connId: string, ns: string, type: string, items: ApiResource[]) => {
    const typeKey = `${connId}:${ns}:${type}`
    const typeCollapsed = collapsedTypes.has(typeKey)
    return (
      <div key={type} className="subgroup">
        <button type="button" className="subgroup-title" onClick={() => toggleTypeInNs(typeKey)} aria-expanded={!typeCollapsed}>
          <ChevronRight size={12} className={`chevron${typeCollapsed ? '' : ' chevron-open'}`} />
          <span>{TYPE_PLURAL[type] ?? type}</span>
          <small className="group-count">{items.length}</small>
        </button>
        {!typeCollapsed && (
          <div className="res-list">
            {items.map((r) => (
              <ResourceCard
                key={r.id}
                resource={r}
                connectorName={connectorName(r.connector_id)}
                onAction={onAction}
                onProject={onProject}
                stale={r.status !== 'ACTIVE'}
                icon={TYPE_ICON[r.resource_type] ?? <CircleDot size={20} />}
              >
                <div className="res-card-meta"><span>执行通道</span><strong>{connectorName(r.connector_id) || r.connector_id.slice(0, 8)}</strong></div>
              </ResourceCard>
            ))}
          </div>
        )}
      </div>
    )
  }

  const renderNamespaceSection = (connId: string, ns: string, items: ApiResource[]) => {
    const byType = new Map<string, ApiResource[]>()
    for (const r of items) {
      ;(byType.get(r.resource_type) ?? byType.set(r.resource_type, []).get(r.resource_type)!).push(r)
    }
    const types = sortTypes([...byType.keys()])
    const nsKey = `${connId}:${ns}`
    const nsCollapsed = collapsedNs.has(nsKey)
    return (
      <section key={nsKey} className="group-section">
        <button
          type="button"
          className="group-header group-header-button"
          onClick={() => toggleNs(nsKey)}
          aria-expanded={!nsCollapsed}
        >
          <ChevronRight size={14} className={`chevron${nsCollapsed ? '' : ' chevron-open'}`} />
          <span>{ns === '__none' ? '未命名空间' : ns}</span>
          <div className="group-type-counts">
            {types.map((t) => (
              <small key={t} className="group-count">{byType.get(t)!.length} {TYPE_LABEL[t] ?? t}</small>
            ))}
          </div>
        </button>
        {!nsCollapsed && types.map((t) => renderTypeSubgroup(connId, ns, t, byType.get(t)!))}
      </section>
    )
  }

  return (
    <div>
      <TypeFilterBar chips={typeChips} hiddenTypes={hiddenTypes} onToggle={toggleType} />
      {filtered.length === 0 ? (
        <EmptyState icon={Search} title="没有匹配的资源" text="当前类型筛选已隐藏所有结果，请启用至少一个类型。" />
      ) : multiCluster ? (
        byConnector.map(([connId, items]) => {
          const name = connectorName(connId) || connId.slice(0, 8)
          const clusterCollapsed = collapsedClusters.has(connId)
          const nsGroups = groupByNamespace(items)
          const nsSummary = nsGroups.map(([ns, nsItems]) => `${ns === '__none' ? '未命名空间' : ns}:${nsItems.length}`).join(' · ')
          return (
            <section key={connId} className="group-section cluster-section">
              <button
                type="button"
                className="group-header group-header-button cluster-header"
                onClick={() => toggleCluster(connId)}
                aria-expanded={!clusterCollapsed}
              >
                <ChevronRight size={14} className={`chevron${clusterCollapsed ? '' : ' chevron-open'}`} />
                <Boxes size={14} />
                <span>{name}</span>
                <small className="group-count">{items.length}</small>
                {clusterCollapsed && nsSummary && (
                  <span className="cluster-summary">{nsSummary}</span>
                )}
              </button>
              {!clusterCollapsed && nsGroups.map(([ns, nsItems]) =>
                renderNamespaceSection(connId, ns, nsItems))}
            </section>
          )
        })
      ) : (
        byConnector.flatMap(([connId, items]) =>
          groupByNamespace(items).map(([ns, nsItems]) =>
            renderNamespaceSection(connId, ns, nsItems)))
      )}
    </div>
  )
}
