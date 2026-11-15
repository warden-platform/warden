import { NavLink } from '../shared/router'
import {
  Bot,
  Boxes,
  Building2,
  FileClock,
  Fingerprint,
  FolderKanban,
  LayoutDashboard,
  Link2,
  Network,
  ServerCog,
  Share2,
  Shield,
  ShieldCheck,
  TerminalSquare,
  Users,
  X,
  type LucideIcon,
} from 'lucide-react'
import { canUser } from '../shared/api'
import { useApp } from './components/Toast'

/**
 * Each menu item declares the capability its page requires. The same scope
 * names guard the corresponding backend endpoints, so a visible menu item is
 * always callable and a hidden one is always rejected by the API.
 */
type NavItem = { to: string; label: string; icon: LucideIcon; requires?: string }

const navItems: NavItem[] = [
  { to: '/overview', label: '运行总览', icon: LayoutDashboard },
  { to: '/projects', label: '项目', icon: FolderKanban },
  { to: '/project-users', label: '项目用户', icon: Users, requires: 'pam:tenant:admin' },
  { to: '/topology', label: '拓扑图谱', icon: Share2, requires: 'pam:tenant:admin' },
  { to: '/targets', label: '主机与集群', icon: Boxes, requires: 'pam:tenant:admin' },
  { to: '/integrations', label: '中间件', icon: ServerCog, requires: 'pam:registration:read' },
  { to: '/resources', label: '资源目录', icon: Boxes },
  { to: '/connectors', label: '执行通道', icon: Network, requires: 'pam:tenant:admin' },
  { to: '/agents', label: 'Agent 身份', icon: Bot, requires: 'pam:tenant:admin' },
  { to: '/external-identities', label: '外部身份', icon: Link2, requires: 'pam:tenant:admin' },
  { to: '/executions', label: '执行记录', icon: TerminalSquare },
  { to: '/approvals', label: '访问审批', icon: FileClock, requires: 'pam:approve' },
  { to: '/audit', label: '审计追踪', icon: Fingerprint, requires: 'pam:audit:read' },
  { to: '/policies', label: '策略中心', icon: ShieldCheck, requires: 'pam:tenant:admin' },
]

export function AppSidebar({ mobileOpen, onClose }: { mobileOpen: boolean; onClose: () => void }) {
  const { pendingApprovals, auth } = useApp()
  const can = (scope?: string) => !scope || canUser(auth.user, scope)
  const visibleItems = navItems.filter((item) => can(item.requires))
  const platformAdmin = canUser(auth.user, 'pam:admin')
  return (
    <aside className={`sidebar ${mobileOpen ? 'sidebar-open' : ''}`}>
      <div className="brand">
        <div className="brand-mark">
          <Shield size={21} strokeWidth={2.2} />
        </div>
        <div>
          <strong>Warden</strong>
          <span>Agent Warden</span>
        </div>
        <button className="icon-button sidebar-close" onClick={onClose} aria-label="关闭导航">
          <X size={20} />
        </button>
      </div>
      <nav aria-label="主导航">
        {platformAdmin && (
          <>
            <p className="nav-caption">平台管理</p>
            <NavLink to="/platform/tenants" className={({ isActive }) => (isActive ? 'nav-active' : '')} onClick={onClose}>
              <Building2 size={18} />
              <span>租户管理</span>
            </NavLink>
            <NavLink to="/platform/accounts" className={({ isActive }) => (isActive ? 'nav-active' : '')} onClick={onClose}>
              <Users size={18} />
              <span>账号与权限</span>
            </NavLink>
          </>
        )}
        <p className="nav-caption">工作台</p>
        {visibleItems.map(({ to, label, icon: Icon }) => (
          <NavLink key={to} to={to} className={({ isActive }) => (isActive ? 'nav-active' : '')} onClick={onClose}>
            <Icon size={18} />
            <span>{label}</span>
            {to === '/approvals' && pendingApprovals > 0 && <b>{pendingApprovals}</b>}
          </NavLink>
        ))}
      </nav>
    </aside>
  )
}
