import { NavLink } from '../shared/router'
import {
  Bot,
  Boxes,
  Building2,
  FileClock,
  Fingerprint,
  FolderKanban,
  LayoutDashboard,
  Network,
  ServerCog,
  Share2,
  ShieldCheck,
  TerminalSquare,
  Users,
  X,
  type LucideIcon,
} from 'lucide-react'
import { canUser, getObservedTenant } from '../shared/api'
import { useApp } from './components/Toast'
import { WardenLogo } from './components/WardenLogo'

/**
 * Each menu item declares the capability its page requires. The same scope
 * names guard the corresponding backend endpoints, so a visible menu item is
 * always callable and a hidden one is always rejected by the API.
 *
 * Mission Control IA: groups follow the operator's mental model —
 * situation (what is happening), assets (what we govern), identity
 * (who acts), governance (who decides).
 */
type NavItem = { to: string; label: string; icon: LucideIcon; requires?: string }
type NavGroup = { caption: string; en: string; items: NavItem[] }

const navGroups: NavGroup[] = [
  {
    caption: '态势',
    en: 'Situation',
    items: [
      { to: '/overview', label: '运行总览', icon: LayoutDashboard },
      { to: '/executions', label: '执行记录', icon: TerminalSquare },
      { to: '/my-requests', label: '我的申请', icon: FileClock },
      { to: '/audit', label: '审计追踪', icon: Fingerprint, requires: 'pam:audit:read' },
    ],
  },
  {
    caption: '资产',
    en: 'Assets',
    items: [
      { to: '/topology', label: '拓扑图谱', icon: Share2, requires: 'pam:tenant:admin' },
      { to: '/targets', label: '主机与集群', icon: Boxes, requires: 'pam:tenant:admin' },
      { to: '/integrations', label: '中间件', icon: ServerCog, requires: 'pam:registration:read' },
      { to: '/resources', label: '资源目录', icon: Boxes },
      { to: '/connectors', label: '执行通道', icon: Network, requires: 'pam:tenant:admin' },
    ],
  },
  {
    caption: '身份',
    en: 'Identity',
    items: [
      { to: '/agents', label: 'Agent 身份', icon: Bot, requires: 'pam:tenant:admin' },
      { to: '/projects', label: '项目', icon: FolderKanban },
      { to: '/project-users', label: '项目用户', icon: Users, requires: 'pam:tenant:admin' },
    ],
  },
  {
    caption: '治理',
    en: 'Governance',
    items: [
      { to: '/approvals', label: '访问审批', icon: FileClock, requires: 'pam:approve' },
      { to: '/policies', label: '策略中心', icon: ShieldCheck, requires: 'pam:tenant:admin' },
    ],
  },
]

export function AppSidebar({ mobileOpen, onClose }: { mobileOpen: boolean; onClose: () => void }) {
  const { pendingApprovals, auth } = useApp()
  const can = (scope?: string) => !scope || canUser(auth.user, scope)
  const platformAdmin = canUser(auth.user, 'pam:admin')
  // While the super admin observes a business organization, the full business
  // console becomes available in that organization's context.
  const observing = platformAdmin && Boolean(getObservedTenant())
  return (
    <aside className={`sidebar ${mobileOpen ? 'sidebar-open' : ''}`}>
      <div className="brand">
        <div className="brand-mark">
          <WardenLogo size={22} />
        </div>
        <div>
          <strong>Warden</strong>
          <span>Ops Console</span>
        </div>
        <button className="icon-button sidebar-close" onClick={onClose} aria-label="关闭导航">
          <X size={20} />
        </button>
      </div>
      <nav aria-label="主导航" className="flex-1 overflow-y-auto">
        {platformAdmin && (
          <>
            <p className="nav-caption">
              平台管理<em>Platform</em>
            </p>
            <NavLink to="/platform/tenants" className={({ isActive }) => (isActive ? 'nav-active' : '')} onClick={onClose}>
              <Building2 size={17} />
              <span>租户管理</span>
            </NavLink>
            <NavLink to="/platform/accounts" className={({ isActive }) => (isActive ? 'nav-active' : '')} onClick={onClose}>
              <Users size={17} />
              <span>账号与权限</span>
            </NavLink>
          </>
        )}
        {(!platformAdmin || observing) &&
          navGroups.map((group) => {
          const visibleItems = group.items.filter((item) => can(item.requires))
          if (!visibleItems.length) return null
          return (
            <div key={group.caption}>
              <p className="nav-caption">
                {group.caption}
                <em>{group.en}</em>
              </p>
              {visibleItems.map(({ to, label, icon: Icon }) => (
                <NavLink key={to} to={to} className={({ isActive }) => (isActive ? 'nav-active' : '')} onClick={onClose}>
                  <Icon size={17} />
                  <span>{label}</span>
                  {to === '/approvals' && pendingApprovals > 0 && <b>{pendingApprovals}</b>}
                </NavLink>
              ))}
            </div>
          )
        })}
      </nav>
      <div className="sidebar-foot">
        <span className="pulse-dot" aria-hidden="true" />
        控制面 · 受控执行
      </div>
    </aside>
  )
}