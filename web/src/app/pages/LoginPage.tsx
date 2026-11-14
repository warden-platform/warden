import { useEffect, useState } from 'react'
import { useNavigate } from '../../shared/router'
import { Loader2, Lock, LogIn, ShieldCheck } from 'lucide-react'
import { ApiError, canUser, getMe, getOIDCConfig, login, oidcLoginUrl } from '../../shared/api'
import { useApp } from '../components/Toast'

// Login page: centered card, reuses the Keep-style design-system classes
// (.panel / .input / .primary-button). On success calls signIn() and
// navigates to /overview. The route guard in App.tsx bounces authenticated
// users away from /login automatically.
export function LoginPage() {
  const navigate = useNavigate()
  const { signIn } = useApp()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [tenant, setTenant] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [oidcEnabled, setOIDCEnabled] = useState(false)
  const [oidcHref, setOidcHref] = useState(oidcLoginUrl)

  useEffect(() => {
    getOIDCConfig()
      .then((c) => {
        setOIDCEnabled(c.enabled)
        // Always start the flow from the origin registered with the IdP so the
        // host-only state/nonce cookies come back on the callback.
        if (c.login_url) setOidcHref(c.login_url)
      })
      .catch(() => setOIDCEnabled(false))
  }, [])

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    if (loading) return
    setLoading(true)
    setError('')
    login(username.trim(), password, tenant.trim().toLowerCase())
      .then(() => getMe())
      .then((user) => {
        signIn(user)
        navigate(canUser(user, 'pam:admin') ? '/platform/tenants' : '/overview')
      })
      .catch((err: ApiError) => {
        setError(err.code === 'INVALID_CREDENTIALS' ? '用户名或密码错误' : err.message || '登录失败')
      })
      .finally(() => setLoading(false))
  }

  return (
    <div className="login-shell">
      <form className="login-card" onSubmit={submit}>
        <div className="login-brand">
          <ShieldCheck size={32} />
          <div>
            <h1>HighTower</h1>
            <p>使用管理员账户登录</p>
          </div>
        </div>
        <label className="login-field">
          <span>租户标识</span>
          <input
            className="input"
            type="text"
            autoComplete="organization"
            value={tenant}
            onChange={(e) => setTenant(e.target.value.toLowerCase())}
            disabled={loading}
            placeholder="例如 acme；平台管理员可留空"
            pattern="[a-z0-9][a-z0-9-]*"
          />
        </label>
        <label className="login-field">
          <span>用户名</span>
          <input
            className="input"
            type="text"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoFocus
            disabled={loading}
            placeholder="admin"
          />
        </label>
        <label className="login-field">
          <span>密码</span>
          <input
            className="input"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={loading}
          />
        </label>
        {error && <div className="login-error">{error}</div>}
        <button className="primary-button login-submit" type="submit" disabled={loading || !username || !password}>
          {loading ? <Loader2 size={18} className="spin" /> : <Lock size={18} />}
          {loading ? '登录中' : '登录'}
        </button>
        {oidcEnabled && (
          <>
            <div className="my-4 flex items-center gap-3 text-xs text-content-subtle dark:text-content-dark-subtle">
              <span className="h-px flex-1 bg-border dark:bg-border-dark" />
              或
              <span className="h-px flex-1 bg-border dark:bg-border-dark" />
            </div>
            <a className="secondary-button login-submit" href={oidcHref}>
              <LogIn size={18} />
              使用企业账号登录（SSO）
            </a>
          </>
        )}
      </form>
    </div>
  )
}
