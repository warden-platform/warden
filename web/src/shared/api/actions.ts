import { request } from './core'

export type ApiAction = {
  id: string
  name: string
  version: number
  display_name?: string
  description?: string
  resource_type: string
  risk_level: 'low' | 'medium' | 'high' | 'critical'
  execution_mode: 'SYNCHRONOUS' | 'ASYNCHRONOUS'
  executor_type?: 'connector' | 'control_plane'
  parameter_schema: Record<string, unknown>
  timeout_seconds: number
  available?: boolean
  availability_code?:
    'RESOURCE_NOT_ACTIVE' | 'CONNECTOR_NOT_ACTIVE' | 'CONNECTOR_HEARTBEAT_TIMEOUT' | 'INTEGRATION_NOT_ACTIVE' | 'CONNECTOR_UPGRADE_REQUIRED'
  availability_reason?: string
  required_capability?: string
  connector_version?: string
}

export const listAvailableActions = () => request<ApiAction[]>('GET', '/actions')
export const listResourceActions = (id: string, includeUnavailable = false) =>
  request<ApiAction[]>('GET', `/resources/${id}/actions${includeUnavailable ? '?includeUnavailable=true' : ''}`)
