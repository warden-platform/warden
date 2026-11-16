import { request } from './core'
import type { ApiPrincipal } from './principals'

// --- Projects (resource catalog grouping) ---

export type ApiProject = {
  id: string
  name: string
  description: string
  is_default: boolean
  created_at: string
  updated_at: string
}

export type ApiProjectNamespaceRule = {
  id: string
  project_id: string
  connector_id: string
  namespace: string
  created_at: string
}

export const listProjects = () => request<ApiProject[]>('GET', '/projects')
export type ApiMyProject = ApiProject & { my_role: 'PROJECT_ADMIN' | 'OPERATOR' | 'VIEWER' | string }
export const listMyProjects = () => request<ApiMyProject[]>('GET', '/me/projects')
export const createProject = (body: { name: string; description?: string }) => request<ApiProject>('POST', '/projects', body)
export const updateProject = (id: string, body: { name: string; description?: string }) =>
  request<ApiProject>('PUT', `/projects/${encodeURIComponent(id)}`, body)
export const deleteProject = (id: string) => request<void>('DELETE', `/projects/${encodeURIComponent(id)}`)
export type ApiProjectMembership = {
  tenant_id: string
  project_id: string
  project_name: string
  principal_id: string
  principal_name: string
  /** IdP display label for federated users; empty for local identities. */
  principal_display_name?: string
  principal_kind: string
  role: 'PROJECT_ADMIN' | 'OPERATOR' | 'VIEWER'
  status: 'ACTIVE' | 'DISABLED'
  created_by?: string
  created_at: string
  updated_at: string
}
export type ProjectMembershipPolicy = { enforced: boolean }
export type ApiProjectUser = {
  id: string
  name: string
  /** IdP display label for federated users; empty for local identities. */
  display_name?: string
  status: 'ACTIVE' | 'DISABLED'
  tenant_id: string
  last_login_at?: string
  created_at: string
  updated_at: string
  memberships: ApiProjectMembership[]
}
export const listProjectMembers = (projectId: string) =>
  request<ApiProjectMembership[]>('GET', `/projects/${encodeURIComponent(projectId)}/members`)
export const listProjectMemberCandidates = (projectId: string) =>
  request<ApiPrincipal[]>('GET', `/projects/${encodeURIComponent(projectId)}/member-candidates`)
export const addProjectMember = (projectId: string, body: { principalId: string; role: ApiProjectMembership['role'] }) =>
  request<ApiProjectMembership>('POST', `/projects/${encodeURIComponent(projectId)}/members`, body)
export const createProjectUser = (projectId: string, body: { name: string; password: string; role: ApiProjectMembership['role'] }) =>
  request<{ user: ApiPrincipal; membership: ApiProjectMembership }>('POST', `/projects/${encodeURIComponent(projectId)}/users`, body)
export const updateProjectMember = (
  projectId: string,
  principalId: string,
  body: { role: ApiProjectMembership['role']; status: ApiProjectMembership['status'] },
) => request<ApiProjectMembership>('PUT', `/projects/${encodeURIComponent(projectId)}/members/${encodeURIComponent(principalId)}`, body)
export const removeProjectMember = (projectId: string, principalId: string) =>
  request<void>('DELETE', `/projects/${encodeURIComponent(projectId)}/members/${encodeURIComponent(principalId)}`)
export const getProjectMembershipPolicy = () => request<ProjectMembershipPolicy>('GET', '/project-membership-policy')
export const updateProjectMembershipPolicy = (enforced: boolean) =>
  request<ProjectMembershipPolicy>('PUT', '/project-membership-policy', { enforced })
export const listProjectUsers = () => request<ApiProjectUser[]>('GET', '/project-users')
export const getProjectUser = (principalId: string) => request<ApiProjectUser>('GET', `/project-users/${encodeURIComponent(principalId)}`)
export const updateProjectUserStatus = (principalId: string, status: ApiProjectUser['status']) =>
  request<ApiProjectUser>('PUT', `/project-users/${encodeURIComponent(principalId)}/status`, { status })
export const resetProjectUserPassword = (principalId: string, password: string) =>
  request<{ status: string; revoked_sessions: number }>('POST', `/project-users/${encodeURIComponent(principalId)}/reset-password`, {
    password,
  })
export const revokeProjectUserSessions = (principalId: string) =>
  request<{ status: string; revoked_sessions: number }>('POST', `/project-users/${encodeURIComponent(principalId)}/revoke-sessions`)
export const listProjectNamespaceRules = () => request<ApiProjectNamespaceRule[]>('GET', '/project-namespace-rules')
export const upsertProjectNamespaceRule = (body: { project_id: string; connector_id: string; namespace: string }) =>
  request<ApiProjectNamespaceRule>('PUT', '/project-namespace-rules', body)
export const deleteProjectNamespaceRule = (id: string) => request<void>('DELETE', `/project-namespace-rules/${encodeURIComponent(id)}`)
