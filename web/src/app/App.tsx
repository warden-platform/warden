import { useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from '../shared/router'
import { AppSidebar } from './AppSidebar'
import { AppTopbar } from './AppTopbar'
import { AppProvider, useApp } from './components/Toast'
import { LoadingState } from './components/States'
import { LoginPage } from './pages/LoginPage'
import { OverviewPage } from './pages/OverviewPage'
import { TopologyPage } from './pages/TopologyPage'
import { IntegrationsPage } from './pages/IntegrationsPage'
import { IntegrationDetailPage } from './pages/IntegrationDetailPage'
import { IntegrationCreatePage } from './pages/IntegrationCreatePage'
import { ConnectorsPage } from './pages/ConnectorsPage'
import { ConnectorDetailPage } from './pages/ConnectorDetailPage'
import { TargetsPage } from './pages/TargetsPage'
import { ResourcesPage } from './pages/ResourcesPage'
import { ExecutionsPage } from './pages/ExecutionsPage'
import { ApprovalsPage } from './pages/ApprovalsPage'
import { AuditPage } from './pages/AuditPage'
import { PoliciesPage } from './pages/PoliciesPage'
import { AgentsPage } from './pages/AgentsPage'
import { DingTalkApprovalsPage } from './pages/DingTalkApprovalsPage'
import { ProjectsPage } from './pages/ProjectsPage'
import { TenantsPage } from './pages/TenantsPage'
import { ProjectUsersPage } from './pages/ProjectUsersPage'
import { ExternalIdentitiesPage } from './pages/ExternalIdentitiesPage'

function PlatformOnly({ children }: { children: React.ReactElement }) {
  const { auth } = useApp()
  return auth.user?.is_platform_admin ? children : <Navigate to="/overview" replace />
}

function TenantAdminOnly({ children }: { children: React.ReactElement }) {
  const { auth } = useApp()
  return auth.user?.is_platform_admin || auth.user?.role === 'tenant_admin' ? children : <Navigate to="/overview" replace />
}

function AppShell() {
  const [mobileNav, setMobileNav] = useState(false)
  return (
    <div className="app-shell">
      <AppSidebar mobileOpen={mobileNav} onClose={() => setMobileNav(false)} />
      {mobileNav && <button className="nav-scrim" aria-label="关闭导航" onClick={() => setMobileNav(false)} />}
      <main>
        <AppTopbar onMenu={() => setMobileNav(true)} />
        <div className="page-content">
          <Routes>
            <Route path="/" element={<Navigate to="/overview" replace />} />
            <Route path="/overview" element={<OverviewPage />} />
            <Route path="/projects" element={<ProjectsPage />} />
            <Route path="/project-users" element={<TenantAdminOnly><ProjectUsersPage /></TenantAdminOnly>} />
            <Route path="/platform/tenants" element={<PlatformOnly><TenantsPage /></PlatformOnly>} />
            <Route path="/topology" element={<TopologyPage />} />
            <Route path="/integrations" element={<IntegrationsPage />} />
            <Route path="/integrations/new" element={<IntegrationCreatePage />} />
            <Route path="/integrations/:id" element={<IntegrationDetailPage />} />
            <Route path="/targets" element={<TargetsPage />} />
            <Route path="/targets/:id" element={<ConnectorDetailPage mode="target" />} />
            <Route path="/connectors" element={<ConnectorsPage />} />
            <Route path="/connectors/:id" element={<ConnectorDetailPage />} />
            <Route path="/resources" element={<ResourcesPage />} />
            <Route path="/executions" element={<ExecutionsPage />} />
            <Route path="/approvals" element={<ApprovalsPage />} />
            <Route path="/audit" element={<AuditPage />} />
            <Route path="/policies" element={<PoliciesPage />} />
            <Route path="/agents" element={<AgentsPage />} />
            <Route path="/external-identities" element={<TenantAdminOnly><ExternalIdentitiesPage /></TenantAdminOnly>} />
            <Route path="*" element={<Navigate to="/overview" replace />} />
          </Routes>
        </div>
      </main>
    </div>
  )
}

function AppRoutes() {
  const { auth } = useApp()
  if (auth.loading) return <LoadingState label="正在验证登录状态" />
  return (
    <Routes>
      <Route path="/login" element={auth.user ? <Navigate to={auth.user.is_platform_admin ? '/platform/tenants' : '/overview'} replace /> : <LoginPage />} />
      <Route path="*" element={auth.user ? <AppShell /> : <Navigate to="/login" replace />} />
    </Routes>
  )
}

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/dingtalk/approvals" element={<DingTalkApprovalsPage />} />
        <Route path="*" element={<AppProvider><AppRoutes /></AppProvider>} />
      </Routes>
    </BrowserRouter>
  )
}

export default App
