// Shared type-filter chip bar used by all resource domain components.
// Each chip shows icon + label + count; clicking toggles visibility of that
// resource type. Chips with zero count are hidden; if only one chip remains
// the bar is not rendered (no point filtering a single type).
import type { ReactNode } from 'react'

export interface TypeChip {
  type: string
  label: string
  icon: ReactNode
  count: number
}

export function TypeFilterBar({
  chips,
  hiddenTypes,
  onToggle,
}: {
  chips: TypeChip[]
  hiddenTypes: Set<string>
  onToggle: (type: string) => void
}) {
  const visible = chips.filter((c) => c.count > 0)
  if (visible.length <= 1) return null
  return (
    <div className="type-filter-bar">
      {visible.map((chip) => {
        const active = !hiddenTypes.has(chip.type)
        return (
          <button
            key={chip.type}
            type="button"
            className={`type-chip${active ? '' : ' inactive'}`}
            onClick={() => onToggle(chip.type)}
            aria-pressed={active}
          >
            {chip.icon}
            <span>{chip.label}</span>
            <small>{chip.count}</small>
          </button>
        )
      })}
    </div>
  )
}
