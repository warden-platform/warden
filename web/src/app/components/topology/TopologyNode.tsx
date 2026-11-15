import { memo } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import {
  Boxes, Cloud, Container, Cog, GitBranch, ListChecks, Network, Server, Workflow, type LucideIcon,
} from 'lucide-react'
import type { TopoNodeData, TopoStatus } from '../../lib/topology/buildTopology'

function iconFor(kind: string, entity: string): LucideIcon {
  if (entity === 'action') return ListChecks
  if (kind.startsWith('huawei')) return Cloud
  switch (kind) {
    case 'kubernetes': return Cloud
    case 'network': return Network
    case 'linux': return Server
    case 'linux_host': return Server
    case 'systemd_service': return Cog
    case 'docker_container': return Container
    case 'pod': return Container
    case 'deployment': return Boxes
    case 'jenkins':
    case 'jenkins_job':
    case 'jenkins_build':
    case 'jenkins_controller':
    case 'jenkins_node':
    case 'jenkins_queue_item':
      return Workflow
    case 'postgres': return Workflow
    default: return Boxes
  }
}

const statusRing: Record<TopoStatus, string> = {
  active: 'bg-green-500',
  warning: 'bg-amber-500',
  error: 'bg-red-500',
  neutral: 'bg-gray-400',
}

const statusIconBg: Record<TopoStatus, string> = {
  active: 'bg-green-50 text-green-600 dark:bg-green-950 dark:text-green-400',
  warning: 'bg-amber-50 text-amber-600 dark:bg-amber-950 dark:text-amber-400',
  error: 'bg-red-50 text-red-600 dark:bg-red-950 dark:text-red-400',
  neutral: 'bg-surface-muted text-content-subtle dark:bg-surface-dark-muted dark:text-content-dark-subtle',
}

const K8S_KINDS = new Set(['kubernetes', 'pod', 'deployment', 'statefulset', 'daemonset', 'replicaset', 'job', 'cronjob', 'service', 'endpoints', 'ingress', 'configmap', 'secret', 'event', 'node', 'namespace'])

// Domain accent dot: lets users visually group nodes by ecosystem at a glance.
export function domainDotClass(kind: string): string {
  if (K8S_KINDS.has(kind)) return 'bg-sky-500'
  if (kind.startsWith('jenkins')) return 'bg-orange-500'
  if (kind.startsWith('grafana')) return 'bg-yellow-500'
  if (kind.startsWith('database') || kind === 'postgresql' || kind === 'mysql') return 'bg-emerald-500'
  if (kind.startsWith('huawei')) return 'bg-red-500'
  if (kind === 'network') return 'bg-emerald-500'
  if (kind === 'linux' || kind === 'linux_host' || kind === 'systemd_service' || kind === 'docker_container') return 'bg-violet-500'
  return 'bg-slate-300 dark:bg-slate-600'
}

function TopologyNode({ data, selected }: NodeProps) {
  const d = data as unknown as TopoNodeData
  const Icon = iconFor(d.kind, d.entity)
  return (
    <div
      className={`flex w-[220px] items-center gap-3 rounded-lg border bg-white/80 px-3 py-2.5 shadow-glass backdrop-blur-xl transition dark:bg-surface-dark/80 dark:shadow-glass-dark ${
        selected ? 'border-brand ring-2 ring-brand/30 dark:border-brand-dark dark:ring-brand-dark/30' : 'border-white/70 dark:border-white/10'
      }`}
    >
      <Handle type="target" position={Position.Left} className="!h-2 !w-2 !border-0 !bg-gray-400" />
      <div className={`grid h-9 w-9 flex-none place-items-center rounded-lg ${statusIconBg[d.status]}`}>
        <Icon size={18} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span className={`h-1.5 w-1.5 flex-none rounded-full ${domainDotClass(d.kind)}`} title="所属生态" />
          <span className={`h-1.5 w-1.5 flex-none rounded-full ${statusRing[d.status]}`} />
          <strong className="truncate text-sm font-semibold text-content dark:text-content-dark">{d.label}</strong>
        </div>
        {d.sublabel && <p className="mt-0.5 truncate text-[11px] text-content-subtle dark:text-content-dark-subtle">{d.sublabel}</p>}
      </div>
      {typeof d.count === 'number' && (
        <span className="ml-auto flex-none rounded-md bg-brand/10 px-1.5 py-0.5 text-[10px] font-bold text-brand dark:bg-brand-dark/15 dark:text-brand-dark">{d.count}</span>
      )}
      <Handle type="source" position={Position.Right} className="!h-2 !w-2 !border-0 !bg-gray-400" />
    </div>
  )
}

export default memo(TopologyNode)
