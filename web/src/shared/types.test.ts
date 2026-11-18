import { describe, expect, it } from 'vitest'
import {
  connectorTypeLabel,
  executionLedClass,
  executionStatusGroup,
  executionStatusTone,
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

  it('surfaces unknown statuses instead of masking them as queued', () => {
    expect(mapExecutionStatus('SOME_NEW_STATE')).toBe('未知状态')
    expect(mapExecutionStatus('')).toBe('未知状态')
  })
})

describe('execution logic helpers key on the raw backend enum', () => {
  it('executionStatusTone', () => {
    expect(executionStatusTone('SUCCEEDED')).toBe('success')
    expect(executionStatusTone('FAILED')).toBe('danger')
    expect(executionStatusTone('CANCELLED')).toBe('danger')
    expect(executionStatusTone('EXPIRED')).toBe('danger')
    expect(executionStatusTone('WAITING_APPROVAL')).toBe('warning')
    expect(executionStatusTone('RUNNING')).toBe('info')
    expect(executionStatusTone('QUEUED')).toBe('info')
    expect(executionStatusTone('UNKNOWN')).toBe('neutral')
    expect(executionStatusTone('SOME_NEW_STATE')).toBe('neutral')
  })

  it('executionStatusGroup', () => {
    expect(executionStatusGroup('SUCCEEDED')).toBe('success')
    expect(executionStatusGroup('DISPATCHED')).toBe('running')
    expect(executionStatusGroup('QUEUED')).toBe('running')
    expect(executionStatusGroup('WAITING_APPROVAL')).toBe('waiting')
    expect(executionStatusGroup('CANCELLING')).toBe('failed')
    expect(executionStatusGroup('SOME_NEW_STATE')).toBe('other')
  })

  it('executionLedClass', () => {
    expect(executionLedClass('SUCCEEDED')).toBe('ok')
    expect(executionLedClass('FAILED')).toBe('deny')
    expect(executionLedClass('WAITING_APPROVAL')).toBe('wait')
    expect(executionLedClass('RUNNING')).toBe('run')
    expect(executionLedClass('SOME_NEW_STATE')).toBe('')
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