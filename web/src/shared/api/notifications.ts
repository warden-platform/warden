import { request, coolAppRequest } from './core'

export type ApiNotificationChannel = {
  id?: string
  type: 'dingtalk'
  name: string
  enabled: boolean
  events: string[]
  configured: boolean
  webhook_configured: boolean
  coolapp_configured: boolean
  card_configured: boolean
  stream_configured: boolean
  credential_source: 'secret_file'
  created_at?: string
  updated_at?: string
}

export const getDingTalkChannel = () => request<ApiNotificationChannel>('GET', '/notification-channels/dingtalk')
export const updateDingTalkChannel = (body: { name: string; enabled: boolean; events: string[] }) =>
  request<ApiNotificationChannel>('PUT', '/notification-channels/dingtalk', body)
export const testDingTalkChannel = () => request<{ status: 'SUCCEEDED' }>('POST', '/notification-channels/dingtalk/test')

export type DingTalkCoolAppUser = { id: string; name: string }
export const getDingTalkCoolAppConfig = () => coolAppRequest<{ client_id: string }>('GET', '/dingtalk/config')
export type DingTalkCoolAppApproval = {
  id: string
  status: string
  reason?: string
  parameters: Record<string, unknown>
  expires_at?: string
  created_at: string
  applicant?: string
  resource_name?: string
  resource_type?: string
  environment?: string
  action_name?: string
  risk_level?: string
}

export const authenticateDingTalkCoolApp = (body: { auth_code: string; corp_id: string; open_conversation_id: string }) =>
  coolAppRequest<{ user: DingTalkCoolAppUser; expires_at: string }>('POST', '/dingtalk/auth', body)
export const listDingTalkCoolAppApprovals = () => coolAppRequest<DingTalkCoolAppApproval[]>('GET', '/dingtalk/access-requests')
export const decideDingTalkCoolAppApproval = (id: string, approve: boolean) =>
  coolAppRequest<{ id: string; status: string }>(
    'POST',
    `/dingtalk/access-requests/${encodeURIComponent(id)}/${approve ? 'approve' : 'reject'}`,
  )
export const logoutDingTalkCoolApp = () => coolAppRequest<void>('POST', '/dingtalk/logout')
