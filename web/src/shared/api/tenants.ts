import { request } from './core'
import type { ApiPrincipal } from './principals'

// Warden does not meter identities or connectors for commercial tiering.
// The only per-tenant limit is the concurrency safety valve that protects the
// shared control plane from runaway execution floods.
export type ApiTenantQuota = {
  tenant_id: string
  max_concurrent_tasks: number
  updated_at: string
}

export type ApiTenantUsage = {
  principals: number
  connectors: number
  projects: number
  in_flight_tasks: number
  administrators: number
}

export type ApiTenant = {
  id: string
  slug: string
  name: string
  status: 'ACTIVE' | 'DISABLED'
  settings: Record<string, unknown>
  quota: ApiTenantQuota
  usage: ApiTenantUsage
  created_at: string
  updated_at: string
}

export type TenantQuotaInput = {
  maxConcurrentTasks: number
}

export type BootstrapTenantInput = {
  slug: string
  name: string
  adminName: string
  adminPassword: string
  quota: TenantQuotaInput
}

export type BootstrapTenantResponse = { tenant: Omit<ApiTenant, 'quota' | 'usage'>; administrator: ApiPrincipal }

export const listTenants = () => request<ApiTenant[]>('GET', '/tenants')
export const getTenant = (slug: string) => request<ApiTenant>('GET', `/tenants/${encodeURIComponent(slug)}`)
export const bootstrapTenant = (body: BootstrapTenantInput) => request<BootstrapTenantResponse>('POST', '/tenants/bootstrap', body)
export const listTenantAdmins = (slug: string) => request<ApiPrincipal[]>('GET', `/tenants/${encodeURIComponent(slug)}/admins`)
export const createTenantAdmin = (slug: string, body: { name: string; password: string }) =>
  request<ApiPrincipal>('POST', `/tenants/${encodeURIComponent(slug)}/admins`, body)

export type TenantMemberProject = { project_id: string; project_name: string; role: string; status: string }
export type TenantMember = {
  id: string
  name: string
  display_name: string
  email: string
  role: string
  status: string
  auth_source: string
  is_builtin_admin: boolean
  last_login_at: string | null
  created_at: string
  memberships: TenantMemberProject[]
}
export const listTenantMembers = (slug: string) => request<TenantMember[]>('GET', `/tenants/${encodeURIComponent(slug)}/members`)
export const createTenantMember = (slug: string, body: { name: string; password: string; role: string }) =>
  request<{ id: string; name: string; role: string; tenant_id: string; login_name: string }>(
    'POST',
    `/tenants/${encodeURIComponent(slug)}/members`,
    body,
  )
export const updateTenantQuota = (slug: string, body: TenantQuotaInput) =>
  request<TenantQuotaInput>('PUT', `/tenants/${encodeURIComponent(slug)}/quota`, body)
export const updateTenantSettings = (slug: string, body: { emailDomains: string[] }) =>
  request<{ slug: string; emailDomains: string[] }>('PUT', `/tenants/${encodeURIComponent(slug)}/settings`, body)
export const disableTenant = (slug: string) =>
  request<{ slug: string; status: 'DISABLED' }>('POST', `/tenants/${encodeURIComponent(slug)}/disable`)

export type UnaffiliatedIdentity = {
  id: string
  name: string
  display_name: string
  email: string
  avatar_url: string
  issuer: string
  subject: string
  created_at: string
  last_login_at: string | null
}
export const listUnaffiliatedIdentities = () => request<UnaffiliatedIdentity[]>('GET', '/unaffiliated-identities')
export const assignUnaffiliatedIdentity = (id: string, body: { tenant: string; role: string }) =>
  request<{ id: string; tenant: string; role: string; revoked_sessions: number; force_relogin: boolean }>(
    'POST',
    `/unaffiliated-identities/${encodeURIComponent(id)}/assign`,
    body,
  )

export type Invitation = {
  id: string
  tenant_id: string
  email: string
  role: string
  status: 'PENDING' | 'ACCEPTED' | 'REVOKED'
  created_at: string
  accepted_at?: string | null
}
export type MyInvitation = Invitation & { tenant_name: string }

// Sub-administrator side (scoped to the caller's own organization).
export const listInvitations = () => request<Invitation[]>('GET', '/invitations')
export const createInvitation = (body: { email: string; role: string }) => request<Invitation>('POST', '/invitations', body)
export const revokeInvitation = (id: string) => request<{ id: string; status: string }>('DELETE', `/invitations/${encodeURIComponent(id)}`)
// Unaffiliated-identity self-service side.
export const myInvitations = () => request<MyInvitation[]>('GET', '/my/invitations')
export const acceptInvitation = (id: string) =>
  request<{ id: string; tenant: string; role: string; force_relogin: boolean }>('POST', `/my/invitations/${encodeURIComponent(id)}/accept`)
