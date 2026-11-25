// API client for warden-control. All Web UI pages should call these functions
// instead of reading mock data, so the UI stays in sync with the backend.

export const API_BASE = import.meta.env.VITE_API_BASE_URL ?? '/api/v1'

// Super-admin organization switcher: while observing a business organization,
// every request carries X-Warden-Tenant so the backend scopes the business
// console to it. Persisted across reloads; the backend ignores the header for
// non-super-admins, so it is safe to keep in localStorage.
const OBSERVE_KEY = 'warden-observe-tenant'
export function getObservedTenant(): string {
  try {
    return localStorage.getItem(OBSERVE_KEY) || ''
  } catch {
    return ''
  }
}
export function setObservedTenant(slug: string) {
  try {
    if (slug) localStorage.setItem(OBSERVE_KEY, slug)
    else localStorage.removeItem(OBSERVE_KEY)
  } catch {
    /* ignore */
  }
}

// Admin authentication is held in an HttpOnly cookie; these compatibility
// helpers intentionally keep JWT material out of browser storage.
export function getToken() {
  return ''
}
export function setToken(_token: string) {}
export function clearToken() {}

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  const observed = getObservedTenant()
  if (observed) headers['X-Warden-Tenant'] = observed
  let payload: BodyInit | undefined
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  const resp = await fetch(`${API_BASE}${path}`, { method, headers, body: payload, credentials: 'include' })
  // Parse the server error before applying the generic session-expired
  // handling. Login deliberately returns 401/INVALID_CREDENTIALS and callers
  // need that distinction to show the correct message.
  const text = resp.status === 204 ? '' : await resp.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = text
    }
  }
  const errObj = typeof data === 'object' && data && 'error' in (data as object) ? (data as { error?: string; message?: string }) : null
  // 超级管理员切换组织时残留的 observe tenant 可能已被停用/删除。
  // 这种状态下保留 header 会让登录后的 /auth/me 也失败，表现为“目标组织已停用”。
  // 清掉残留并重试一次，让用户回到正常登录流程。
  if (observed && (errObj?.error === 'TENANT_DISABLED' || errObj?.error === 'TENANT_NOT_FOUND')) {
    setObservedTenant('')
    return request<T>(method, path, body)
  }
  if (resp.status === 401) {
    if (errObj?.error === 'INVALID_CREDENTIALS') {
      throw new ApiError(401, errObj.error, errObj.message ?? '用户名或密码错误')
    }
    // Token missing/expired: clear and bounce to login. Hard redirect avoids
    // React Router loops when the caller is already inside a guarded route.
    // Both public login entries must be exempt, or an unauthenticated visitor
    // on /platform/login gets bounced to the tenant login page.
    clearToken()
    if (window.location.pathname !== '/login' && window.location.pathname !== '/platform/login') {
      window.location.href = '/login'
    }
    throw new ApiError(401, 'UNAUTHORIZED', '登录已过期，请重新登录')
  }
  if (resp.status === 204) return undefined as T
  if (!resp.ok) {
    throw new ApiError(resp.status, errObj?.error ?? 'HTTP_ERROR', errObj?.message ?? `请求失败 (${resp.status})`)
  }
  return data as T
}

export async function coolAppRequest<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  let payload: BodyInit | undefined
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  const resp = await fetch(`${API_BASE}${path}`, { method, headers, body: payload, credentials: 'include' })
  const text = resp.status === 204 ? '' : await resp.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = text
    }
  }
  if (resp.status === 204) return undefined as T
  if (!resp.ok) {
    const errObj = typeof data === 'object' && data ? (data as { error?: string; message?: string; user_id?: string }) : null
    const error = new ApiError(resp.status, errObj?.error ?? 'HTTP_ERROR', errObj?.message ?? `请求失败 (${resp.status})`) as ApiError & {
      userId?: string
    }
    error.userId = errObj?.user_id
    throw error
  }
  return data as T
}
