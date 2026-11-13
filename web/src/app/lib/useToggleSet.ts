import { useCallback, useState } from 'react'

// useToggleSet manages a Set<string> of toggled keys. Used for collapse state
// (keys present = collapsed) and type filters (keys present = hidden).
// Pass an initializer to pre-populate (e.g. collapse all but the first item);
// omit it for an empty set (everything expanded/visible by default).
export function useToggleSet(initial?: () => Set<string>) {
  const [set, setSet] = useState<Set<string>>(() => initial?.() ?? new Set())
  const toggle = useCallback((key: string) => {
    setSet((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key); else next.add(key)
      return next
    })
  }, [])
  return [set, toggle] as const
}
