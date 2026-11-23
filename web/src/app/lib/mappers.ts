// View-model mappers: convert API response shapes into the UI display shapes.
import type { ApiAccessRequest, ApiAuditEvent, ApiExecution, ApiResource } from '../../shared/api'
import type { Approval, AuditItem, Execution, Resource } from '../../shared/types'
import { mapActionName, mapEnvironment, mapExecutionStatus, mapResourceStatus, mapRiskLevel } from '../../shared/types'
import { resourceTypeLabel } from './labels'

export function apiResourceToView(r: ApiResource, connectorName: string): Resource {
  return {
    id: r.id,
    name: r.name,
    type: resourceTypeLabel(r.resource_type),
    environment: mapEnvironment(r.environment),
    owner: r.owner_team || '未指派',
    connector: connectorName || r.connector_id.slice(0, 8),
    status: mapResourceStatus(r.status),
    actions: [],
  }
}

export function apiExecutionToView(e: ApiExecution, resourceLookup?: (id: string) => ApiResource | undefined): Execution {
  const res = e.resourceName ? undefined : resourceLookup?.(e.resourceId)
  const hasFinished = ['SUCCEEDED', 'FAILED', 'CANCELLED', 'EXPIRED', 'UNKNOWN'].includes(e.status)
  const dur =
    hasFinished && e.updatedAt && e.createdAt
      ? Math.max(0, (new Date(e.updatedAt).getTime() - new Date(e.createdAt).getTime()) / 1000).toFixed(1) + 's'
      : '-'
  const principalLabel = e.principalName
    ? `${e.principalName}${e.credentialName ? ` · key ${e.credentialName}` : ''}`
    : e.principalId
      ? `${e.principalId.slice(0, 8)}${e.credentialId ? ` · key ${e.credentialId.slice(0, 8)}` : ''}`
      : 'legacy'
  return {
    id: e.id,
    taskId: e.traceId,
    tenant: e.tenantId,
    resource: e.resourceName ?? res?.name ?? e.resourceId.slice(0, 8),
    action: e.actionDisplayName || mapActionName(e.actionName || e.actionId),
    actor: principalLabel,
    status: mapExecutionStatus(e.status),
    statusKey: e.status,
    createdAt: new Date(e.createdAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }),
    duration: dur,
    risk: e.riskLevel ? mapRiskLevel(e.riskLevel) : '中',
    parameters: e.errorMessage || e.errorCode || '',
    result: e.result,
    errorMessage: e.errorMessage,
  }
}

export function apiRequestToApproval(r: ApiAccessRequest, resourceLookup?: (id: string) => ApiResource | undefined): Approval {
  const res = r.resource_name ? undefined : resourceLookup?.(r.resource_id)
  return {
    id: r.id,
    executionId: r.execution_id,
    requestor: r.principal_name || r.principal_id.slice(0, 8),
    resource: r.resource_name ?? `${res?.name ?? r.resource_id.slice(0, 8)} · ${res ? mapEnvironment(res.environment) : ''}`,
    action: r.action_display_name || mapActionName(r.action_name || r.action_id),
    reason: r.reason || '未提供业务原因',
    risk: r.risk_level ? mapRiskLevel(r.risk_level) : '高',
    expires: r.expires_at ? `${Math.max(0, Math.round((new Date(r.expires_at).getTime() - Date.now()) / 60000))} 分钟后` : '不限制',
    parameters: JSON.stringify(r.parameters),
  }
}

export function apiAuditToView(e: ApiAuditEvent): AuditItem {
  const categoryMap: Record<string, string> = {
    execution: '执行',
    access_request: '审批',
    grant: '授权',
    connector: '连接器',
    policy: '策略',
    permission_drift: '权限',
  }
  const category = categoryMap[e.action.split('.')[0]] || '系统'
  const resultMap: Record<string, AuditItem['result']> = {
    allow: '允许',
    allowed: '允许',
    deny: '拒绝',
    denied: '拒绝',
    approve: '允许',
    approved: '允许',
    reject: '拒绝',
    rejected: '拒绝',
    queued: '待处理',
    dispatched: '系统',
    completed: '完成',
  }
  const actionVerb = e.action.split('.').slice(-1)[0]
  const result = resultMap[actionVerb] || '系统'
  const meta = (e.metadata ?? {}) as { dingtalk_user_name?: string; dingtalk_user_id?: string; auth_channel?: string }
  const dingTalkApprover = meta.dingtalk_user_name || meta.dingtalk_user_id
  const actor = dingTalkApprover
    ? `钉钉 · ${dingTalkApprover}`
    : e.actor_id
      ? `${e.actor_type || 'principal'} · ${e.actor_id.slice(0, 8)}${e.credential_id ? ` · key ${e.credential_id.slice(0, 8)}` : ''}`
      : e.connector_id?.slice(0, 8) || 'system'
  return {
    id: e.id,
    time: new Date(e.created_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }),
    category,
    event: e.summary || e.action,
    actor,
    result,
    traceId: e.trace_id || e.id.slice(0, 8),
  }
}
