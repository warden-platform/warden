import { useState } from 'react'
import { useNavigate } from '../../shared/router'
import { Loader2, Lock } from 'lucide-react'
import { ApiError, getMe, login } from '../../shared/api'
import { useApp } from '../components/Toast'
import { WardenLogo } from '../components/WardenLogo'

// Platform operations entry. Deliberately separate from the tenant login page
// (/login): tenant users never see platform vocabulary, and this URL is not
// linked from anywhere in the tenant UI — operators reach it from the docs.
// Security rests on credentials + login rate limiting, not on URL obscurity.
export function PlatformLoginPage() {
  const navigate = useNavigate()
  const { signIn } = useApp()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    if (loading) return
    setLoading(true)
    setError('')
    login(username.trim(), password, 'platform')
      .then(() => getMe())
      .then((user) => {
        signIn(user)
        navigate('/platform/tenants')
      })
      .catch((err: ApiError) => {
        setError(err.code === 'INVALID_CREDENTIALS' ? '用户名或密码错误' : err.message || '登录失败')
      })
      .finally(() => setLoading(false))
  }

  return (
    <div className="login-pane" style={{ minHeight: '100vh' }}>
      <form className="login-card" onSubmit={submit}>
        <div className="login-brand">
          <WardenLogo size={32} />
          <div>
            <strong>WARDEN</strong>
            <span>Platform Operations</span>
          </div>
        </div>
        <p className="login-eyebrow">平台运维入口</p>
        <h1>控制面管理登录</h1>
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
          {loading ? '正在登录' : '登录'}
        </button>
        <p className="login-hint" style={{ marginTop: 16, textAlign: 'center' }}>
          租户用户请从组织登录页进入
        </p>
      </form>
    </div>
  )
}