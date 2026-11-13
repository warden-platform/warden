import { NavLink } from '../shared/router'
import {
  Bot, Boxes, Building2, FileClock, Fingerprint, FolderKanban, LayoutDashboard, Link2, Network,
  ServerCog, Share2, Shield, ShieldCheck, TerminalSquare, Users, X,
} from 'lucide-react'
import { useApp } from './components/Toast'

const navItems = [
  { to: '/overview', label: '运行总览', icon: LayoutDashboard },
  { to: '/projects', label: '项目', icon: FolderKanban },
  { to: '/project-users', label: '项目用户', icon: Users },
  { to: '/topology', label: '拓扑图谱', icon: Share2 },
  { to: '/targets', label: '主机与集群', icon: Boxes },
  { to: '/integrations', label: '中间件', icon: ServerCog },
  { to: '/resources', label: '资源目录', icon: Boxes },
  { to: '/connectors', label: '执行通道', icon: Network },
  { to: '/agents', label: 'Agent 身份', icon: Bot },
  { to: '/external-identities', label: '外部身份', icon: Link2 },
  { to: '/executions', label: '执行记录', icon: TerminalSquare },
  { to: '/approvals', label: '访问审批', icon: FileClock },
  { to: '/audit', label: '审计追踪', icon: Fingerprint },
  { to: '/policies', label: '策略中心', icon: ShieldCheck },
] as const

export function AppSidebar({ mobileOpen, onClose }: { mobileOpen: boolean; onClose: () => void }) {
  const { pendingApprovals, auth } = useApp()
  const tenantAdmin = Boolean(auth.user?.is_platform_admin || auth.user?.role === 'tenant_admin')
  const visibleItems = tenantAdmin ? navItems : navItems.filter(({ to }) => ['/overview', '/projects', '/resources', '/executions'].includes(to))
  return (
    <aside className={`sidebar ${mobileOpen ? 'sidebar-open' : ''}`}>
      <div className="brand">
        <div className="brand-mark"><Shield size={21} strokeWidth={2.2} /></div>
        <div><strong>HighTower</strong><span>Agent PAM</span></div>
        <button className="icon-button sidebar-close" onClick={onClose} aria-label="关闭导航"><X size={20} /></button>
      </div>
      <nav aria-label="主导航">
        {auth.user?.is_platform_admin && <>
          <p className="nav-caption">平台管理</p>
          <NavLink to="/platform/tenants" className={({ isActive }) => isActive ? 'nav-active' : ''} onClick={onClose}>
            <Building2 size={18} /><span>租户管理</span>
          </NavLink>
        </>}
        <p className="nav-caption">工作台</p>
        {visibleItems.map(({ to, label, icon: Icon }) => (
          <NavLink key={to} to={to} className={({ isActive }) => isActive ? 'nav-active' : ''} onClick={onClose}>
            <Icon size={18} /><span>{label}</span>
            {to === '/approvals' && pendingApprovals > 0 && <b>{pendingApprovals}</b>}
          </NavLink>
        ))}
      </nav>
    </aside>
  )
}
