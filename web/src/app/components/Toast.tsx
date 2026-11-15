// Shared app context: toast notifications + pending approvals count + auth.
// Lifted out of App so any routed page can trigger a toast, report the
// current pending-approval count, or read the logged-in user without prop drilling.
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { CheckCircle2 } from 'lucide-react'
import { getMe, getOIDCConfig, logout, type AuthUser } from '../../shared/api'

type ToastEntry = { id: number; message: string }

type AuthState = { user: AuthUser | null; loading: boolean }

type AppContextValue = {
  notify: (message: string) => void
  pendingApprovals: number
  setPendingApprovals: (n: number) => void
  auth: AuthState
  signIn: (user: AuthUser) => void
  signOut: () => void
}

const AppContext = createContext<AppContextValue | null>(null)

export function AppProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastEntry[]>([])
  const [pendingApprovals, setPendingApprovals] = useState(0)
  const [auth, setAuth] = useState<AuthState>({ user: null, loading: true })
  const [oidcLogoutUrl, setOIDCLogoutUrl] = useState('')
  const seq = useRef(0)

  const notify = useCallback((message: string) => {
    const id = ++seq.current
    setToasts((current) => [...current, { id, message }])
    window.setTimeout(() => {
      setToasts((current) => current.filter((item) => item.id !== id))
    }, 3200)
  }, [])

  // On mount, validate the HttpOnly session cookie through /auth/me and
  // fetch the canonical RP-initiated logout URL (only present when SSO is on).
  useEffect(() => {
    getMe()
      .then((user: AuthUser) => setAuth({ user, loading: false }))
      .catch(() => {
        setAuth({ user: null, loading: false })
      })
    getOIDCConfig()
      .then((config) => setOIDCLogoutUrl(config.logout_url ?? ''))
      .catch(() => undefined)
  }, [])

  const signIn = useCallback((user: AuthUser) => {
    setAuth({ user, loading: false })
  }, [])

  const isFederated = auth.user?.auth_source === 'oidc'
  const signOut = useCallback(() => {
    // A federated session must also be terminated at the IdP; otherwise the
    // next SSO click silently signs the user straight back in.
    if (isFederated && oidcLogoutUrl) {
      window.location.assign(oidcLogoutUrl)
      return
    }
    void logout().catch(() => undefined)
    setAuth({ user: null, loading: false })
  }, [isFederated, oidcLogoutUrl])

  const value = useMemo(
    () => ({ notify, pendingApprovals, setPendingApprovals, auth, signIn, signOut }),
    [notify, pendingApprovals, auth, signIn, signOut],
  )

  return (
    <AppContext.Provider value={value}>
      {children}
      {toasts.length > 0 && (
        <div className="toast-stack">
          {toasts.map((t) => (
            <div className="toast" key={t.id}>
              <CheckCircle2 size={18} />
              {t.message}
            </div>
          ))}
        </div>
      )}
    </AppContext.Provider>
  )
}

export function useApp(): AppContextValue {
  const ctx = useContext(AppContext)
  if (!ctx) throw new Error('useApp must be used within AppProvider')
  return ctx
}
