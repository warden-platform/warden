import { useEffect, useState } from 'react'
import { useNavigate } from '../../shared/router'
import { Loader2, Lock, LogIn } from 'lucide-react'
import { ApiError, canUser, getMe, getOIDCConfig, login, oidcLoginUrl } from '../../shared/api'
import { useApp } from '../components/Toast'
import { WardenLogo } from '../components/WardenLogo'

// Tenant login: a single RAM-style qualified login name (user@org-slug).
// This page intentionally contains no platform/admin vocabulary — platform
// operators use the separate /platform/login entry. Auth logic is unchanged;
// the backend parses the qualified name into user + tenant.
export function LoginPage() {
  const navigate = useNavigate()
  const { signIn } = useApp()
  const [loginName, setLoginName] = useState('')
  const [password, setPassword] = useState('')
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
    const name = loginName.trim()
    const at = name.lastIndexOf('@')
    if (at <= 0 || at === name.length - 1) {
      setError('请使用 用户名@组织标识 格式，例如 zhangsan@acme')
      return
    }
    setLoading(true)
    setError('')
    login(name, password)
      .then(() => getMe())
      .then((user) => {
        signIn(user)
        navigate(canUser(user, 'warden:admin') ? '/platform/tenants' : '/overview')
      })
      .catch((err: ApiError) => {
        setError(err.code === 'INVALID_CREDENTIALS' ? '用户名或密码错误' : err.message || '登录失败')
      })
      .finally(() => setLoading(false))
  }

  return (
    <div className="login-split">
      <section className="login-hero" aria-hidden="true">
        <div className="login-hero-brand">
          <div className="brand-mark">
            <WardenLogo size={26} />
          </div>
          <div>
            <strong>WARDEN</strong>
            <span>Action-level Authorization</span>
          </div>
        </div>
        <h2>
          不交凭据，
          <br />
          只交付一次<em>受约束的执行</em>。
        </h2>
        <p>每一次动作都经过策略决策、审批与签名信封，全程可审计、可追溯。Agent 拿到的不是长期密钥，而是一个 execution_id。</p>
        <div className="login-chain">
          <div className="login-chain-node">
            <b>Agent</b>
            <span>请求动作</span>
          </div>
          <div className="login-chain-edge">
            <i />
          </div>
          <div className="login-chain-node gate">
            <b>Policy Gate</b>
            <span>策略关卡</span>
          </div>
          <div className="login-chain-edge">
            <i style={{ animationDelay: '1.4s' }} />
          </div>
          <div className="login-chain-node">
            <b>Connector</b>
            <span>受控执行</span>
          </div>
        </div>
        <p className="login-hero-foot">DEFAULT DENY · SIGNED ENVELOPE · FULL AUDIT TRAIL</p>
      </section>

      <div className="login-pane">
        <form className="login-card" onSubmit={submit}>
          <p className="login-eyebrow">Controlled Session</p>
          <h1>建立受控会话</h1>
          <label className="login-field">
            <span>登录名</span>
            <input
              className="input"
              type="text"
              autoComplete="username"
              value={loginName}
              onChange={(e) => setLoginName(e.target.value)}
              autoFocus
              disabled={loading}
              placeholder="zhangsan@acme"
            />
            <small className="login-hint">格式：用户名@组织标识，组织标识由你的管理员提供</small>
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
          <button className="primary-button login-submit" type="submit" disabled={loading || !loginName || !password}>
            {loading ? <Loader2 size={18} className="spin" /> : <Lock size={18} />}
            {loading ? '正在建立会话' : '登录'}
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
                使用 SSO 登录
              </a>
            </>
          )}
        </form>
      </div>
    </div>
  )
}