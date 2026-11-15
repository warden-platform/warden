import { ArrowUpRight, X } from 'lucide-react'
import type { TopoNodeData, TopoStatus } from '../../lib/topology/buildTopology'

const statusLabel: Record<TopoStatus, string> = {
  active: '正常',
  warning: '注意',
  error: '异常',
  neutral: '中性',
}

const entityLabel: Record<string, string> = {
  connector: '执行通道',
  integration: '中间件集成',
  'resource-group': '资源分组',
  action: '可用动作',
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
      <div className="flex h-full flex-col items-center justify-center rounded-lg border border-dashed border-border p-6 text-center dark:border-border-dark">
        <p className="text-xs text-content-subtle dark:text-content-dark-subtle">点击节点查看详情</p>
        <p className="mt-1 text-[11px] leading-relaxed text-content-subtle dark:text-content-dark-subtle">双击节点可跳转到对应详情页</p>
      </div>
    )
  }
  return (
    <div className="flex h-full flex-col rounded-lg border border-white/60 bg-white/75 p-4 shadow-glass backdrop-blur-xl dark:border-white/10 dark:bg-surface-dark/75 dark:shadow-glass-dark">
      <div className="mb-3 flex items-start justify-between">
        <div>
          <p className="text-[10px] font-semibold uppercase tracking-wider text-brand dark:text-brand-dark">
            {entityLabel[data.entity] ?? data.entity}
          </p>
          <h3 className="mt-1 text-sm font-semibold text-content dark:text-content-dark">{data.label}</h3>
          {data.sublabel && (
            <p className="mt-0.5 break-all text-[11px] text-content-subtle dark:text-content-dark-subtle">{data.sublabel}</p>
          )}
        </div>
        <button className="icon-button" onClick={onClose} aria-label="关闭详情">
          <X size={16} />
        </button>
      </div>

      <dl className="grid gap-2 text-xs">
        <div className="flex justify-between gap-2">
          <dt className="text-content-subtle dark:text-content-dark-subtle">类型</dt>
          <dd className="font-mono text-content dark:text-content-dark">{data.kind}</dd>
        </div>
        <div className="flex justify-between gap-2">
          <dt className="text-content-subtle dark:text-content-dark-subtle">状态</dt>
          <dd>{statusLabel[data.status]}</dd>
        </div>
        {typeof data.count === 'number' && (
          <div className="flex justify-between gap-2">
            <dt className="text-content-subtle dark:text-content-dark-subtle">资源数量</dt>
            <dd className="font-semibold">{data.count}</dd>
          </div>
        )}
      </dl>

      {data.actions && data.actions.length > 0 && (
        <div className="mt-3">
          <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-content-subtle dark:text-content-dark-subtle">
            动作列表
          </p>
          <div className="flex flex-wrap gap-1">
            {data.actions.map((a) => (
              <code
                key={a}
                className="rounded border border-border bg-surface-muted px-1.5 py-0.5 text-[10px] dark:border-border-dark dark:bg-surface-dark-muted"
              >
                {a}
              </code>
            ))}
          </div>
        </div>
      )}

      {data.entity === 'resource-group' &&
        Array.isArray((data.raw as { sample?: { name: string; namespace?: string }[] } | null)?.sample) && (
          <div className="mt-3">
            <p className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-content-subtle dark:text-content-dark-subtle">
              资源样本
            </p>
            <ul className="grid gap-1">
              {(data.raw as { sample: { name: string; namespace?: string }[] }).sample.map((s, i) => (
                <li key={i} className="truncate font-mono text-[11px] text-content-subtle dark:text-content-dark-subtle">
                  {s.namespace ? `${s.namespace}/` : ''}
                  {s.name}
                </li>
              ))}
            </ul>
          </div>
        )}

      {data.detailPath && (
        <button className="primary-button compact mt-4 w-full" onClick={() => onNavigate(data.detailPath!)}>
          {data.entity === 'resource-group' ? '在资源目录中查看' : '查看详情'}
          <ArrowUpRight size={15} />
        </button>
      )}
    </div>
  )
}
