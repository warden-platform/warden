import type { ExecutionStatus } from '../../shared/types'

export function Badge({ children, tone = 'neutral' }: { children: React.ReactNode; tone?: 'success' | 'warning' | 'danger' | 'info' | 'neutral' }) {
  return <span className={`badge badge-${tone}`}>{children}</span>
}

export function StatusBadge({ status }: { status: ExecutionStatus }) {
  const tone = status === '成功' ? 'success' : status === '拒绝' || status === '失败' ? 'danger' : status === '等待审批' ? 'warning' : 'info'
  return <Badge tone={tone}>{status}</Badge>
}
