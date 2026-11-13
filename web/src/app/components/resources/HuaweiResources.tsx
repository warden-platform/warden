import { useMemo, useState, type ReactNode } from 'react'
import { ChevronDown, ChevronRight, Cloud, Database, Eye, FileSearch, HardDrive, Layers3, MapPin, Network, Search, Server, Settings2, ShieldCheck } from 'lucide-react'
import type { ApiResource, Integration } from '../../../shared/api'
import { EmptyState } from '../States'
import { ResourceCard, ResourceOwnershipButton } from './ResourceCard'
import { aliyunRegionLabel, cloudRegionLabel } from '../../lib/labels'

type CloudService = 'all' | 'ecs' | 'rds' | 'elb' | 'obs' | 'lts' | 'swr' | 'sls'

const SERVICE_TYPES: Record<Exclude<CloudService, 'all'>, string[]> = {
  ecs: ['huawei_ecs_instance'],
  rds: ['huawei_rds_instance'],
  elb: ['huawei_elb_loadbalancer'],
  obs: ['huawei_obs_bucket'],
  lts: ['huawei_lts_log_group', 'huawei_lts_log_stream'],
  swr: ['huawei_swr_instance', 'huawei_swr_namespace', 'huawei_swr_repository', 'huawei_swr_tag'],
  sls: ['aliyun_sls_account', 'aliyun_sls_logstore'],
}

const SERVICE_LABELS: Record<CloudService, string> = {
  all: '全部服务', ecs: 'ECS', rds: 'RDS', elb: 'ELB', obs: 'OBS', lts: 'LTS', swr: 'SWR', sls: 'SLS',
}

const HUAWEI_SERVICES = [
  { key: 'ecs', type: 'huawei_ecs_instance', label: 'ECS 弹性云服务器', icon: Server },
  { key: 'rds', type: 'huawei_rds_instance', label: 'RDS 云数据库', icon: Database },
  { key: 'elb', type: 'huawei_elb_loadbalancer', label: 'ELB 弹性负载均衡', icon: Network },
  { key: 'obs', type: 'huawei_obs_bucket', label: 'OBS 对象存储桶', icon: HardDrive },
] as const

