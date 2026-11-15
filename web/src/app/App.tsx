import { lazy, Suspense, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from '../shared/router'
import { AppSidebar } from './AppSidebar'
import { AppTopbar } from './AppTopbar'
import { AppProvider, useApp } from './components/Toast'
import { LoadingState } from './components/States'
import { LoginPage } from './pages/LoginPage'
import { canUser } from '../shared/api'

// 路由级代码分割：除登录页外全部懒加载，首屏只保留 shell + 登录逻辑
const OverviewPage = lazy(() => import('./pages/OverviewPage').then((m) => ({ default: m.OverviewPage })))
const TopologyPage = lazy(() => import('./pages/TopologyPage').then((m) => ({ default: m.TopologyPage })))
const IntegrationsPage = lazy(() => import('./pages/IntegrationsPage').then((m) => ({ default: m.IntegrationsPage })))
const IntegrationDetailPage = lazy(() => import('./pages/IntegrationDetailPage').then((m) => ({ default: m.IntegrationDetailPage })))
const IntegrationCreatePage = lazy(() => import('./pages/IntegrationCreatePage').then((m) => ({ default: m.IntegrationCreatePage })))
const ConnectorsPage = lazy(() => import('./pages/ConnectorsPage').then((m) => ({ default: m.ConnectorsPage })))
const ConnectorDetailPage = lazy(() => import('./pages/ConnectorDetailPage').then((m) => ({ default: m.ConnectorDetailPage })))
const TargetsPage = lazy(() => import('./pages/TargetsPage').then((m) => ({ default: m.TargetsPage })))
const ResourcesPage = lazy(() => import('./pages/ResourcesPage').then((m) => ({ default: m.ResourcesPage })))
const ExecutionsPage = lazy(() => import('./pages/ExecutionsPage').then((m) => ({ default: m.ExecutionsPage })))
const ApprovalsPage = lazy(() => import('./pages/ApprovalsPage').then((m) => ({ default: m.ApprovalsPage })))
const AuditPage = lazy(() => import('./pages/AuditPage').then((m) => ({ default: m.AuditPage })))
const PoliciesPage = lazy(() => import('./pages/PoliciesPage').then((m) => ({ default: m.PoliciesPage })))
const AgentsPage = lazy(() => import('./pages/AgentsPage').then((m) => ({ default: m.AgentsPage })))
const DingTalkApprovalsPage = lazy(() => import('./pages/DingTalkApprovalsPage').then((m) => ({ default: m.DingTalkApprovalsPage })))
const ProjectsPage = lazy(() => import('./pages/ProjectsPage').then((m) => ({ default: m.ProjectsPage })))
const TenantsPage = lazy(() => import('./pages/TenantsPage').then((m) => ({ default: m.TenantsPage })))
const ProjectUsersPage = lazy(() => import('./pages/ProjectUsersPage').then((m) => ({ default: m.ProjectUsersPage })))
const ExternalIdentitiesPage = lazy(() => import('./pages/ExternalIdentitiesPage').then((m) => ({ default: m.ExternalIdentitiesPage })))
const AccountsPage = lazy(() => import('./pages/AccountsPage').then((m) => ({ default: m.AccountsPage })))

function PlatformOnly({ children }: { children: React.ReactElement }) {
  const { auth } = useApp()
  return canUser(auth.user, 'pam:admin') ? children : <Navigate to="/overview" replace />
}

function TenantAdminOnly({ children }: { children: React.ReactElement }) {
  const { auth } = useApp()
  return canUser(auth.user, 'pam:tenant:admin') ? children : <Navigate to="/overview" replace />
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
          <Suspense fallback={<LoadingState label="正在加载页面" />}>
            <Routes>
              <Route path="/" element={<Navigate to="/overview" replace />} />
              <Route path="/overview" element={<OverviewPage />} />
              <Route path="/projects" element={<ProjectsPage />} />
              <Route
                path="/project-users"
                element={
                  <TenantAdminOnly>
                    <ProjectUsersPage />
                  </TenantAdminOnly>
                }
              />
              <Route
                path="/platform/tenants"
                element={
                  <PlatformOnly>
                    <TenantsPage />
                  </PlatformOnly>
                }
              />
              <Route
                path="/platform/accounts"
                element={
                  <PlatformOnly>
                    <AccountsPage />
                  </PlatformOnly>
                }
              />
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
              <Route
                path="/external-identities"
                element={
                  <TenantAdminOnly>
                    <ExternalIdentitiesPage />
                  </TenantAdminOnly>
                }
              />
              <Route path="*" element={<Navigate to="/overview" replace />} />
            </Routes>
          </Suspense>
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
      <Route
        path="/login"
        element={auth.user ? <Navigate to={canUser(auth.user, 'pam:admin') ? '/platform/tenants' : '/overview'} replace /> : <LoginPage />}
      />
      <Route path="*" element={auth.user ? <AppShell /> : <Navigate to="/login" replace />} />
    </Routes>
  )
}

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route
          path="/dingtalk/approvals"
          element={
            <Suspense fallback={<LoadingState label="正在加载页面" />}>
              <DingTalkApprovalsPage />
            </Suspense>
          }
        />
        <Route
          path="*"
          element={
            <AppProvider>
              <AppRoutes />
            </AppProvider>
          }
        />
      </Routes>
    </BrowserRouter>
  )
}

export default App
