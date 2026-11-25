import { ArrowUpRight, X } from 'lucide-react'
import type { TopologyConnector, TopologyGroup, TopologyIntegration } from '../../../shared/api'
import { resourceTypeLabel } from '../../lib/labels'
import { channelTypeLabel, type TopoNodeData, type TopoStatus } from '../../lib/topology/graph'

const statusLabel: Record<TopoStatus, string> = {
  active: '正常',
  warning: '注意',
  error: '异常',
  neutral: '中性',
}

const statusBadge: Record<TopoStatus, string> = {
  active: 'border-green-500/25 bg-green-500/10 text-green-600 dark:text-green-400',
  warning: 'border-amber-500/25 bg-amber-500/10 text-amber-600 dark:text-amber-400',
  error: 'border-red-500/25 bg-red-500/10 text-red-600 dark:text-red-400',
  neutral: 'border-slate-400/25 bg-slate-500/10 text-slate-500 dark:text-slate-300',
}

const entityLabel: Record<string, string> = {
  connector: '执行通道',
  integration: '中间件',
  'resource-group': '资源分组',
  action: '可用动作',
}

const riskLabel: Record<string, string> = {
  low: '低风险',
  medium: '中风险',
  high: '高风险',
  critical: '严重风险',
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-2">
      <dt className="text-content-subtle dark:text-content-dark-subtle">{label}</dt>
      <dd className="max-w-[60%] break-all text-right font-medium text-content dark:text-content-dark">{value}</dd>
    </div>
  )
}

export function TopologyDetails({
  data,
  onClose,
  onNavigate,
}: {
  data: TopoNodeData | null
  onClose: () => void
  onNavigate: (path: string) => void
}) {
  if (!data) {
    return (
      <div className="flex h-full flex-col rounded-xl border border-border bg-surface p-4 shadow-card dark:border-border-dark dark:bg-surface-dark">
        <p className="text-[10px] font-semibold uppercase tracking-wider text-brand dark:text-brand-dark">拓扑图谱</p>
        <h3 className="mt-1 text-sm font-semibold text-content dark:text-content-dark">整张图已是资源全貌</h3>
        <p className="mt-1 text-[11px] leading-relaxed text-content-subtle dark:text-content-dark-subtle">
          从左到右依次是执行通道 → 集成 → 资源分组 → 动作。资源分组按类型折叠并标注数量，点击任意节点查看真实资源样本与可用动作。
        </p>
        <div className="mt-auto flex items-start gap-2 rounded-lg border border-dashed border-border px-3 py-2.5 text-[11px] leading-relaxed text-content-subtle dark:border-border-dark dark:text-content-dark-subtle">
          <X size={0} />
          <span>滚轮缩放、拖拽平移；节点可拖动重排，双击进入对应目录或详情。</span>
        </div>
      </div>
    )
  }

  const connector = data.entity === 'connector' ? (data.raw as TopologyConnector) : null
  const integration = data.entity === 'integration' ? (data.raw as TopologyIntegration) : null
  const group = data.entity === 'resource-group' ? (data.raw as TopologyGroup) : null
  const actionRefs = data.entity === 'action' ? (data.actions ?? []) : []

  return (
    <div className="flex h-full flex-col rounded-xl border border-border bg-surface p-4 shadow-card dark:border-border-dark dark:bg-surface-dark">
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-brand dark:text-brand-dark">{entityLabel[data.entity] ?? data.entity}</p>
          <h3 className="mt-1 truncate text-sm font-semibold text-content dark:text-content-dark" title={data.label}>
            {data.label}
          </h3>
          {data.sublabel && <p className="mt-0.5 break-all text-[11px] text-content-subtle dark:text-content-dark-subtle">{data.sublabel}</p>}
        </div>
        <button className="icon-button" onClick={onClose} aria-label="关闭详情">
          <X size={16} />
        </button>
      </div>

      <div className="flex flex-wrap items-center gap-1.5">
        <span className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-semibold ${statusBadge[data.status]}`}>
          <span className="h-1.5 w-1.5 rounded-full bg-current" />
          {statusLabel[data.status]}
        </span>
        {data.live && (
          <span className="inline-flex items-center gap-1 rounded-full border border-brand/25 bg-brand/10 px-2 py-0.5 text-[10px] font-semibold text-brand dark:border-brand-dark/25 dark:text-brand-dark">
            <span className="topo-live-dot !relative !right-auto !top-auto !h-1.5 !w-1.5" />
            执行中
          </span>
        )}
      </div>

      <dl className="mt-3 grid gap-2 border-t border-border pt-3 text-xs dark:border-border-dark">
        {connector && (
          <>
            <Row label="类型" value={channelTypeLabel(connector.type)} />
            <Row label="版本" value={connector.version || '未知'} />
            <Row label="集成" value={`${connector.integrations.length} 个`} />
            <Row label="直挂资源组" value={`${connector.groups.length} 个`} />
          </>
        )}
        {integration && (
          <>
            <Row label="协议" value={integration.protocol} />
            <Row label="端点" value={integration.endpoint} />
            <Row label="资源组" value={`${integration.groups.length} 个`} />
          </>
        )}
        {group && (
          <>
            <Row label="资源类型" value={resourceTypeLabel(group.type)} />
            <Row label="资源数量" value={`${group.count} 个`} />
            <Row label="可用" value={`${group.activeCount} 个`} />
            <Row label="健康度" value={group.health} />
          </>
        )}
      </dl>

      {group && group.samples.length > 0 && (
        <div className="mt-3">
          <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-content-subtle dark:text-content-dark-subtle">资源样本</p>
          <ul className="grid gap-1">
            {group.samples.map((item) => (
              <li key={`${item.namespace ?? ''}/${item.name}`} className="truncate font-mono text-[11px] text-content-subtle dark:text-content-dark-subtle">
                {item.namespace ? `${item.namespace}/` : ''}
                {item.name}
              </li>
            ))}
          </ul>
          {group.count > group.samples.length && (
            <p className="mt-1 text-[10px] text-content-subtle dark:text-content-dark-subtle">另有 {group.count - group.samples.length} 个资源，进入资源目录查看</p>
          )}
        </div>
      )}

      {group && group.actions.length > 0 && (
        <div className="mt-3">
          <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-content-subtle dark:text-content-dark-subtle">可用动作</p>
          <div className="flex flex-wrap gap-1">
            {group.actions.map((action) => (
              <code key={action.name} title={riskLabel[action.risk ?? ''] ?? '风险未知'} className="rounded border border-border bg-surface-muted px-1.5 py-0.5 text-[10px] dark:border-border-dark dark:bg-surface-dark-muted">
                {action.name}
              </code>
            ))}
          </div>
        </div>
      )}

      {actionRefs.length > 0 && (
        <div className="mt-3">
          <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-content-subtle dark:text-content-dark-subtle">动作列表</p>
          <div className="flex flex-wrap gap-1">
            {actionRefs.map((name) => (
              <code key={name} className="rounded border border-border bg-surface-muted px-1.5 py-0.5 text-[10px] dark:border-border-dark dark:bg-surface-dark-muted">
                {name}
              </code>
            ))}
          </div>
        </div>
      )}

      {data.detailPath && (
        <button className="primary-button compact mt-auto w-full" onClick={() => onNavigate(data.detailPath!)}>
          {data.entity === 'resource-group' ? '在资源目录中查看' : '查看详情'}
          <ArrowUpRight size={15} />
        </button>
      )}
    </div>
  )
}