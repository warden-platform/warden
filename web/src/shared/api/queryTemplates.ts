import { request } from './core'

export type ApiQueryTemplate = {
  id: string
  integration_id: string
  schema_resource_id: string
  name: string
  version: number
  status: 'DRAFT' | 'PUBLISHED' | 'DEPRECATED' | 'DISABLED'
  parameter_schema?: Record<string, unknown>
  resource_bindings?: string[]
  max_rows: number
  timeout_seconds: number
  sensitivity: string
  owner?: string
  published_at?: string
  template_hash: string
  created_at: string
  updated_at: string
}

export type ApiTemplatePublishRequest = {
  id: string
  template_id: string
  requested_by: string
  status: 'PENDING' | 'APPROVED' | 'REJECTED'
  approved_by?: string
  rejected_reason?: string
  requested_at: string
  decided_at?: string
}

export const listQueryTemplates = (integrationId: string, status = 'PUBLISHED') => {
  const params = new URLSearchParams({ integrationId, status })
  return request<ApiQueryTemplate[]>('GET', `/database/query-templates?${params.toString()}`)
}
export const importQueryTemplate = (body: { integrationId: string; schemaResourceId: string; name: string; sql: string; owner?: string }) =>
  request<ApiQueryTemplate>('POST', '/database/query-templates', body)
export const submitQueryTemplatePublish = (templateId: string) =>
  request<ApiTemplatePublishRequest>('POST', `/database/query-templates/${encodeURIComponent(templateId)}/publish`)
export const listTemplatePublishRequests = (status = '') =>
  request<ApiTemplatePublishRequest[]>('GET', `/database/query-templates/publish-requests${status ? `?status=${encodeURIComponent(status)}` : ''}`)
export const approveTemplatePublish = (templateId: string, requestId: string) =>
  request<{ id: string; status: string }>('POST', `/database/query-templates/${encodeURIComponent(templateId)}/publish-requests/${encodeURIComponent(requestId)}/approve`)
export const rejectTemplatePublish = (templateId: string, requestId: string, reason: string) =>
  request<{ id: string; status: string }>('POST', `/database/query-templates/${encodeURIComponent(templateId)}/publish-requests/${encodeURIComponent(requestId)}/reject`, { reason })
