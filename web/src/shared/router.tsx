import {
  Children,
  createContext,
  isValidElement,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type AnchorHTMLAttributes,
  type ReactElement,
  type ReactNode,
} from 'react'

type Location = { pathname: string; search: string }
type NavigateOptions = { replace?: boolean }
type RouterValue = Location & { navigate: (to: string, options?: NavigateOptions) => void }

const currentLocation = (): Location => ({ pathname: window.location.pathname, search: window.location.search })
const RouterContext = createContext<RouterValue | null>(null)
const ParamsContext = createContext<Record<string, string>>({})

export function BrowserRouter({ children }: { children: ReactNode }) {
  const [location, setLocation] = useState(currentLocation)
  useEffect(() => {
    const onPopState = () => setLocation(currentLocation())
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])
  const navigate = useCallback((to: string, options: NavigateOptions = {}) => {
    const target = new URL(to, window.location.href)
    if (target.origin !== window.location.origin) throw new Error('cross-origin navigation is not allowed')
    const next = target.pathname + target.search + target.hash
    if (options.replace) window.history.replaceState(null, '', next)
    else window.history.pushState(null, '', next)
    setLocation(currentLocation())
  }, [])
  const value = useMemo(() => ({ ...location, navigate }), [location, navigate])
  return <RouterContext.Provider value={value}>{children}</RouterContext.Provider>
}

function useRouter() {
  const router = useContext(RouterContext)
  if (!router) throw new Error('router hook used outside BrowserRouter')
  return router
}

export function useNavigate() {
  return useRouter().navigate
}

export function useLocation(): Location {
  const { pathname, search } = useRouter()
  return { pathname, search }
}

export function useParams<T extends Record<string, string | undefined> = Record<string, string>>() {
  return useContext(ParamsContext) as T
}

export function useSearchParams(): [URLSearchParams, (next: URLSearchParams | Record<string, string>, options?: NavigateOptions) => void] {
  const { pathname, search, navigate } = useRouter()
  const params = useMemo(() => new URLSearchParams(search), [search])
  const setParams = useCallback(
    (next: URLSearchParams | Record<string, string>, options?: NavigateOptions) => {
      const value = next instanceof URLSearchParams ? next : new URLSearchParams(next)
      const query = value.toString()
      navigate(pathname + (query ? `?${query}` : ''), options)
    },
    [navigate, pathname],
  )
  return [params, setParams]
}

type RouteProps = { path: string; element: ReactElement }

export function Route(_: RouteProps) {
  return null
}

export function matchPath(pattern: string, pathname: string): Record<string, string> | null {
  if (pattern === '*') return {}
  const patternParts = pattern.split('/').filter(Boolean)
  const pathParts = pathname.split('/').filter(Boolean)
  if (patternParts.length !== pathParts.length) return null
  const params: Record<string, string> = {}
  for (let i = 0; i < patternParts.length; i += 1) {
    const expected = patternParts[i]
    const actual = pathParts[i]
    if (expected.startsWith(':')) {
      try {
        params[expected.slice(1)] = decodeURIComponent(actual)
      } catch {
        return null
      }
    } else if (expected !== actual) return null
  }
  return params
}

export function Routes({ children }: { children: ReactNode }) {
  const { pathname } = useRouter()
  for (const child of Children.toArray(children)) {
    if (!isValidElement<RouteProps>(child)) continue
    const params = matchPath(child.props.path, pathname)
    if (params) return <ParamsContext.Provider value={params}>{child.props.element}</ParamsContext.Provider>
  }
  return null
}

export function Navigate({ to, replace = false }: { to: string; replace?: boolean }) {
  const navigate = useNavigate()
  useEffect(() => navigate(to, { replace }), [navigate, replace, to])
  return null
}

type NavLinkProps = Omit<AnchorHTMLAttributes<HTMLAnchorElement>, 'className' | 'href'> & {
  to: string
  className?: string | ((state: { isActive: boolean }) => string)
}

export function NavLink({ to, className, onClick, ...props }: NavLinkProps) {
  const { pathname, navigate } = useRouter()
  const active = pathname === to || (to !== '/' && pathname.startsWith(to + '/'))
  const resolvedClass = typeof className === 'function' ? className({ isActive: active }) : className
  return (
    <a
      {...props}
      href={to}
      className={resolvedClass}
      onClick={(event) => {
        onClick?.(event)
        if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
        event.preventDefault()
        navigate(to)
      }}
    />
  )
}