// Cloud credentials stay in the account overview. The catalog beneath it
// mirrors the provider hierarchy: account -> Region/Project -> service -> resource.
export function HuaweiResources({ resources, integrations, connectorName, query, onAction, onProject }: {
  resources: ApiResource[]
  integrations: Integration[]
  connectorName: (id: string) => string
  query: string
  onAction: (resource: ApiResource, action?: string) => void
  onProject: (resource: ApiResource) => void
}) {
  const cloudIntegrations = integrations.filter((item) => item.protocol === 'huaweicloud' || item.protocol === 'aliyun_sls')
  const [service, setService] = useState<CloudService>('all')
  const [region, setRegion] = useState('all')
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const q = query.trim().toLowerCase()
  const regions = useMemo(() => [...new Set(resources.map(resourceRegion).filter(Boolean))].sort(), [resources])
  const filtered = useMemo(() => {
    const scoped = resources.filter((resource) => {
      if (resource.resource_type === 'huawei_cloud_account') return false
      const serviceMatch = service === 'all' || SERVICE_TYPES[service].includes(resource.resource_type)
      const regionMatch = region === 'all' || resourceRegion(resource) === region
      return serviceMatch && regionMatch
    })
    if (!q) return scoped
    const matches = scoped.filter((resource) => `${resource.name} ${resourceRegion(resource)} ${resource.metadata.logGroupName ?? ''} ${resource.metadata.project ?? ''} ${resource.metadata.instanceName ?? ''} ${resource.metadata.namespaceName ?? ''} ${resource.metadata.repositoryName ?? ''} ${resource.metadata.digest ?? ''} ${resource.metadata.accessAddress ?? ''}`.toLowerCase().includes(q))
    const included = new Set(matches.map((resource) => resource.id))
    // Keep real parent/child context in search results instead of flattening a
    // matching Logstream or Logstore into an artificial top-level result.
    for (const resource of matches) {
      if (resource.resource_type === 'huawei_lts_log_stream') {
        scoped.filter((candidate) => candidate.resource_type === 'huawei_lts_log_group' && String(candidate.metadata.id ?? '') === String(resource.metadata.logGroupId ?? '')).forEach((candidate) => included.add(candidate.id))
      }
      if (resource.resource_type === 'huawei_lts_log_group') {
        scoped.filter((candidate) => candidate.resource_type === 'huawei_lts_log_stream' && String(candidate.metadata.logGroupId ?? '') === String(resource.metadata.id ?? '')).forEach((candidate) => included.add(candidate.id))
      }
      if (resource.resource_type === 'aliyun_sls_logstore') {
        scoped.filter((candidate) => candidate.resource_type === 'aliyun_sls_account' && candidate.name === String(resource.metadata.project ?? '')).forEach((candidate) => included.add(candidate.id))
      }
      if (resource.resource_type === 'aliyun_sls_account') {
        scoped.filter((candidate) => candidate.resource_type === 'aliyun_sls_logstore' && String(candidate.metadata.project ?? '') === resource.name).forEach((candidate) => included.add(candidate.id))
      }
      includeHuaweiSWRContext(resource, scoped, included)
    }
    return scoped.filter((resource) => included.has(resource.id))
  }, [resources, service, region, q])
  const selectedRegionScope = resources.find((item) => item.resource_type === 'huawei_cloud_account' && (region === 'all' || resourceRegion(item) === region))

  const toggle = (key: string) => setExpanded((current) => {
    const next = new Set(current)
    if (next.has(key)) next.delete(key); else next.add(key)
    return next
  })

  if (resources.length === 0 && cloudIntegrations.length === 0) {
    return <EmptyState icon={Cloud} title="暂无云厂商资源" text="接入华为云或阿里云后，账号和已授权服务会在这里展示。" />
  }

  const serviceTabs: { key: CloudService; icon: typeof Cloud; count: number }[] = [
    { key: 'all', icon: Layers3, count: resources.filter((item) => item.resource_type !== 'huawei_cloud_account').length },
    { key: 'ecs', icon: Server, count: countTypes(resources, SERVICE_TYPES.ecs) },
    { key: 'rds', icon: Database, count: countTypes(resources, SERVICE_TYPES.rds) },
    { key: 'elb', icon: Network, count: countTypes(resources, SERVICE_TYPES.elb) },
    { key: 'obs', icon: HardDrive, count: countTypes(resources, SERVICE_TYPES.obs) },
    { key: 'lts', icon: FileSearch, count: countTypes(resources, SERVICE_TYPES.lts) },
    { key: 'swr', icon: Layers3, count: countTypes(resources, SERVICE_TYPES.swr) },
    { key: 'sls', icon: FileSearch, count: countTypes(resources, SERVICE_TYPES.sls) },
  ]

  return <div className="cloud-catalog">
    <section className="cloud-account-section" aria-labelledby="cloud-account-heading">
      <div className="cloud-section-title"><div><ShieldCheck size={17} /><span><strong id="cloud-account-heading">云账号与接入身份</strong><small>AK/SK 只作为 Connector 侧凭据，不作为资源展示</small></span></div></div>
      <div className="cloud-account-grid">
        {cloudIntegrations.map((integration) => <CloudAccountCard key={integration.id} integration={integration} resources={resources.filter((item) => item.integration_id === integration.id)} connectorName={connectorName(integration.connector_id)} />)}
      </div>
    </section>

    <section className="cloud-services" aria-labelledby="cloud-services-heading">
      <div className="cloud-section-title"><div><Layers3 size={17} /><span><strong id="cloud-services-heading">云服务资源树</strong><small>云账号 → Region / Project → 服务 → 资源，默认折叠</small></span></div>
        <div className="cloud-service-controls">{regions.length > 0 && <label className="cloud-region-filter"><MapPin size={14} /><span>区域</span><div className="select-wrap"><select value={region} onChange={(event) => setRegion(event.target.value)}><option value="all">全部区域</option>{regions.map((item) => <option key={item} value={item}>{cloudRegionLabel(item)}</option>)}</select><ChevronDown size={14} /></div></label>}{selectedRegionScope && (service === 'all' || service === 'ecs') && <button type="button" className="secondary-button" onClick={() => onAction(selectedRegionScope, 'huawei.ecs.create')} title="在所选区域创建 ECS"><Settings2 size={14} />新建 ECS</button>}</div>
      </div>
      <div className="cloud-service-tabs" role="tablist" aria-label="按云服务筛选">
        {serviceTabs.map((tab) => { const Icon = tab.icon; return <button key={tab.key} type="button" role="tab" aria-selected={service === tab.key} className={service === tab.key ? 'selected' : ''} onClick={() => setService(tab.key)}><Icon size={16} /><span>{SERVICE_LABELS[tab.key]}</span><small>{tab.count}</small></button> })}
      </div>
      {filtered.length === 0 ? <EmptyState icon={Search} title="没有匹配的云服务资源" text={service === 'lts' ? '当前账号尚未发现 LTS 日志组或日志流，请检查 LTS 权限及 Region。' : service === 'swr' ? '当前账号尚未发现 SWR 企业版实例、命名空间、镜像仓库或标签，请检查 SWR 权限及 Region。' : service === 'sls' ? '请先接入阿里云 SLS Project，随后会自动发现 Logstore。' : '尝试调整服务、Region 或搜索条件。'} /> : <div className="cloud-resource-tree">
        {cloudIntegrations.length === 0 ? <ResourceLeaves resources={filtered} icon={Cloud} connectorName={connectorName} onAction={onAction} onProject={onProject} /> : cloudIntegrations.map((integration) => {
          const accountResources = filtered.filter((item) => item.integration_id === integration.id)
          if (accountResources.length === 0) return null
          return <CloudIntegrationTree key={integration.id} integration={integration} resources={accountResources} connectorName={connectorName} onAction={onAction} onProject={onProject} expanded={expanded} toggle={toggle} forceOpen={Boolean(q)} />
        })}
      </div>}
    </section>
  </div>
}

