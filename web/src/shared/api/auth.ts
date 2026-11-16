import { request, API_BASE } from './core'
import type { ApiTenant } from './tenants'
import type { ApiPrincipal } from './principals'
import type { ApiProjectUser, ApiProjectMembership } from './projects'

// --- Auth ---

export type LoginResponse = {
  token: string
  expiresAt: string
  user: { id: string; name: string }
}

export type AuthUser = {
  id: string
  /** Stable internal identifier; federated users keep a `federated:<hash>` name. */
  name: string
  /** Human-readable label from the identity provider, when available. */
  display_name?: string
  email?: string
  /** IdP profile picture URL, when the provider supplies one. */
  avatar_url?: string
  /** 'oidc' for federated logins, 'local' for password logins. */
  auth_source?: string
  kind: string
  tenant: Omit<ApiTenant, 'quota' | 'usage'> | null
  role: 'platform_admin' | 'tenant_admin' | string
  scopes: string[]
  /** Server-computed capability list: pam:admin is already expanded. */
  capabilities?: string[]
  is_platform_admin: boolean
  /** True when this is a federated identity authenticated but not yet placed
   *  into any organization (the unaffiliated sentinel tenant). */
  unaffiliated?: boolean
  expires_at?: string | null
}

/**
 * The single capability list used by both the backend guards and the console.
 * Older sessions (before /auth/me exposed capabilities) fall back to scopes.
 */
export const userCapabilities = (user: Pick<AuthUser, 'capabilities' | 'scopes'> | null | undefined): string[] =>
  user?.capabilities?.length ? user.capabilities : (user?.scopes ?? [])

/** True when the identity holds a capability. Never infer from a role name. */
export const canUser = (user: AuthUser | null | undefined, scope: string): boolean =>
  Boolean(user && userCapabilities(user).includes(scope))

/** Prefer the IdP display name in the UI; keep `name` for identity/audit lookups. */
export const userDisplayName = (user: Pick<AuthUser, 'name' | 'display_name' | 'email'> | null | undefined) =>
  user?.display_name?.trim() || user?.email?.trim() || user?.name || ''

/** First character used as the avatar fallback when no IdP picture exists. */
export const userInitial = (user: Pick<AuthUser, 'name' | 'display_name' | 'email'> | null | undefined) =>
  (userDisplayName(user).trim()[0] ?? '?').toUpperCase()

/**
 * Federated principals keep an opaque `federated:<hash>` name for uniqueness.
 * Every list/detail view should call this helper so the hash never reaches the
 * UI unless the IdP provided no profile attributes at all.
 */
export const apiPrincipalDisplayName = (principal: Pick<ApiPrincipal, 'name' | 'metadata'> | null | undefined) => {
  if (!principal) return ''
  const label = principal.metadata?.display_name
  const email = principal.metadata?.email
  return (typeof label === 'string' && label.trim()) || (typeof email === 'string' && email.trim()) || principal.name || ''
}

export const projectUserDisplayName = (user: Pick<ApiProjectUser, 'name' | 'display_name'>) => user.display_name?.trim() || user.name

export const membershipDisplayName = (member: Pick<ApiProjectMembership, 'principal_name' | 'principal_display_name'>) =>
  member.principal_display_name?.trim() || member.principal_name

export const login = (username: string, password: string, tenant?: string) =>
  request<LoginResponse>('POST', '/auth/login', { username, password, ...(tenant ? { tenant } : {}) })

// login_url is the canonical absolute start URL derived from the redirect URI
// registered with the IdP. The state/nonce cookies are host-only, so starting
// the flow from any other origin (e.g. localhost vs 127.0.0.1) loses them.
export type OIDCConfig = { enabled: boolean; login_url?: string; logout_url?: string }
export const getOIDCConfig = () => request<OIDCConfig>('GET', '/auth/oidc/config')
// Top-level navigation URL for the OIDC authorization-code flow. Must be a
// full page load (not fetch) so the state/nonce cookies survive the redirect.
// Used only as a fallback when the server does not return login_url.
export const oidcLoginUrl = `${API_BASE}/auth/oidc/start`

export const getMe = () => request<AuthUser>('GET', '/auth/me')
export const logout = () => request<{ status: string }>('POST', '/auth/logout')
export const changeOwnPassword = (currentPassword: string, newPassword: string) =>
  request<{ status: string; revoked_sessions: number }>('PUT', '/auth/password', { currentPassword, newPassword })

export type AccountRole = 'user' | 'tenant_admin' | 'platform_admin'
/** A login identity as seen by a platform administrator. */
export type ApiAccount = {
  id: string
  name: string
  display_name: string
  email: string
  avatar_url: string
  auth_source: string
  role: AccountRole | string
  status: string
  tenant_id: string
  tenant_name?: string
  last_login_at: string | null
  created_at: string
  is_builtin_admin: boolean
  is_self: boolean
  provider_issuer?: string
  provider_subject?: string
}
export const listAccounts = () => request<ApiAccount[]>('GET', '/accounts')
export const updateAccountRole = (id: string, role: AccountRole) =>
  request<{ id: string; role: string; previous_role: string; revoked_sessions: number; force_relogin: boolean }>(
    'PUT',
    `/accounts/${encodeURIComponent(id)}/role`,
    { role },
  )
export const accountDisplayName = (account: ApiAccount) => account.display_name?.trim() || account.email?.trim() || account.name

/** Remove a member from its organization: SSO identities go back to the
 *  unaffiliated hall, local accounts are disabled. Super-admin only. */
export const removeAccountFromOrganization = (id: string) =>
  request<{ id: string; action: 'removed' | 'disabled'; previous_tenant: string; revoked_sessions: number }>(
    'POST',
    `/accounts/${encodeURIComponent(id)}/remove`,
  )
export const reenableAccount = (id: string) =>
  request<{ id: string; status: string }>('POST', `/accounts/${encodeURIComponent(id)}/reenable`)
