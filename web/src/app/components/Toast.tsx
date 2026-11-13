// Shared app context: toast notifications + pending approvals count + auth.
// Lifted out of App so any routed page can trigger a toast, report the
// current pending-approval count, or read the logged-in user without prop drilling.
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { CheckCircle2 } from 'lucide-react'
import { getMe, logout, type AuthUser } from '../../shared/api'

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
  const seq = useRef(0)

  const notify = useCallback((message: string) => {
    const id = ++seq.current
    setToasts((current) => [...current, { id, message }])
    window.setTimeout(() => {
      setToasts((current) => current.filter((item) => item.id !== id))
    }, 3200)
  }, [])

  // On mount, validate the HttpOnly session cookie through /auth/me.
  useEffect(() => {
    getMe()
      .then((user: AuthUser) => setAuth({ user, loading: false }))
      .catch(() => {
        setAuth({ user: null, loading: false })
      })
  }, [])

  const signIn = useCallback((user: AuthUser) => {
    setAuth({ user, loading: false })
  }, [])

  const signOut = useCallback(() => {
    void logout().catch(() => undefined)
    setAuth({ user: null, loading: false })
  }, [])

  const value = useMemo(() => ({ notify, pendingApprovals, setPendingApprovals, auth, signIn, signOut }), [notify, pendingApprovals, auth, signIn, signOut])

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