function CloudIntegrationTree({ integration, resources, connectorName, onAction, onProject, expanded, toggle, forceOpen }: {
  integration: Integration
  resources: ApiResource[]
  connectorName: (id: string) => string
  onAction: (resource: ApiResource, action?: string) => void
  onProject: (resource: ApiResource) => void
  expanded: Set<string>
  toggle: (key: string) => void
  forceOpen: boolean
}) {
  const rootKey = `account:${integration.id}`
  const huawei = integration.protocol === 'huaweicloud'
  return <TreeBranch nodeKey={rootKey} label={integration.name} subtitle={huawei ? '华为云账号' : '阿里云账号'} count={resources.length} icon={<Cloud size={16} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={0}>
    {huawei
      ? [...new Set(resources.map(resourceRegion).filter(Boolean))].sort().map((region) => <HuaweiRegionTree key={region} integrationId={integration.id} region={region} resources={resources.filter((item) => resourceRegion(item) === region)} connectorName={connectorName} onAction={onAction} onProject={onProject} expanded={expanded} toggle={toggle} forceOpen={forceOpen} />)
      : <AliyunRegionsTree integrationId={integration.id} resources={resources} connectorName={connectorName} onAction={onAction} onProject={onProject} expanded={expanded} toggle={toggle} forceOpen={forceOpen} />}
  </TreeBranch>
}

function HuaweiRegionTree({ integrationId, region, resources, connectorName, onAction, onProject, expanded, toggle, forceOpen }: {
  integrationId: string
  region: string
  resources: ApiResource[]
  connectorName: (id: string) => string
  onAction: (resource: ApiResource, action?: string) => void
  onProject: (resource: ApiResource) => void
  expanded: Set<string>
  toggle: (key: string) => void
  forceOpen: boolean
}) {
  const regionKey = `region:${integrationId}:${region}`
  return <TreeBranch nodeKey={regionKey} label={cloudRegionLabel(region)} subtitle={region} count={resources.length} icon={<MapPin size={15} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={1}>
    {HUAWEI_SERVICES.map((definition) => {
      const items = resources.filter((item) => item.resource_type === definition.type)
      if (items.length === 0) return null
      const Icon = definition.icon
      return <TreeBranch key={definition.key} nodeKey={`service:${integrationId}:${region}:${definition.key}`} label={definition.label} count={items.length} icon={<Icon size={15} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={2}>
        <ResourceLeaves resources={items} icon={Icon} connectorName={connectorName} onAction={onAction} onProject={onProject} />
      </TreeBranch>
    })}
    <HuaweiLTSTree integrationId={integrationId} region={region} resources={resources} connectorName={connectorName} onAction={onAction} onProject={onProject} expanded={expanded} toggle={toggle} forceOpen={forceOpen} />
    <HuaweiSWRTree integrationId={integrationId} region={region} resources={resources} connectorName={connectorName} onAction={onAction} onProject={onProject} expanded={expanded} toggle={toggle} forceOpen={forceOpen} />
  </TreeBranch>
}

function HuaweiLTSTree({ integrationId, region, resources, connectorName, onAction, onProject, expanded, toggle, forceOpen }: {
  integrationId: string
  region: string
  resources: ApiResource[]
  connectorName: (id: string) => string
  onAction: (resource: ApiResource, action?: string) => void
  onProject: (resource: ApiResource) => void
  expanded: Set<string>
  toggle: (key: string) => void
  forceOpen: boolean
}) {
  const groups = resources.filter((item) => item.resource_type === 'huawei_lts_log_group')
  const streams = resources.filter((item) => item.resource_type === 'huawei_lts_log_stream')
  const unmatchedStreams = streams.filter((stream) => !groups.some((group) => String(group.metadata.id ?? '') === String(stream.metadata.logGroupId ?? '')))
  const unmatchedGroups = [...new Set(unmatchedStreams.map((stream) => String(stream.metadata.logGroupId ?? stream.metadata.logGroupName ?? 'unknown')))]
  if (groups.length === 0 && streams.length === 0) return null
  return <TreeBranch nodeKey={`service:${integrationId}:${region}:lts`} label="LTS 云日志服务" count={groups.length + streams.length} icon={<FileSearch size={15} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={2}>
    {groups.map((group) => {
      const groupStreams = streams.filter((stream) => String(stream.metadata.logGroupId ?? '') === String(group.metadata.id ?? ''))
      return <TreeBranch key={group.id} nodeKey={`lts-group:${group.id}`} label={group.name} subtitle={group.metadata.ttlInDays ? `日志组 · 保留 ${group.metadata.ttlInDays} 天` : '日志组'} count={groupStreams.length} icon={<Layers3 size={15} />} accessory={<ResourceOwnershipButton resource={group} onProject={onProject} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={3}>
        <ResourceLeaves resources={groupStreams} icon={FileSearch} connectorName={connectorName} onAction={onAction} onProject={onProject} actionLabel="查询日志" />
      </TreeBranch>
    })}
    {unmatchedGroups.map((groupId) => {
      const children = unmatchedStreams.filter((stream) => String(stream.metadata.logGroupId ?? stream.metadata.logGroupName ?? 'unknown') === groupId)
      return <TreeBranch key={groupId} nodeKey={`lts-group:${integrationId}:${region}:${groupId}`} label={String(children[0]?.metadata.logGroupName ?? groupId)} subtitle="日志组" count={children.length} icon={<Layers3 size={15} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={3}>
        <ResourceLeaves resources={children} icon={FileSearch} connectorName={connectorName} onAction={onAction} onProject={onProject} actionLabel="查询日志" />
      </TreeBranch>
    })}
  </TreeBranch>
}

function HuaweiSWRTree({ integrationId, region, resources, connectorName, onAction, onProject, expanded, toggle, forceOpen }: {
  integrationId: string
  region: string
  resources: ApiResource[]
  connectorName: (id: string) => string
  onAction: (resource: ApiResource, action?: string) => void
  onProject: (resource: ApiResource) => void
  expanded: Set<string>
  toggle: (key: string) => void
  forceOpen: boolean
}) {
  const instances = resources.filter((item) => item.resource_type === 'huawei_swr_instance')
  const namespaces = resources.filter((item) => item.resource_type === 'huawei_swr_namespace')
  const repositories = resources.filter((item) => item.resource_type === 'huawei_swr_repository')
  const tags = resources.filter((item) => item.resource_type === 'huawei_swr_tag')
  const knownInstanceIDs = new Set(instances.map((item) => String(item.metadata.id ?? '')))
  const unmatchedInstanceIDs = [...new Set([...namespaces, ...repositories, ...tags].map((item) => String(item.metadata.instanceId ?? '')).filter((id) => id && !knownInstanceIDs.has(id)))]
  if (instances.length === 0 && namespaces.length === 0 && repositories.length === 0 && tags.length === 0) return null
  return <TreeBranch nodeKey={`service:${integrationId}:${region}:swr`} label="SWR 企业版镜像仓库" count={instances.length + namespaces.length + repositories.length + tags.length} icon={<Layers3 size={15} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={2}>
    {instances.map((instance) => {
      const instanceID = String(instance.metadata.id ?? '')
      const instanceNamespaces = namespaces.filter((item) => String(item.metadata.instanceId ?? '') === instanceID)
      const instanceRepositories = repositories.filter((item) => String(item.metadata.instanceId ?? '') === instanceID)
      const instanceTags = tags.filter((item) => String(item.metadata.instanceId ?? '') === instanceID)
      return <TreeBranch key={instance.id} nodeKey={`swr-instance:${instance.id}`} label={instance.name} subtitle={`企业版实例 · ${String(instance.metadata.accessAddress ?? instance.metadata.status ?? '')}`} count={instanceNamespaces.length + instanceRepositories.length + instanceTags.length} icon={<Layers3 size={15} />} onView={() => onAction(instance, 'huawei.swr.instance.get')} accessory={<ResourceOwnershipButton resource={instance} onProject={onProject} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={3}>
        {instanceNamespaces.map((namespace) => {
          const namespaceName = String(namespace.metadata.name ?? namespace.name)
          const namespaceRepositories = repositories.filter((item) => String(item.metadata.instanceId ?? '') === instanceID && String(item.metadata.namespaceName ?? '') === namespaceName)
          const namespaceTags = tags.filter((item) => String(item.metadata.instanceId ?? '') === instanceID && String(item.metadata.namespaceName ?? '') === namespaceName)
          return <TreeBranch key={namespace.id} nodeKey={`swr-namespace:${namespace.id}`} label={namespace.name} subtitle={namespace.metadata.public === 'true' ? '公开命名空间' : '私有命名空间'} count={namespaceRepositories.length + namespaceTags.length} icon={<Layers3 size={15} />} onView={() => onAction(namespace, 'huawei.swr.namespace.get')} accessory={<ResourceOwnershipButton resource={namespace} onProject={onProject} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={4}>
            {namespaceRepositories.map((repository) => {
              const repositoryName = String(repository.metadata.name ?? repository.name)
              const repositoryTags = tags.filter((item) => String(item.metadata.instanceId ?? '') === instanceID && String(item.metadata.namespaceName ?? '') === namespaceName && String(item.metadata.repositoryName ?? '') === repositoryName)
              return <TreeBranch key={repository.id} nodeKey={`swr-repository:${repository.id}`} label={repository.name} subtitle={`镜像仓库 · ${String(repository.metadata.tagCount ?? repositoryTags.length)} 个标签`} count={repositoryTags.length} icon={<Layers3 size={15} />} onView={() => onAction(repository, 'huawei.swr.repository.get')} accessory={<ResourceOwnershipButton resource={repository} onProject={onProject} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={5}>
                <ResourceLeaves resources={repositoryTags} icon={Layers3} connectorName={connectorName} onAction={onAction} onProject={onProject} actionLabel="查看标签" />
              </TreeBranch>
            })}
          </TreeBranch>
        })}
      </TreeBranch>
    })}
    {unmatchedInstanceIDs.map((instanceID) => {
      const instanceNamespaces = namespaces.filter((item) => String(item.metadata.instanceId ?? '') === instanceID)
      const instanceRepositories = repositories.filter((item) => String(item.metadata.instanceId ?? '') === instanceID)
      const instanceTags = tags.filter((item) => String(item.metadata.instanceId ?? '') === instanceID)
      const label = String(instanceNamespaces[0]?.metadata.instanceName ?? instanceRepositories[0]?.metadata.instanceName ?? instanceTags[0]?.metadata.instanceName ?? instanceID)
      return <TreeBranch key={`swr-missing-instance:${integrationId}:${region}:${instanceID}`} nodeKey={`swr-instance-missing:${integrationId}:${region}:${instanceID}`} label={label} subtitle="企业版实例" count={instanceNamespaces.length + instanceRepositories.length + instanceTags.length} icon={<Layers3 size={15} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={3}>
        {instanceNamespaces.map((namespace) => {
          const namespaceName = String(namespace.metadata.name ?? namespace.name)
          const namespaceRepositories = repositories.filter((item) => String(item.metadata.instanceId ?? '') === instanceID && String(item.metadata.namespaceName ?? '') === namespaceName)
          const namespaceTags = tags.filter((item) => String(item.metadata.instanceId ?? '') === instanceID && String(item.metadata.namespaceName ?? '') === namespaceName)
          return <TreeBranch key={namespace.id} nodeKey={`swr-namespace:${namespace.id}`} label={namespace.name} subtitle={namespace.metadata.public === 'true' ? '公开命名空间' : '私有命名空间'} count={namespaceRepositories.length + namespaceTags.length} icon={<Layers3 size={15} />} onView={() => onAction(namespace, 'huawei.swr.namespace.get')} accessory={<ResourceOwnershipButton resource={namespace} onProject={onProject} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={4}>
            {namespaceRepositories.map((repository) => {
              const repositoryName = String(repository.metadata.name ?? repository.name)
              const repositoryTags = tags.filter((item) => String(item.metadata.instanceId ?? '') === instanceID && String(item.metadata.namespaceName ?? '') === namespaceName && String(item.metadata.repositoryName ?? '') === repositoryName)
              return <TreeBranch key={repository.id} nodeKey={`swr-repository:${repository.id}`} label={repository.name} subtitle={`镜像仓库 · ${String(repository.metadata.tagCount ?? repositoryTags.length)} 个标签`} count={repositoryTags.length} icon={<Layers3 size={15} />} onView={() => onAction(repository, 'huawei.swr.repository.get')} accessory={<ResourceOwnershipButton resource={repository} onProject={onProject} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={5}>
                <ResourceLeaves resources={repositoryTags} icon={Layers3} connectorName={connectorName} onAction={onAction} onProject={onProject} actionLabel="查看标签" />
              </TreeBranch>
            })}
          </TreeBranch>
        })}
      </TreeBranch>
    })}
  </TreeBranch>
}

function AliyunRegionsTree({ integrationId, resources, connectorName, onAction, onProject, expanded, toggle, forceOpen }: {
  integrationId: string
  resources: ApiResource[]
  connectorName: (id: string) => string
  onAction: (resource: ApiResource, action?: string) => void
  onProject: (resource: ApiResource) => void
  expanded: Set<string>
  toggle: (key: string) => void
  forceOpen: boolean
}) {
  const regions = [...new Set(resources.map(resourceRegion).filter(Boolean))].sort()
  if (regions.length === 0) {
    return <AliyunProjectTree integrationId={integrationId} region="legacy" resources={resources} connectorName={connectorName} onAction={onAction} onProject={onProject} expanded={expanded} toggle={toggle} forceOpen={forceOpen} />
  }
  return <>{regions.map((region) => {
    const children = resources.filter((resource) => resourceRegion(resource) === region)
    return <TreeBranch key={region} nodeKey={`aliyun-region:${integrationId}:${region}`} label={aliyunRegionLabel(region)} subtitle={region} count={children.length} icon={<MapPin size={15} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={1}>
      <AliyunProjectTree integrationId={integrationId} region={region} resources={children} connectorName={connectorName} onAction={onAction} onProject={onProject} expanded={expanded} toggle={toggle} forceOpen={forceOpen} />
    </TreeBranch>
  })}</>
}

function AliyunProjectTree({ integrationId, region, resources, connectorName, onAction, onProject, expanded, toggle, forceOpen }: {
  integrationId: string
  region: string
  resources: ApiResource[]
  connectorName: (id: string) => string
  onAction: (resource: ApiResource, action?: string) => void
  onProject: (resource: ApiResource) => void
  expanded: Set<string>
  toggle: (key: string) => void
  forceOpen: boolean
}) {
  const projects = resources.filter((item) => item.resource_type === 'aliyun_sls_account')
  const logstores = resources.filter((item) => item.resource_type === 'aliyun_sls_logstore')
  const unmatchedLogstores = logstores.filter((item) => !projects.some((project) => project.name === String(item.metadata.project ?? '')))
  const unmatchedProjects = [...new Set(unmatchedLogstores.map((item) => String(item.metadata.project ?? 'SLS Project')))]
  return <>{projects.map((project) => {
    const children = logstores.filter((item) => String(item.metadata.project ?? '') === project.name)
    return <TreeBranch key={project.id} nodeKey={`sls-project:${project.id}`} label={project.name} subtitle={`SLS Project · ${String(project.metadata.endpoint ?? '-')}`} count={children.length} icon={<Layers3 size={15} />} accessory={<ResourceOwnershipButton resource={project} onProject={onProject} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={2}>
      <ResourceLeaves resources={children} icon={FileSearch} connectorName={connectorName} onAction={onAction} onProject={onProject} actionLabel="查询日志" />
    </TreeBranch>
  })}{unmatchedProjects.map((projectName) => {
    const children = unmatchedLogstores.filter((item) => String(item.metadata.project ?? 'SLS Project') === projectName)
    return <TreeBranch key={projectName} nodeKey={`sls-project:${integrationId}:${region}:${projectName}`} label={projectName} subtitle="SLS Project" count={children.length} icon={<Layers3 size={15} />} expanded={expanded} toggle={toggle} forceOpen={forceOpen} level={2}>
      <ResourceLeaves resources={children} icon={FileSearch} connectorName={connectorName} onAction={onAction} onProject={onProject} actionLabel="查询日志" />
    </TreeBranch>
  })}</>
}

function ResourceLeaves({ resources, icon: Icon, connectorName, onAction, onProject, actionLabel }: {
  resources: ApiResource[]
  icon: typeof Cloud
  connectorName: (id: string) => string
  onAction: (resource: ApiResource, action?: string) => void
  onProject: (resource: ApiResource) => void
  actionLabel?: string
}) {
  if (resources.length === 0) return <div className="cloud-tree-empty">暂无子资源</div>
  return <div className="res-list cloud-tree-leaves">{resources.map((resource) => <ResourceCard key={resource.id} resource={resource} connectorName={connectorName(resource.connector_id)} onAction={onAction} onProject={onProject} icon={<Icon size={20} />} actionLabel={actionLabel}>{resourceMeta(resource)}</ResourceCard>)}</div>
}

function TreeBranch({ nodeKey, label, subtitle, count, icon, onView, accessory, expanded, toggle, forceOpen, level, children }: {
  nodeKey: string
  label: string
  subtitle?: string
  count: number
  icon: ReactNode
  onView?: () => void
  accessory?: ReactNode
  expanded: Set<string>
  toggle: (key: string) => void
  forceOpen: boolean
  level: number
  children: ReactNode
}) {
  const open = forceOpen || expanded.has(nodeKey)
  return <section className={`cloud-tree-node cloud-tree-level-${level}`}>
    <div className="cloud-tree-node-row">
      <button type="button" className="cloud-tree-node-toggle" onClick={() => toggle(nodeKey)} aria-expanded={open}>
        <ChevronRight size={14} className={`chevron${open ? ' chevron-open' : ''}`} />{icon}<span><strong>{label}</strong>{subtitle && <small>{subtitle}</small>}</span><b>{count}</b>
      </button>
      {onView && <button type="button" className="cloud-tree-view-button" title={`查看 ${label} 详情`} aria-label={`查看 ${label} 详情`} onClick={onView}><Eye size={14} /></button>}
      {accessory && <div className="cloud-tree-node-accessory">{accessory}</div>}
    </div>
    {open && <div className="cloud-tree-children">{children}</div>}
  </section>
}

function CloudAccountCard({ integration, resources, connectorName }: { integration: Integration; resources: ApiResource[]; connectorName: string }) {
  const huawei = integration.protocol === 'huaweicloud'
  const regions = new Set(resources.map(resourceRegion).filter(Boolean))
  const services = huawei
    ? ['ecs', 'rds', 'elb', 'obs', 'lts', 'swr'].filter((name) => resources.some((item) => resourceService(item.resource_type) === name || (item.resource_type === 'huawei_cloud_account' && (item.metadata.services as Record<string, unknown> | undefined)?.[name] === 'ACTIVE')))
    : ['sls']
  return <article className="cloud-account-card">
    <div className="cloud-account-card-heading"><div className={`cloud-provider-mark ${huawei ? 'huawei' : 'aliyun'}`}><Cloud size={20} /></div><div><strong>{integration.name}</strong><span>{huawei ? '华为云 · IAM 接入' : '阿里云 · RAM 接入'}</span></div><span className={`integration-state state-${integration.status.toLowerCase()}`}>{integration.status === 'ACTIVE' ? '连接正常' : integration.status}</span></div>
    <dl>
      <div><dt>接入身份</dt><dd>{integration.username?.trim() || (huawei ? '未标注 IAM 用户' : '未标注 RAM 用户')}</dd></div>
      <div><dt>凭据</dt><dd><ShieldCheck size={13} />已在执行通道配置</dd></div>
      <div><dt>{huawei ? '可用 Region' : 'SLS Project'}</dt><dd>{huawei ? regions.size : resources.filter((item) => item.resource_type === 'aliyun_sls_account').length}</dd></div>
      <div><dt>执行通道</dt><dd>{connectorName || '-'}</dd></div>
    </dl>
    <div className="cloud-account-services">{services.length > 0 ? services.map((item) => <span key={item}>{item.toUpperCase()}</span>) : <span>等待资源发现</span>}</div>
  </article>
}

function Meta({ label, value }: { label: string; value: unknown }) {
  return <div className="res-card-meta"><span>{label}</span><strong>{String(value ?? '-')}</strong></div>
}

function resourceMeta(resource: ApiResource) {
  if (resource.resource_type === 'huawei_ecs_instance') return <Meta label="规格" value={resource.metadata.flavor} />
  if (resource.resource_type === 'huawei_rds_instance') return <Meta label="引擎" value={`${resource.metadata.engine ?? '-'} ${resource.metadata.engineVersion ?? ''}`} />
  if (resource.resource_type === 'huawei_elb_loadbalancer') return <Meta label="VIP" value={resource.metadata.vipAddress} />
  if (resource.resource_type === 'huawei_obs_bucket') return <Meta label="位置" value={resource.metadata.location} />
  if (resource.resource_type === 'huawei_lts_log_stream') return <Meta label="日志组" value={resource.metadata.logGroupName} />
  if (resource.resource_type === 'huawei_swr_instance') return <Meta label="访问地址" value={resource.metadata.accessAddress} />
  if (resource.resource_type === 'huawei_swr_namespace') return <Meta label="镜像仓库" value={resource.metadata.repositoryCount} />
  if (resource.resource_type === 'huawei_swr_repository') return <Meta label="标签数" value={resource.metadata.tagCount} />
  if (resource.resource_type === 'huawei_swr_tag') return <Meta label="摘要" value={shortDigest(resource.metadata.digest)} />
  if (resource.resource_type === 'aliyun_sls_logstore') return <Meta label="Project" value={resource.metadata.project} />
  return null
}

function includeHuaweiSWRContext(resource: ApiResource, scoped: ApiResource[], included: Set<string>) {
  if (!resource.resource_type.startsWith('huawei_swr_')) return
  const instanceID = String(resource.metadata.instanceId ?? (resource.resource_type === 'huawei_swr_instance' ? resource.metadata.id ?? '' : ''))
  const namespaceName = String(resource.metadata.namespaceName ?? (resource.resource_type === 'huawei_swr_namespace' ? resource.metadata.name ?? resource.name : ''))
  const repositoryName = String(resource.metadata.repositoryName ?? (resource.resource_type === 'huawei_swr_repository' ? resource.metadata.name ?? resource.name : ''))
  const sameInstance = (candidate: ApiResource) => String(candidate.metadata.instanceId ?? (candidate.resource_type === 'huawei_swr_instance' ? candidate.metadata.id ?? '' : '')) === instanceID
  if (instanceID) {
    scoped.filter((candidate) => candidate.resource_type === 'huawei_swr_instance' && String(candidate.metadata.id ?? '') === instanceID).forEach((candidate) => included.add(candidate.id))
  }
  if (resource.resource_type === 'huawei_swr_instance') {
    scoped.filter((candidate) => sameInstance(candidate) && candidate.resource_type !== 'huawei_swr_instance').forEach((candidate) => included.add(candidate.id))
    return
  }
  if (namespaceName) {
    scoped.filter((candidate) => candidate.resource_type === 'huawei_swr_namespace' && sameInstance(candidate) && String(candidate.metadata.name ?? candidate.name) === namespaceName).forEach((candidate) => included.add(candidate.id))
  }
  if (resource.resource_type === 'huawei_swr_namespace') {
    scoped.filter((candidate) => sameInstance(candidate) && (candidate.resource_type === 'huawei_swr_repository' || candidate.resource_type === 'huawei_swr_tag') && String(candidate.metadata.namespaceName ?? '') === namespaceName).forEach((candidate) => included.add(candidate.id))
    return
  }
  if (repositoryName) {
    scoped.filter((candidate) => candidate.resource_type === 'huawei_swr_repository' && sameInstance(candidate) && String(candidate.metadata.namespaceName ?? '') === namespaceName && String(candidate.metadata.name ?? candidate.name) === repositoryName).forEach((candidate) => included.add(candidate.id))
  }
  if (resource.resource_type === 'huawei_swr_repository') {
    scoped.filter((candidate) => candidate.resource_type === 'huawei_swr_tag' && sameInstance(candidate) && String(candidate.metadata.namespaceName ?? '') === namespaceName && String(candidate.metadata.repositoryName ?? '') === repositoryName).forEach((candidate) => included.add(candidate.id))
  }
}

function shortDigest(value: unknown) {
  const text = String(value ?? '-')
  return text.length > 24 ? `${text.slice(0, 21)}...` : text
}

function resourceRegion(resource: ApiResource) {
  return String(resource.metadata.region ?? resource.metadata.location ?? '')
}

function countTypes(resources: ApiResource[], types: string[]) {
  return resources.filter((item) => types.includes(item.resource_type)).length
}

function resourceService(type: string) {
  if (type.includes('_ecs_')) return 'ecs'
  if (type.includes('_rds_')) return 'rds'
  if (type.includes('_elb_')) return 'elb'
  if (type.includes('_obs_')) return 'obs'
  if (type.includes('_lts_')) return 'lts'
  if (type.includes('_swr_')) return 'swr'
  if (type.startsWith('aliyun_sls_')) return 'sls'
  return ''
}
