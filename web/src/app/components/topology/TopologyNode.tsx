import { memo } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { Boxes, Cloud, Container, Cog, Database, ListChecks, Network, Plug, Server, Workflow, type LucideIcon } from 'lucide-react'
import type { TopoNodeData, TopoStatus } from '../../lib/topology/graph'

function iconFor(kind: string, entity: string): LucideIcon {
  if (entity === 'action') return ListChecks
  if (entity === 'connector') return Plug
  if (kind.startsWith('huawei')) return Cloud
  switch (kind) {
    case 'kubernetes':
      return Cloud
    case 'network':
      return Network
    case 'linux':
    case 'linux_host':
      return Server
    case 'systemd_service':
      return Cog
    case 'docker_container':
    case 'pod':
      return Container
    case 'deployment':
      return Boxes
    case 'jenkins':
    case 'jenkins_job':
    case 'jenkins_build':
      return Workflow
    case 'postgres':
    case 'mysql':
      return Database
    default:
      return Boxes
  }
}

const K8S_KINDS = new Set(['kubernetes', 'pod', 'deployment', 'statefulset', 'daemonset', 'replicaset', 'job', 'cronjob', 'service', 'endpoints', 'ingress', 'configmap', 'secret', 'event', 'node', 'namespace'])

function domainTileClass(kind: string, entity: string): string {
  if (entity === 'connector') return 'bg-indigo-500/10 text-indigo-600 ring-indigo-500/20 dark:text-indigo-400'
  if (K8S_KINDS.has(kind)) return 'bg-sky-500/10 text-sky-600 ring-sky-500/20 dark:text-sky-400'
  if (kind.startsWith('jenkins')) return 'bg-orange-500/10 text-orange-600 ring-orange-500/20 dark:text-orange-400'
  if (kind.startsWith('grafana')) return 'bg-yellow-500/10 text-yellow-600 ring-yellow-500/20 dark:text-yellow-400'
  if (kind.startsWith('database') || kind === 'postgresql' || kind === 'mysql') return 'bg-emerald-500/10 text-emerald-600 ring-emerald-500/20 dark:text-emerald-400'
  if (kind.startsWith('huawei') || kind === 'aliyun_sls') return 'bg-rose-500/10 text-rose-600 ring-rose-500/20 dark:text-rose-400'
  if (kind === 'network' || kind === 'h3c_comware_ssh') return 'bg-teal-500/10 text-teal-600 ring-teal-500/20 dark:text-teal-400'
  if (kind === 'linux' || kind === 'linux_host' || kind === 'systemd_service' || kind === 'docker_container') return 'bg-violet-500/10 text-violet-600 ring-violet-500/20 dark:text-violet-400'
  return 'bg-slate-500/10 text-slate-600 ring-slate-500/20 dark:text-slate-300'
}

const statusAccent: Record<TopoStatus, string> = {
  active: 'bg-green-500',
  warning: 'bg-amber-500',
  error: 'bg-red-500',
  neutral: 'bg-slate-400',
}

const statusDot: Record<TopoStatus, string> = {
  active: 'bg-green-500 shadow-[0_0_6px_rgba(34,197,94,0.7)]',
  warning: 'bg-amber-500',
  error: 'bg-red-500',
  neutral: 'bg-slate-400',
}

const statusLabel: Record<TopoStatus, string> = {
  active: '正常',
  warning: '注意',
  error: '异常',
  neutral: '中性',
}

const entityBadge: Record<string, { label: string; className: string }> = {
  connector: { label: '执行通道', className: 'text-indigo-600 ring-indigo-500/25 bg-indigo-500/10 dark:text-indigo-400' },
  integration: { label: '中间件', className: 'text-violet-600 ring-violet-500/25 bg-violet-500/10 dark:text-violet-400' },
  'resource-group': { label: '资源', className: 'text-emerald-600 ring-emerald-500/25 bg-emerald-500/10 dark:text-emerald-400' },
  action: { label: '动作', className: 'text-slate-500 ring-slate-400/25 bg-slate-500/10 dark:text-slate-300' },
}

function TopologyNode({ data, selected }: NodeProps) {
  const d = data as unknown as TopoNodeData
  const Icon = iconFor(d.kind, d.entity)
  return (
    <div
      title={`${d.label}${d.sublabel ? ` · ${d.sublabel}` : ''}`}
      className={`group relative flex w-[232px] items-center gap-3 overflow-hidden rounded-xl border bg-surface px-3 py-2.5 shadow-card transition-all duration-150 dark:bg-surface-dark ${
        selected
          ? 'border-brand shadow-lift ring-2 ring-brand/25 dark:border-brand-dark dark:ring-brand-dark/25'
          : 'border-border hover:-translate-y-0.5 hover:border-brand/40 hover:shadow-lift dark:border-border-dark dark:hover:border-brand-dark/40'
      }`}
    >
      <span className={`absolute inset-y-2 left-0 w-[3px] rounded-r-full ${statusAccent[d.status]}`} aria-hidden />
      {d.live && <span className="topo-live-dot" title="有执行信号正在经过" aria-label="有执行信号正在经过" />}
      <Handle type="target" position={Position.Left} className="!h-1.5 !w-1.5 !border-0 !bg-transparent" />
      <div className={`grid h-9 w-9 flex-none place-items-center rounded-[10px] ring-1 ring-inset ${domainTileClass(d.kind, d.entity)}`}>
        {/* eslint-disable-next-line react-hooks/static-components -- iconFor 返回模块级 lucide 组件，按 kind/entity 稳定 */}
        <Icon size={17} strokeWidth={1.9} />
      </div>
      <div className="min-w-0 flex-1">
        <strong className="block truncate text-[13px] font-semibold leading-5 text-content dark:text-content-dark">{d.label}</strong>
        <span className={`mt-0.5 inline-flex items-center rounded px-1.5 py-px text-[9px] font-semibold leading-[13px] ring-1 ring-inset ${entityBadge[d.entity]?.className ?? ''}`}>
          {entityBadge[d.entity]?.label ?? d.entity}
        </span>
      </div>
      <div className="flex flex-none flex-col items-end gap-1.5">
        <span className={`h-2 w-2 rounded-full ${statusDot[d.status]}`} title={statusLabel[d.status]} aria-label={statusLabel[d.status]} />
        {typeof d.count === 'number' && d.count > 1 && (
          <span className="rounded-md border border-brand/15 bg-brand/5 px-1.5 py-px text-[10px] font-semibold tabular-nums text-brand dark:border-brand-dark/20 dark:bg-brand-dark/10 dark:text-brand-dark">
            {d.count}
          </span>
        )}
      </div>
      <Handle type="source" position={Position.Right} className="!h-1.5 !w-1.5 !border-0 !bg-transparent" />
    </div>
  )
}

export default memo(TopologyNode)