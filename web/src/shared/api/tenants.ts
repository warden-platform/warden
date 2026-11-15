import { request } from './core'
import type { ApiPrincipal } from './principals'

export type ApiTenantQuota = {
  tenant_id: string
  max_principals: number
  max_connectors: number
  max_concurrent_tasks: number
  max_requests_per_minute: number
  audit_retention_days: number
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
  maxPrincipals: number
  maxConnectors: number
  maxConcurrentTasks: number
  maxRequestsPerMinute: number
  auditRetentionDays: number
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
export const updateTenantQuota = (slug: string, body: TenantQuotaInput) =>
  request<TenantQuotaInput>('PUT', `/tenants/${encodeURIComponent(slug)}/quota`, body)
export const disableTenant = (slug: string) =>
  request<{ slug: string; status: 'DISABLED' }>('POST', `/tenants/${encodeURIComponent(slug)}/disable`)
