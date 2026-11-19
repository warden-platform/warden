import { useCallback, useEffect, useRef, useState } from 'react'
import { useAutoRefresh } from './useAutoRefresh'

// 统一的 useQuery 式数据获取 hook：封装 loading / error / 首次加载 /
// 静默自动刷新（页面可见时才轮询），替代各页面手写的 load + useEffect +
// useAutoRefresh 三件套。
//
// 用法：
//   const { data, error, loading, reload } = useApiQuery(() => listXxx(), [], { intervalMs: 5000 })
//
// options:
//   intervalMs  自动刷新间隔；不传或传 0 表示不自动刷新（传 toggle 值可随时开关）
//   enabled     为 false 时暂停加载与刷新
//   isEqual     静默刷新时数据未变化则跳过 setState，避免整页重渲染
//   onSuccess   每次成功加载后的副作用（如同步全局待办数）
//   debounceMs  deps 变化后延迟加载（搜索框防抖）；首次加载同样延迟
type UseApiQueryOptions<T> = {
  intervalMs?: number
  enabled?: boolean
  isEqual?: (prev: T, next: T) => boolean
  onSuccess?: (data: T) => void
  debounceMs?: number
}

export function useApiQuery<T>(
  fetcher: () => Promise<T>,
  deps: unknown[],
  { intervalMs, enabled = true, isEqual, onSuccess, debounceMs }: UseApiQueryOptions<T> = {},
) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher
  const onSuccessRef = useRef(onSuccess)
  onSuccessRef.current = onSuccess
  // isEqual 通常由调用方内联传入（每次渲染都是新引用），必须走 ref，
  // 否则 reload 的 useCallback 依赖变化会导致 effect 反复触发。
  const isEqualRef = useRef(isEqual)
  isEqualRef.current = isEqual
  // Sequence guard: a slow earlier response must not overwrite a newer one
  // (e.g. rapid filter switches triggering overlapping reloads).
  const requestSeq = useRef(0)

  const reload = useCallback(
    (showSpinner = true) => {
      if (!enabled) return
      const request = ++requestSeq.current
      if (showSpinner) {
        setLoading(true)
        setError('')
      }
      fetcherRef
        .current()
        .then((result) => {
          if (request !== requestSeq.current) return
          setData((prev) => (prev !== null && isEqualRef.current?.(prev, result) ? prev : result))
          setError('')
          onSuccessRef.current?.(result)
        })
        .catch((e: Error) => {
          if (request !== requestSeq.current) return
          if (showSpinner) setError(e.message || '加载失败')
        })
        .finally(() => {
          if (request !== requestSeq.current) return
          if (showSpinner) setLoading(false)
        })
    },
    [enabled],
  )

  useEffect(() => {
    if (!enabled) return
    if (debounceMs && debounceMs > 0) {
      const timer = window.setTimeout(() => reload(true), debounceMs)
      return () => window.clearTimeout(timer)
    }
    reload(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, enabled, reload, debounceMs])

  // 静默刷新：不闪 loading、失败时保留旧数据（与各页原 load(false) 语义一致）。
  useAutoRefresh(() => reload(false), intervalMs ?? 0, Boolean(intervalMs) && enabled)

  return { data, error, loading, reload }
}
