import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from '../shared/router'
import { Building2, KeyRound, LogOut, Menu, Moon, Sun } from 'lucide-react'
import { useApp } from './components/Toast'
import { ChangePasswordDialog } from './components/ChangePasswordDialog'
import { canUser, userDisplayName, userInitial } from '../shared/api'

const titleMap: Record<string, string> = {
  '/overview': '运行总览',
  '/projects': '项目',
  '/project-users': '项目用户',
  '/platform/tenants': '租户管理',
  '/topology': '拓扑图谱',
  '/targets': '主机与集群',
  '/integrations': '中间件',
  '/integrations/new': '接入中间件',
  '/connectors': '执行通道',
  '/resources': '资源目录',
  '/agents': 'Agent 身份',
  '/executions': '执行记录',
  '/approvals': '访问审批',
  '/audit': '审计追踪',
  '/policies': '策略中心',
}

function resolveTitle(pathname: string): string {
  if (titleMap[pathname]) return titleMap[pathname]
  if (pathname.startsWith('/integrations/')) return '中间件详情'
  if (pathname.startsWith('/targets/')) return '主机与集群详情'
  if (pathname.startsWith('/connectors/')) return '执行通道详情'
  return 'Warden'
}

function useDarkMode() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))
  const toggle = () => {
    setDark((prev) => {
      const next = !prev
      document.documentElement.classList.toggle('dark', next)
      try {
        localStorage.setItem('warden-theme', next ? 'dark' : 'light')
      } catch {
        /* ignore */
      }
      return next
    })
  }
  return { dark, toggle }
}

export function AppTopbar({ onMenu }: { onMenu: () => void }) {
  const location = useLocation()
  const navigate = useNavigate()
  const { auth, signOut } = useApp()
  const title = resolveTitle(location.pathname)
  const { dark, toggle } = useDarkMode()
  const [passwordOpen, setPasswordOpen] = useState(false)
  const [avatarFailed, setAvatarFailed] = useState(false)
  useEffect(() => setAvatarFailed(false), [auth.user?.avatar_url])
  return (
    <header className="topbar">
      <div className="title-row">
        <button className="icon-button mobile-menu" onClick={onMenu} aria-label="打开导航">
          <Menu size={20} />
        </button>
        <div>
          <h1>{title}</h1>
        </div>
      </div>
      <div className="top-actions">
        {auth.user?.tenant && (
          <span className="tenant-context">
            <Building2 size={15} />
            <span>{auth.user.tenant.name}</span>
            <code>{auth.user.tenant.slug}</code>
          </span>
        )}
        {auth.user && (
          <span className="user-identity" title={auth.user.email || auth.user.name}>
            {auth.user.avatar_url && !avatarFailed ? (
              <img
                className="user-avatar"
                src={auth.user.avatar_url}
                alt=""
                referrerPolicy="no-referrer"
                onError={() => setAvatarFailed(true)}
              />
            ) : (
              <span className="user-avatar user-avatar-fallback" aria-hidden="true">
                {userInitial(auth.user)}
              </span>
            )}
            <span className="user-name">
              {userDisplayName(auth.user)}
              <small>
                {canUser(auth.user, 'pam:admin') ? '平台管理员' : canUser(auth.user, 'pam:tenant:admin') ? '租户管理员' : '项目成员'}
                {auth.user.email ? ` · ${auth.user.email}` : ''}
              </small>
            </span>
          </span>
        )}
        <button
          className="icon-button"
          onClick={toggle}
          aria-label={dark ? '切换到浅色模式' : '切换到深色模式'}
          title={dark ? '切换到浅色模式' : '切换到深色模式'}
        >
          {dark ? <Sun size={18} /> : <Moon size={18} />}
        </button>
        {auth.user && (
          <button className="icon-button" onClick={() => setPasswordOpen(true)} aria-label="修改密码" title="修改密码">
            <KeyRound size={18} />
          </button>
        )}
        {auth.user && (
          <button
            className="quiet-button"
            onClick={() => {
              signOut()
              if (auth.user?.auth_source !== 'oidc') navigate('/login')
            }}
            title="登出"
          >
            <LogOut size={16} />
            登出
          </button>
        )}
      </div>
      {passwordOpen && (
        <ChangePasswordDialog
          onClose={() => setPasswordOpen(false)}
          onChanged={() => {
            setPasswordOpen(false)
            signOut()
            navigate('/login')
          }}
        />
      )}
    </header>
  )
}
