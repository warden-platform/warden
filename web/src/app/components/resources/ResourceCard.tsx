// Shared resource card row: icon + name/type + domain-specific meta slot + status/action.
// Used by all three domain views so card chrome stays consistent.
import type { ReactNode } from 'react'
import { FolderKanban, Play, Tags } from 'lucide-react'
import type { ApiResource } from '../../../shared/api'
import { Badge } from '../Badge'
import { apiResourceToView } from '../../lib/mappers'
import { mapEnvironment } from '../../../shared/types'

const K8S_TYPES = new Set([
  'pod',
  'deployment',
  'event',
  'statefulset',
  'daemonset',
  'replicaset',
  'job',
  'cronjob',
  'service',
  'endpoints',
  'ingress',
  'configmap',
  'secret',
  'node',
  'namespace',
])

export function ResourceCard({
  resource,
  connectorName,
  onAction,
  onProject,
  selected,
  onSelect,
  children,
  icon,
  stale,
  actionLabel,
}: {
  resource: ApiResource
  connectorName: string
  onAction: (r: ApiResource) => void
  onProject?: (r: ApiResource) => void
  selected?: boolean
  onSelect?: (id: string) => void
  children?: ReactNode
  icon?: ReactNode
  stale?: boolean
  actionLabel?: string
}) {
  const view = apiResourceToView(resource, connectorName)
  const historical = resource.status === 'ORPHANED'
  const t = resource.resource_type
  const iconTone = K8S_TYPES.has(t)
    ? 'k8s'
    : t.startsWith('jenkins')
      ? 'jenkins'
      : t.startsWith('grafana')
        ? 'grafana'
        : t.startsWith('database')
          ? 'database'
          : t.startsWith('network_')
            ? 'network'
            : 'linux'
  return (
    <div
      className={`res-card ${selected ? 'res-card-selected' : ''} ${stale ? 'res-card-stale' : ''}`}
      onClick={onSelect ? () => onSelect(resource.id) : undefined}
      role={onSelect ? 'button' : undefined}
    >
      <div className={`resource-icon ${iconTone}`}>{icon}</div>
      <div className="res-card-main">
        <strong>{view.name}</strong>
        <span>{view.type}</span>
      </div>
      {children}
      <ResourceOwnershipButton resource={resource} onProject={onProject} />
      <div className="res-card-actions">
        <Badge tone={resource.action_access === 'RESTRICTED_CHANGE' ? 'warning' : resource.action_access === 'CHANGE' ? 'info' : 'neutral'}>
          {resource.action_access === 'RESTRICTED_CHANGE' ? '受限变更' : resource.action_access === 'CHANGE' ? '可变更' : '只读'}
        </Badge>
        {resource.action_upgrade_required && <Badge tone="warning">需升级</Badge>}
        <Badge tone={view.status === '正常' ? 'success' : view.status === '历史' ? 'neutral' : 'warning'}>{view.status}</Badge>
        <button
          className="secondary-button"
          disabled={historical || resource.available_action_count === 0}
          title={
            historical
              ? '历史资源不可执行'
              : resource.action_upgrade_required
                ? '需要升级执行通道后才能执行动作'
                : resource.available_action_count === 0
                  ? '当前资源没有可用动作'
                  : '请求受控动作'
          }
          onClick={(e) => {
            e.stopPropagation()
            onAction(resource)
          }}
        >
          <Play size={15} />
          {historical || resource.available_action_count === 0 ? '不可执行' : actionLabel || '请求动作'}
        </button>
      </div>
    </div>
  )
}

export function ResourceOwnershipButton({ resource, onProject }: { resource: ApiResource; onProject?: (resource: ApiResource) => void }) {
  if (!resource.project_name && !resource.environment) return null
  return (
    <button
      type="button"
      className="resource-project-button"
      title="调整资源项目与环境归属"
      aria-label={`${resource.name}：${resource.project_name || '未归属项目'}，${mapEnvironment(resource.environment)}；点击调整归属`}
      onClick={(event) => {
        event.stopPropagation()
        onProject?.(resource)
      }}
      disabled={!onProject}
    >
      <FolderKanban size={14} />
      <span>
        <strong>{resource.project_name || '未归属项目'}</strong>
        <small>{projectSourceLabel(resource.project_source)}</small>
      </span>
      <span className={`resource-environment environment-${resource.environment}`}>
        <Tags size={11} />
        <strong>{mapEnvironment(resource.environment)}</strong>
        <small>{environmentSourceLabel(resource.environment_source)}</small>
      </span>
    </button>
  )
}

const projectSourceLabel = (source?: ApiResource['project_source']) =>
  ({
    RESOURCE: '资源覆盖',
    INTEGRATION_RULE: 'Integration 路由',
    CLOUD_TAG: '云标签',
    NAMESPACE: 'Namespace 规则',
    INTEGRATION: 'Integration 默认',
    CONNECTOR: '通道默认',
    DEFAULT: '系统默认',
  })[source ?? 'DEFAULT']

const environmentSourceLabel = (source?: ApiResource['environment_source']) =>
  ({
    RESOURCE: '资源覆盖',
    DATABASE_SCHEMA: '继承 Schema',
    DATABASE_INSTANCE: '继承数据库实例',
    INTEGRATION: 'Integration 默认',
    CONNECTOR: '通道默认',
  })[source ?? 'CONNECTOR']
