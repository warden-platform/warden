import { executionStatusTone } from '../../shared/types'

export function Badge({
  children,
  tone = 'neutral',
}: {
  children: React.ReactNode
  tone?: 'success' | 'warning' | 'danger' | 'info' | 'neutral'
}) {
  return <span className={`badge badge-${tone}`}>{children}</span>
}

export function StatusBadge({ statusKey, label }: { statusKey: string; label: string }) {
  return <Badge tone={executionStatusTone(statusKey)}>{label}</Badge>
}
