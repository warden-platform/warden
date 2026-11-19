import { describe, expect, it } from 'vitest'
import type { ApiAction, ApiConnector, ApiExecution, ApiResource, Integration } from '../../../shared/api'
import { buildTopology } from './buildTopology'

const connector = (over: Partial<ApiConnector> = {}): ApiConnector => ({
  id: 'c1',
  name: 'prod-cluster',
  connector_type: 'kubernetes',
  status: 'ACTIVE',
  environment: 'production',
  version: '1.0.0',
  capabilities: [],
  config: {},
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  ...over,
})

const integration = (over: Partial<Integration> = {}): Integration => ({
  id: 'i1',
  connector_id: 'c1',
  protocol: 'jenkins',
  name: 'jenkins-ci',
  endpoint: 'https://jenkins.example.test',
  credential_ref: 'warden:cred-1',
  project_ids: [],
  project_rules: [],
  scope: {},
  status: 'ACTIVE',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  ...over,
})

const resource = (over: Partial<ApiResource> = {}): ApiResource => ({
  id: 'r1',
  resource_type: 'jenkins_build',
  external_id: 'job-1',
  name: 'job-1',
  connector_id: 'c1',
  integration_id: 'i1',
  project_scope_mode: 'INHERITED',
  project_ids: [],
  project_names: [],
  environment: 'testing',
  status: 'ACTIVE',
  labels: {},
  annotations: {},
  metadata: {},
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  ...over,
})

const action = (over: Partial<ApiAction> = {}): ApiAction => ({
  id: 'a1',
  name: 'jenkins.job.get',
  version: 1,
  resource_type: 'jenkins_build',
  risk_level: 'low',
  execution_mode: 'SYNCHRONOUS',
  parameter_schema: {},
  timeout_seconds: 60,
  ...over,
})

const execution = (over: Partial<ApiExecution> = {}): ApiExecution => ({
  id: 'e1',
  status: 'RUNNING',
  actionId: 'a1',
  resourceId: 'r1',
  traceId: 't1',
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  ...over,
})

describe('buildTopology', () => {
  it('folds many resources of one type under the same parent into a single group node', () => {
    const resources = Array.from({ length: 359 }, (_, index) => resource({ id: `r${index}`, external_id: `job-${index}`, name: `job-${index}` }))
    const graph = buildTopology([connector()], [integration()], resources, [action()], [])
    const groups = graph.nodes.filter((n) => n.data.entity === 'resource-group')
    expect(groups).toHaveLength(1)
    expect(groups[0].data.count).toBe(359)
    expect(groups[0].data.label).toContain('359')
    expect(groups[0].data.status).toBe('active')
    // connector -> integration -> group -> action chain exists
    expect(graph.edges.some((e) => e.source === 'connector:c1' && e.target === 'integration:i1')).toBe(true)
    expect(graph.edges.some((e) => e.source === 'integration:i1' && e.target === groups[0].id)).toBe(true)
    expect(graph.nodes.some((n) => n.id === 'action:jenkins_build')).toBe(true)
  })

  it('honors topologyCount metadata and reflects partial availability', () => {
    const resources = [
      resource({ id: 'r1', metadata: { topologyCount: 10 } }),
      resource({ id: 'r2', status: 'ORPHANED', metadata: { topologyCount: 5 } }),
    ]
    const graph = buildTopology([connector()], [integration()], resources, [], [])
    const group = graph.nodes.find((n) => n.data.entity === 'resource-group')
    expect(group?.data.count).toBe(15)
    expect(group?.data.status).toBe('warning')
    expect(group?.data.sublabel).toBe('10 可用')
  })

  it('lights up the whole chain for in-flight executions', () => {
    const graph = buildTopology([connector()], [integration()], [resource()], [action()], [execution({ status: 'RUNNING' })])
    const connectorNode = graph.nodes.find((n) => n.id === 'connector:c1')
    const group = graph.nodes.find((n) => n.data.entity === 'resource-group')
    expect(connectorNode?.data.live).toBe(true)
    expect(group?.data.live).toBe(true)
    expect(graph.edges.find((e) => e.source === 'connector:c1')?.animated).toBe(true)
  })

  it('does not light up for old finished executions', () => {
    const old = new Date(Date.now() - 60 * 60 * 1000).toISOString()
    const graph = buildTopology([connector()], [integration()], [resource()], [action()], [execution({ status: 'SUCCEEDED', createdAt: old, updatedAt: old })])
    expect(graph.nodes.find((n) => n.id === 'connector:c1')?.data.live).toBe(false)
    expect(graph.edges.find((e) => e.source === 'connector:c1')?.animated).toBe(false)
  })

  it('groups resources without integration directly under their connector', () => {
    const graph = buildTopology([connector()], [], [resource({ integration_id: undefined, resource_type: 'deployment' })], [], [])
    const group = graph.nodes.find((n) => n.data.entity === 'resource-group')
    expect(group?.id).toBe('group:c1:deployment')
    expect(graph.edges.some((e) => e.source === 'connector:c1' && e.target === 'group:c1:deployment')).toBe(true)
  })
})