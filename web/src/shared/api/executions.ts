import { request } from './core'

export type ExecutionStatus = 'WAITING_APPROVAL' | 'QUEUED' | 'DISPATCHED' | 'RUNNING' | 'SUCCEEDED' | 'FAILED' | 'UNKNOWN' | 'CANCELLING' | 'CANCELLED' | 'EXPIRED'

export type ApiExecution = {
  id: string
  status: ExecutionStatus
  tenantId?: string
  actingProjectId?: string
  principalId?: string
  principalName?: string
  principalKind?: string
  credentialId?: string
  credentialName?: string
  actionId: string
  actionName?: string
  actionDisplayName?: string
  riskLevel?: 'low' | 'medium' | 'high' | 'critical'
  resourceId: string
  resourceName?: string
  resourceType?: string
  traceId: string
  errorCode?: string
  errorMessage?: string
  result?: Record<string, unknown>
  createdAt: string
  updatedAt: string
}

export type ApiAccessRequest = {
  id: string
  execution_id: string
  principal_id: string
  principal_name?: string
  resource_id: string
  resource_name?: string
  resource_type?: string
  resource_status?: string
  action_id: string
  acting_project_id?: string
  action_name?: string
  action_display_name?: string
  risk_level?: 'low' | 'medium' | 'high' | 'critical'
  approval_context?: Record<string, unknown>
  parameters: Record<string, unknown>
  parameter_hash: string
  reason?: string
  status: 'PENDING' | 'APPROVED' | 'REJECTED' | 'EXPIRED' | 'CANCELLED'
  expires_at?: string
  created_at: string
  updated_at: string
}

export type ApiAuditEvent = {
  id: string
  action: string
  actor_id?: string
  actor_type?: string
  acting_project_id?: string
  credential_id?: string
  resource_id?: string
  execution_id?: string
  connector_id?: string
  trace_id?: string
  target_id?: string
  summary?: string
  metadata: Record<string, unknown>
  created_at: string
}

export type CreateExecutionRequest = {
  taskId?: string
  resourceId: string
  actingProjectId?: string
  action: string
  actionVersion?: number
  parameters: Record<string, unknown>
  reason?: string
}

export const listExecutions = (status?: string) =>
  request<ApiExecution[]>('GET', `/executions${status ? `?status=${encodeURIComponent(status)}` : ''}`)
export const listAccessRequests = (status?: string) =>
  request<ApiAccessRequest[]>('GET', `/access-requests${status ? `?status=${encodeURIComponent(status)}` : ''}`)
export const listMyAccessRequests = (status?: string) =>
  request<ApiAccessRequest[]>('GET', `/my/access-requests${status ? `?status=${encodeURIComponent(status)}` : ''}`)
export type AuditEventFilters = {
  action?: string
  category?: string
  executionId?: string
  traceId?: string
  connectorId?: string
  q?: string
  externalSource?: string
  workspaceId?: string
  externalProjectId?: string
  agentId?: string
  taskId?: string
  issueId?: string
  originatorUserId?: string
  delegationTokenId?: string
  onBehalfOfPrincipalId?: string
  accountablePrincipalId?: string
}
export const listAuditEvents = (filters: AuditEventFilters = {}, limit = 200, offset = 0) => {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  for (const [key, value] of Object.entries(filters)) if (value) params.set(key, value)
  return request<ApiAuditEvent[]>('GET', `/audit-events?${params.toString()}`)
}
export const approveRequest = (id: string) => request<{ id: string; status: string }>('POST', `/access-requests/${id}/approve`)
export const rejectRequest = (id: string) => request<{ id: string; status: string }>('POST', `/access-requests/${id}/reject`)
export const createExecution = (req: CreateExecutionRequest) => request<ApiExecution>('POST', '/executions', req)
export const getExecution = (id: string) => request<ApiExecution>('GET', `/executions/${id}`)
