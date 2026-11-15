import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from '../shared/router'
import { Building2, KeyRound, LogOut, Menu, Moon, Sun } from 'lucide-react'
import { useApp } from './components/Toast'
import { ChangePasswordDialog } from './components/ChangePasswordDialog'
import { canUser, userDisplayName, userInitial } from '../shared/api'

const titleMap: Record<string, { title: string; subtitle: string }> = {
  '/overview': { title: '运行总览', subtitle: 'Agent 特权访问的实时态势' },
  '/projects': { title: '项目', subtitle: '管理租户内资源归属与治理边界' },
  '/project-users': { title: '项目用户', subtitle: '管理租户内项目登录用户、状态和会话' },
  '/platform/tenants': { title: '租户管理', subtitle: '管理共享控制面的租户、管理员与配额' },
  '/topology': { title: '拓扑图谱', subtitle: 'Connector -> Integration -> Resource -> Action 的组织架构可视化与 AI 可读导出' },
  '/targets': { title: '主机与集群', subtitle: '管理接入的 Kubernetes 集群、Linux 主机、H3C 网络设备及其原生资源' },
  '/integrations': { title: '中间件', subtitle: '管理已接入的协议集成（Jenkins 等）及其执行通道绑定' },
  '/integrations/new': { title: '接入中间件', subtitle: '填写连接信息并选择执行通道' },
  '/connectors': { title: '执行通道', subtitle: '诊断目标侧执行代理的连接、版本、能力和权限健康度' },
  '/resources': { title: '资源目录', subtitle: '统一管理目标资源、Owner 和允许动作' },
  '/agents': { title: 'Agent 身份', subtitle: '为每个 Multica Agent 管理独立 Principal、API Key 和访问范围' },
  '/external-identities': { title: '外部身份', subtitle: '维护外部联邦身份 Binding 与 PAM 委托边界' },
  '/executions': { title: '执行记录', subtitle: '跟踪所有受控动作的真实执行状态' },
  '/approvals': { title: '访问审批', subtitle: '审查与具体任务、资源和参数绑定的授权申请' },
  '/audit': { title: '审计追踪', subtitle: '还原身份、决策、授权、凭据和目标执行责任链' },
  '/policies': { title: '策略中心', subtitle: '管理动作范围、参数约束和审批要求' },
}

function resolveTitle(pathname: string): { title: string; subtitle: string } {
  if (titleMap[pathname]) return titleMap[pathname]
  if (pathname.startsWith('/integrations/')) return { title: '中间件详情', subtitle: '管理 Integration 配置、Job 范围和关联资源' }
  if (pathname.startsWith('/targets/')) return { title: '主机与集群详情', subtitle: '查看目标资源、项目环境和关联执行代理' }
  if (pathname.startsWith('/connectors/')) return { title: '执行通道详情', subtitle: '诊断关联服务目标、代理能力、权限范围和连接状态' }
  return { title: 'HighTower', subtitle: '' }
}

function useDarkMode() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))
  const toggle = () => {
    setDark((prev) => {
      const next = !prev
      document.documentElement.classList.toggle('dark', next)
      try {
        localStorage.setItem('pam-theme', next ? 'dark' : 'light')
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
  const { title, subtitle } = resolveTitle(location.pathname)
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
          <p>{subtitle}</p>
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
