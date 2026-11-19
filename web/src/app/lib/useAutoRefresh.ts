import { useEffect, useRef } from 'react'

// Shared auto-refresh hook: runs `fn` every `intervalMs` while the tab is
// visible, and once immediately when the tab regains visibility/focus.
// Pass enabled=false to pause (e.g. user toggle).
export function useAutoRefresh(fn: () => void, intervalMs: number, enabled = true) {
  const fnRef = useRef(fn)
  // Latest-ref update in an effect (not during render): interval/visibility
  // callbacks only fire after commit, so they always see the fresh closure.
  useEffect(() => {
    fnRef.current = fn
  })

  useEffect(() => {
    if (!enabled) return
    const tick = () => {
      if (document.visibilityState === 'visible') fnRef.current()
    }
    const timer = window.setInterval(tick, intervalMs)
    const onVisible = () => {
      if (document.visibilityState === 'visible') fnRef.current()
    }
    document.addEventListener('visibilitychange', onVisible)
    window.addEventListener('focus', onVisible)
    return () => {
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
      window.removeEventListener('focus', onVisible)
    }
  }, [intervalMs, enabled])
}
