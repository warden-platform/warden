import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from '../shared/router'
import { Building2, KeyRound, LogOut, Menu, Moon, Sun } from 'lucide-react'
import { useApp } from './components/Toast'
import { ChangePasswordDialog } from './components/ChangePasswordDialog'
import { canUser, getObservedTenant, listTenants, setObservedTenant, userDisplayName, userInitial, type ApiTenant } from '../shared/api'

const titleMap: Record<string, string> = {
  '/overview': '运行总览',
  '/projects': '项目',
  '/project-users': '项目用户',
  '/platform/tenants': '租户管理',
  '/platform/accounts': '账号与权限',
  '/topology': '拓扑图谱',
  '/targets': '主机与集群',
  '/integrations': '中间件',
  '/integrations/new': '接入中间件',
  '/connectors': '执行通道',
  '/resources': '资源目录',
  '/agents': 'Agent 身份',
  '/executions': '执行记录',
  '/approvals': '访问审批',
  '/my-requests': '我的申请',
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

function useClock() {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(t)
  }, [])
  return now.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}

export function AppTopbar({ onMenu }: { onMenu: () => void }) {
  const location = useLocation()
  const navigate = useNavigate()
  const { auth, signOut, notify } = useApp()
  const title = resolveTitle(location.pathname)
  const { dark, toggle } = useDarkMode()
  const clock = useClock()
  const [passwordOpen, setPasswordOpen] = useState(false)
  const [failedAvatarUrl, setFailedAvatarUrl] = useState<string | null>(null)
  const avatarFailed = failedAvatarUrl !== null && failedAvatarUrl === auth.user?.avatar_url
  const isSuperAdmin = canUser(auth.user, 'warden:admin')
  const [orgs, setOrgs] = useState<ApiTenant[]>([])
  const observed = getObservedTenant()
  useEffect(() => {
    if (!isSuperAdmin) return
    listTenants()
      .then((items) => setOrgs(items ?? []))
      .catch(() => { setOrgs([]); notify('组织列表加载失败', 'error') })
  }, [isSuperAdmin, notify])
  const switchOrg = (slug: string) => {
    setObservedTenant(slug)
    // Full navigation remounts every page so no stale tenant data survives.
    window.location.assign(slug ? '/overview' : '/platform/tenants')
  }
  return (
    <header className="topbar">
      <div className="title-row">
        <button className="icon-button mobile-menu" onClick={onMenu} aria-label="打开导航">
          <Menu size={20} />
        </button>
        <div>
          <h1>{title}</h1>
        </div>
        <span className="topbar-live" title="控制面连接正常，数据自动刷新">
          <i aria-hidden="true" />
          LIVE
        </span>
      </div>
      <div className="top-actions">
        <span className="topbar-clock" aria-label="当前时间">
          {clock}
        </span>
        {isSuperAdmin ? (
          <span className="tenant-context org-switcher" title="超级管理员可切入任意业务组织的完整控制台">
            <Building2 size={15} />
            <select
              className="org-switcher-select"
              value={observed}
              onChange={(e) => switchOrg(e.target.value)}
              aria-label="切换组织视角"
            >
              <option value="">平台管理域</option>
              {orgs.map((t) => (
                <option key={t.slug} value={t.slug}>
                  {t.name}（{t.slug}）
                </option>
              ))}
            </select>
          </span>
        ) : (
          auth.user?.tenant && (
            <span className="tenant-context">
              <Building2 size={15} />
              <span>{auth.user.tenant.slug === 'platform' ? '平台管理域' : auth.user.tenant.name}</span>
              <code>{auth.user.tenant.slug}</code>
            </span>
          )
        )}
        {auth.user && (
          <span
            className={`role-badge ${
              canUser(auth.user, 'warden:admin') ? 'role-super' : canUser(auth.user, 'warden:tenant:admin') ? 'role-admin' : 'role-member'
            }`}
            title={
              canUser(auth.user, 'warden:admin')
                ? '唯一超级管理员：跨租户管理组织与账号'
                : canUser(auth.user, 'warden:tenant:admin')
                  ? '组织管理员：管理本组织的资产、成员与审批'
                  : '成员：在本组织内发起并跟踪自己的执行'
            }
          >
            {canUser(auth.user, 'warden:admin') ? '超级管理员' : canUser(auth.user, 'warden:tenant:admin') ? '组织管理员' : '成员'}
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
                onError={() => setFailedAvatarUrl(auth.user?.avatar_url ?? null)}
              />
            ) : (
              <span className="user-avatar user-avatar-fallback" aria-hidden="true">
                {userInitial(auth.user)}
              </span>
            )}
            <span className="user-name">
              {userDisplayName(auth.user)}
              {auth.user.email ? <small>{auth.user.email}</small> : null}
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
