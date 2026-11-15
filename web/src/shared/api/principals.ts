import { request } from './core'

export type ApiPrincipal = {
  id: string
  kind: 'user' | 'agent_type' | 'agent_instance' | 'workload'
  /** Stable internal identifier. Federated users keep a `federated:<hash>` name. */
  name: string
  status: 'ACTIVE' | 'DISABLED'
  tenant_id: string
  metadata: Record<string, unknown>
  last_login_at?: string
  created_at: string
  updated_at: string
}

export type ApiPrincipalKey = {
  id: string
  principal_id: string
  name: string
  key_prefix: string
  status: 'ACTIVE' | 'REVOKED'
  purpose: 'AGENT' | 'REGISTRATION'
  audience?: string
  scopes: string[]
  expires_at?: string
  last_used_at?: string
  last_used_ip?: string
  created_by?: string
  created_at: string
  revoked_at?: string
  revoked_by?: string
  rotation_parent_id?: string
}

export type CreatePrincipalRequest = {
  name: string
  kind?: 'agent_instance' | 'workload'
  metadata?: Record<string, unknown>
}

export type CreateAPIKeyRequest = {
  name?: string
  scopes?: string[]
  expiresAt?: string
}

export type CreateAPIKeyResponse = {
  credential: ApiPrincipalKey
  apiKey: string
  warning: string
  replaces?: string
  apiEndpoint?: string
}

export type CreateRegistrationAPIKeyRequest = {
  name?: string
  audience?: string
  expiresAt?: string
}

export const listPrincipals = (kind = 'agent_instance') =>
  request<ApiPrincipal[]>('GET', `/principals${kind ? `?kind=${encodeURIComponent(kind)}` : ''}`)
export const createPrincipal = (req: CreatePrincipalRequest) => request<ApiPrincipal>('POST', '/principals', req)
export const deletePrincipal = (id: string) =>
  request<{ id: string; status: 'DISABLED' }>('DELETE', `/principals/${id}`)
export const listPrincipalAPIKeys = (principalId: string) =>
  request<ApiPrincipalKey[]>('GET', `/principals/${principalId}/api-keys`)
export const createPrincipalAPIKey = (principalId: string, req: CreateAPIKeyRequest) =>
  request<CreateAPIKeyResponse>('POST', `/principals/${principalId}/api-keys`, req)
export const createRegistrationAPIKey = (principalId: string, req: CreateRegistrationAPIKeyRequest) =>
  request<CreateAPIKeyResponse>('POST', `/principals/${principalId}/registration-api-keys`, req)
export const revokePrincipalAPIKey = (credentialId: string) =>
  request<{ id: string; status: 'REVOKED' }>('POST', `/api-keys/${credentialId}/revoke`)
export const rotatePrincipalAPIKey = (credentialId: string) =>
  request<CreateAPIKeyResponse>('POST', `/api-keys/${credentialId}/rotate`)
export type ApiExternalBinding = {
  id: string
  tenantId?: string
  source: string
  workspaceId: string
  externalProjectId?: string
  identityType: 'AGENT' | 'USER'
  externalId: string
  principalId: string
  projectId?: string
  status: 'ACTIVE' | 'REVOKED'
  createdBy?: string
  createdAt: string
  updatedAt: string
  revokedAt?: string
}
export const listExternalBindings = () => request<ApiExternalBinding[]>('GET', '/external-bindings')
export const createExternalBinding = (body: {
  source: string; workspaceId: string; externalProjectId?: string; identityType: ApiExternalBinding['identityType']; externalId: string; principalId: string; projectId?: string
}) => request<ApiExternalBinding>('POST', '/external-bindings', body)
export const revokeExternalBinding = (id: string) => request<{ id: string; status: 'REVOKED' }>('DELETE', `/external-bindings/${encodeURIComponent(id)}`)
export const revokeExternalWorkspace = (body: { source: string; workspaceId: string }) =>
  request<{ source: string; workspaceId: string; revokedBindings: number }>('POST', '/external-bindings/revoke-workspace', body)
export type ApiFederatedTenantBinding = {
  id: string
  tenantId: string
  source: string
  externalEnterpriseId: string
  externalWorkspaceId: string
  status: 'ACTIVE' | 'REVOKED'
  createdBy?: string
  createdAt: string
  updatedAt: string
  revokedAt?: string
}
export type ApiFederatedProjectBinding = {
  id: string
  tenantId: string
  source: string
  externalWorkspaceId: string
  externalProjectId: string
  externalProjectName?: string
  projectId: string
  status: 'ACTIVE' | 'REVOKED'
  createdBy?: string
  createdAt: string
  updatedAt: string
  revokedAt?: string
}
export const listFederatedTenants = () => request<ApiFederatedTenantBinding[]>('GET', '/federated/tenants')
export const bindFederatedTenant = (body: { source: string; externalEnterpriseId: string; externalWorkspaceId: string }) =>
  request<ApiFederatedTenantBinding>('POST', '/federated/tenants', body)
export const listFederatedProjects = () => request<ApiFederatedProjectBinding[]>('GET', '/federated/projects')
export const bindFederatedProject = (body: { source: string; externalWorkspaceId: string; externalProjectId: string; externalProjectName?: string; projectId: string }) =>
  request<ApiFederatedProjectBinding>('POST', '/federated/projects', body)
export const revokeFederatedProject = (id: string) => request<void>('DELETE', `/federated/projects/${encodeURIComponent(id)}`)
