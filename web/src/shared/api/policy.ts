import { request } from './core'

// --- Approval Rules ---

export type ApiApprovalRule = {
  id: string
  risk_level: string
  environment: string
  requires_approval: boolean
  priority: number
  description?: string
  created_at?: string
  updated_at?: string
}

export const listApprovalRules = () => request<ApiApprovalRule[]>('GET', '/approval-rules')
export const updateApprovalRule = (id: string, body: { requires_approval?: boolean; priority?: number; description?: string }) =>
  request<ApiApprovalRule>('PUT', `/approval-rules/${id}`, body)

export type PolicyDecision = 'ALLOW' | 'REQUIRE_APPROVAL' | 'DENY'

export type ApiPolicyRule = {
  id: string
  name: string
  project_id?: string
  project_name?: string
  resource_type: string
  action_name: string
  environment: '*' | 'development' | 'testing' | 'production'
  risk_level: '' | 'low' | 'medium' | 'high' | 'critical'
  principal_kind: '' | 'user' | 'agent_type' | 'agent_instance' | 'workload'
  decision: PolicyDecision
  priority: number
  enabled: boolean
  description?: string
  hit_count: number
  last_hit_at?: string
  conflict_ids: string[]
  shadowed_by?: string
  created_at: string
  updated_at: string
}

export type PolicyRuleInput = Omit<ApiPolicyRule,
  'id' | 'project_name' | 'hit_count' | 'last_hit_at' | 'conflict_ids' | 'shadowed_by' | 'created_at' | 'updated_at'>

export type PolicySimulationInput = {
  project_id?: string
  resource_type: string
  action_name: string
  environment: '*' | 'development' | 'testing' | 'production'
  risk_level: '' | 'low' | 'medium' | 'high' | 'critical'
  principal_kind: '' | 'user' | 'agent_type' | 'agent_instance' | 'workload'
  base_decision: PolicyDecision
}

export type PolicySimulationResult = {
  base_decision: PolicyDecision
  effective_decision: PolicyDecision
  matched_rule?: ApiPolicyRule | null
  rego_deny_protected: boolean
}

export const listPolicyRules = () => request<ApiPolicyRule[]>('GET', '/policy-rules')
export const createPolicyRule = (body: PolicyRuleInput) => request<ApiPolicyRule>('POST', '/policy-rules', body)
export const updatePolicyRule = (id: string, body: PolicyRuleInput) => request<ApiPolicyRule>('PUT', `/policy-rules/${id}`, body)
export const deletePolicyRule = (id: string) => request<void>('DELETE', `/policy-rules/${id}`)
export const copyPolicyRule = (id: string) => request<ApiPolicyRule>('POST', `/policy-rules/${id}/copy`)
export const simulatePolicyRule = (body: PolicySimulationInput) => request<PolicySimulationResult>('POST', '/policy-rules/simulate', body)
