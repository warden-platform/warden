import type { LucideIcon } from 'lucide-react'

export function Kpi({
  label,
  value,
  detail,
  tone,
  icon: Icon,
}: {
  label: string
  value: string | number
  detail: string
  tone: string
  icon: LucideIcon
}) {
  return (
    <div className="kpi">
      <div className={`kpi-icon ${tone}`}>
        <Icon size={19} />
      </div>
      <div className="kpi-copy">
        <span>{label}</span>
        <strong>{value}</strong>
        <small>{detail}</small>
      </div>
    </div>
  )
}
