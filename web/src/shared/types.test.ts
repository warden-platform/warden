import { describe, expect, it } from 'vitest'
import {
  connectorTypeLabel,
  mapActionName,
  mapConnectorStatus,
  mapEnvironment,
  mapExecutionStatus,
  mapResourceStatus,
  mapRiskLevel,
} from './types'

describe('mapExecutionStatus', () => {
  it('maps every backend state to the UI label', () => {
    expect(mapExecutionStatus('WAITING_APPROVAL')).toBe('等待审批')
    expect(mapExecutionStatus('QUEUED')).toBe('排队中')
    expect(mapExecutionStatus('DISPATCHED')).toBe('执行中')
    expect(mapExecutionStatus('RUNNING')).toBe('执行中')
    expect(mapExecutionStatus('SUCCEEDED')).toBe('成功')
    expect(mapExecutionStatus('FAILED')).toBe('失败')
    expect(mapExecutionStatus('CANCELLED')).toBe('失败')
    expect(mapExecutionStatus('CANCELLING')).toBe('失败')
    expect(mapExecutionStatus('EXPIRED')).toBe('失败')
    expect(mapExecutionStatus('UNKNOWN')).toBe('结果未知')
  })

  it('documents the current unknown-status fallback (queued)', () => {
    // Known design debt: unknown states are masked as 排队中 instead of being
    // surfaced. This test pins current behavior so a future fix is deliberate.
    expect(mapExecutionStatus('SOME_NEW_STATE')).toBe('排队中')
  })
})

describe('mapConnectorStatus', () => {
  it('maps lifecycle states and passes unknown values through', () => {
    expect(mapConnectorStatus('ACTIVE')).toBe('健康')
    expect(mapConnectorStatus('DEGRADED')).toBe('降级')
    expect(mapConnectorStatus('QUARANTINED')).toBe('隔离')
    expect(mapConnectorStatus('DISABLED')).toBe('已停用')
    expect(mapConnectorStatus('BRAND_NEW')).toBe('BRAND_NEW')
  })
})

describe('mapResourceStatus', () => {
  it('maps ACTIVE/ORPHANED and treats the rest as attention', () => {
    expect(mapResourceStatus('ACTIVE')).toBe('正常')
    expect(mapResourceStatus('ORPHANED')).toBe('历史')
    expect(mapResourceStatus('DISCOVERED')).toBe('注意')
    expect(mapResourceStatus('DISABLED')).toBe('注意')
    expect(mapResourceStatus('WHATEVER')).toBe('注意')
  })
})

describe('mapEnvironment', () => {
  it('only production and development are special; everything else is testing', () => {
    expect(mapEnvironment('production')).toBe('生产')
    expect(mapEnvironment('development')).toBe('开发')
    expect(mapEnvironment('testing')).toBe('测试')
    expect(mapEnvironment('')).toBe('测试')
  })
})

describe('mapRiskLevel', () => {
  it('maps the four risk tiers', () => {
    expect(mapRiskLevel('low')).toBe('低')
    expect(mapRiskLevel('medium')).toBe('中')
    expect(mapRiskLevel('high')).toBe('高')
    expect(mapRiskLevel('critical')).toBe('高')
  })
})

describe('labels', () => {
  it('connectorTypeLabel covers the common channels', () => {
    expect(connectorTypeLabel('kubernetes')).toContain('Kubernetes')
    expect(connectorTypeLabel('linux')).toContain('Linux')
  })

  it('mapActionName translates known actions and keeps unknown names visible', () => {
    expect(mapActionName('deployment.update_image')).not.toBe('deployment.update_image')
    expect(mapActionName('totally.unknown.action')).toBe('totally.unknown.action')
  })
})