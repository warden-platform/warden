import { useEffect, useMemo, useState } from 'react'
import { AlertTriangle, ArrowRight, Ban, Copy, Lock, Pencil, Play, Plus, Power, ShieldCheck, Trash2, Unlock, X } from 'lucide-react'
import type {
  ApiAction,
  ApiError,
  ApiPolicyRule,
  ApiProject,
  PolicyDecision,
  PolicyRuleInput,
  PolicySimulationInput,
  PolicySimulationResult,
} from '../../shared/api'
import {
  copyPolicyRule,
  createPolicyRule,
  deletePolicyRule,
  listAvailableActions,
  listPolicyRules,
  listProjects,
  simulatePolicyRule,
  updatePolicyRule,
} from '../../shared/api'
import { mapActionName } from '../../shared/types'
import { resourceTypeLabel } from '../lib/labels'
import { ErrorState, LoadingState } from '../components/States'
import { useApp } from '../components/Toast'
import { DingTalkChannelPanel } from '../components/DingTalkChannelPanel'

const RISK_LABELS: Record<string, string> = {
  '': '所有风险',
  low: '低风险',
  medium: '中风险',
  high: '高风险',
  critical: '严重风险',
}
const ENV_LABELS: Record<string, string> = { '*': '所有环境', development: '开发环境', testing: '测试环境', production: '生产环境' }
const PRINCIPAL_LABELS: Record<string, string> = {
  '': '所有身份',
  user: '管理用户',
  agent_type: 'Agent 类型',
  agent_instance: 'Agent 实例',
  workload: '工作负载',
}
const DECISION_LABELS: Record<PolicyDecision, string> = {
  ALLOW: '自动放行',
  REQUIRE_APPROVAL: '需要审批',
  DENY: '拒绝',
}

const EMPTY_RULE: PolicyRuleInput = {
  name: '',
  project_id: '',
  resource_type: '*',
  action_name: '*',
  environment: '*',
  risk_level: '',
  principal_kind: '',
  decision: 'REQUIRE_APPROVAL',
  priority: 100,
  enabled: true,
  description: '',
}

function toInput(rule: ApiPolicyRule): PolicyRuleInput {
  return {
    name: rule.name,
    project_id: rule.project_id ?? '',
    resource_type: rule.resource_type,
    action_name: rule.action_name,
    environment: rule.environment,
    risk_level: rule.risk_level,
    principal_kind: rule.principal_kind,
    decision: rule.decision,
    priority: rule.priority,
    enabled: rule.enabled,
    description: rule.description ?? '',
  }
}

function DecisionBadge({ decision }: { decision: PolicyDecision }) {
  if (decision === 'DENY')
    return (
      <span className="badge badge-danger">
        <Ban size={12} />
        拒绝
      </span>
    )
  if (decision === 'REQUIRE_APPROVAL')
    return (
      <span className="badge badge-warning">
        <Lock size={12} />
        需要审批
      </span>
    )
  return (
    <span className="badge badge-success">
      <Unlock size={12} />
      自动放行
    </span>
  )
}

export function PoliciesPage() {
  const [rules, setRules] = useState<ApiPolicyRule[]>([])
  const [projects, setProjects] = useState<ApiProject[]>([])
  const [actions, setActions] = useState<ApiAction[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<string | null>(null)
  const [editing, setEditing] = useState<ApiPolicyRule | null | undefined>(undefined)
  const [form, setForm] = useState<PolicyRuleInput>(EMPTY_RULE)
  const [simulation, setSimulation] = useState<PolicySimulationInput>({
    project_id: '',
    resource_type: '',
    action_name: '',
    environment: 'production',
    risk_level: '',
    principal_kind: 'user',
    base_decision: 'REQUIRE_APPROVAL',
  })
  const [simulationResult, setSimulationResult] = useState<PolicySimulationResult | null>(null)
  const { notify } = useApp()

  const load = () => {
    setLoading(true)
    setError('')
    Promise.all([listPolicyRules(), listProjects(), listAvailableActions()])
      .then(([ruleItems, projectItems, actionItems]) => {
        setRules(ruleItems ?? [])
        setProjects(projectItems ?? [])
        setActions(actionItems ?? [])
        const first = actionItems?.[0]
        if (first)
          setSimulation((current) =>
            current.action_name
              ? current
              : {
                  ...current,
                  action_name: first.name,
                  resource_type: first.resource_type,
                  risk_level: first.risk_level,
                },
          )
      })
      .catch((e: ApiError) => setError(e.message || '加载策略中心失败'))
      .finally(() => setLoading(false))
  }
  useEffect(load, [])

  const actionByName = useMemo(() => new Map(actions.map((action) => [action.name, action])), [actions])
  const ruleByID = useMemo(() => new Map(rules.map((rule) => [rule.id, rule])), [rules])
  const resourceTypes = useMemo(() => [...new Set(actions.map((action) => action.resource_type))].sort(), [actions])
  const sorted = useMemo(() => [...rules].sort((a, b) => b.priority - a.priority || a.name.localeCompare(b.name)), [rules])

  const openCreate = () => {
    setForm({ ...EMPTY_RULE })
    setEditing(null)
  }
  const openEdit = (rule: ApiPolicyRule) => {
    setForm(toInput(rule))
    setEditing(rule)
  }
  const closeEditor = () => setEditing(undefined)

  const chooseAction = (name: string, target: 'form' | 'simulation') => {
    const action = actionByName.get(name)
    if (target === 'form') {
      setForm((current) => ({
        ...current,
        action_name: name,
        ...(action ? { resource_type: action.resource_type, risk_level: action.risk_level } : {}),
      }))
    } else {
      setSimulation((current) => ({
        ...current,
        action_name: name,
        ...(action ? { resource_type: action.resource_type, risk_level: action.risk_level } : {}),
      }))
    }
  }

  const save = (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(editing?.id ?? 'create')
    const request = editing ? updatePolicyRule(editing.id, form) : createPolicyRule(form)
    request
      .then(() => {
        notify(editing ? '策略规则已更新' : '策略规则已创建')
        closeEditor()
        load()
      })
      .catch((e: ApiError) => notify(e.message || '保存策略规则失败'))
      .finally(() => setBusy(null))
  }

  const toggle = (rule: ApiPolicyRule) => {
    setBusy(rule.id)
    updatePolicyRule(rule.id, { ...toInput(rule), enabled: !rule.enabled })
      .then(() => {
        notify(rule.enabled ? '规则已停用' : '规则已启用')
        load()
      })
      .catch((e: ApiError) => notify(e.message || '更新失败'))
      .finally(() => setBusy(null))
  }

  const copy = (rule: ApiPolicyRule) => {
    setBusy(rule.id)
    copyPolicyRule(rule.id)
      .then(() => {
        notify('已复制为停用规则，请编辑后启用')
        load()
      })
      .catch((e: ApiError) => notify(e.message || '复制失败'))
      .finally(() => setBusy(null))
  }

  const remove = (rule: ApiPolicyRule) => {
    if (!window.confirm(`确认删除策略规则「${rule.name}」？`)) return
    setBusy(rule.id)
    deletePolicyRule(rule.id)
      .then(() => {
        notify('策略规则已删除')
        load()
      })
      .catch((e: ApiError) => notify(e.message || '删除失败'))
      .finally(() => setBusy(null))
  }

  const runSimulation = (event: React.FormEvent) => {
    event.preventDefault()
    setBusy('simulate')
    setSimulationResult(null)
    simulatePolicyRule(simulation)
      .then(setSimulationResult)
      .catch((e: ApiError) => notify(e.message || '策略模拟失败'))
      .finally(() => setBusy(null))
  }

  if (loading && rules.length === 0) return <LoadingState />
  if (error) return <ErrorState message={error} onRetry={load} />

  return (
    <div className="view-stack">
      <section className="panel">
        <div className="panel-header">
          <div>
            <h2>执行策略规则</h2>
            <p className="panel-desc">共 {sorted.length} 条 · 高优先级先匹配，同优先级选条件更精确者</p>
          </div>
          <button className="primary-button" onClick={openCreate}>
            <Plus size={16} />
            新增规则
          </button>
        </div>

        <p className="policy-hint">
          <ShieldCheck size={14} />
          Rego 默认拒绝是安全底线：矩阵规则不能将其放宽，未命中时沿用 Rego 决策。
        </p>

        {editing !== undefined && (
          <form className="policy-rule-editor" onSubmit={save}>
            <div className="panel-header compact">
              <div>
                <span className="eyebrow">{editing ? '编辑规则' : '新建规则'}</span>
                <h3>{editing?.name ?? '配置匹配条件与决策'}</h3>
              </div>
              <button type="button" className="icon-button" aria-label="关闭规则编辑器" onClick={closeEditor}>
                <X size={18} />
              </button>
            </div>
            <div className="form-grid policy-rule-grid">
              <label>
                <span>规则名称</span>
                <input
                  required
                  maxLength={120}
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder="例如：生产 ECS 创建需审批"
                />
              </label>
              <label>
                <span>项目</span>
                <select value={form.project_id ?? ''} onChange={(e) => setForm({ ...form, project_id: e.target.value })}>
                  <option value="">所有项目</option>
                  {projects.map((project) => (
                    <option key={project.id} value={project.id}>
                      {project.name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>资源类型</span>
                <select value={form.resource_type} onChange={(e) => setForm({ ...form, resource_type: e.target.value })}>
                  <option value="*">所有资源</option>
                  {resourceTypes.map((type) => (
                    <option key={type} value={type}>
                      {resourceTypeLabel(type)}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>动作</span>
                <select value={form.action_name} onChange={(e) => chooseAction(e.target.value, 'form')}>
                  <option value="*">所有动作</option>
                  {actions.map((action) => (
                    <option key={action.id} value={action.name}>
                      {mapActionName(action.name)}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>环境</span>
                <select
                  value={form.environment}
                  onChange={(e) => setForm({ ...form, environment: e.target.value as PolicyRuleInput['environment'] })}
                >
                  {Object.entries(ENV_LABELS).map(([value, label]) => (
                    <option key={value} value={value}>
                      {label}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>风险等级</span>
                <select
                  value={form.risk_level}
                  onChange={(e) => setForm({ ...form, risk_level: e.target.value as PolicyRuleInput['risk_level'] })}
                >
                  {Object.entries(RISK_LABELS).map(([value, label]) => (
                    <option key={value || '*'} value={value}>
                      {label}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>执行身份</span>
                <select
                  value={form.principal_kind}
                  onChange={(e) => setForm({ ...form, principal_kind: e.target.value as PolicyRuleInput['principal_kind'] })}
                >
                  {Object.entries(PRINCIPAL_LABELS).map(([value, label]) => (
                    <option key={value || '*'} value={value}>
                      {label}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>决策</span>
                <select value={form.decision} onChange={(e) => setForm({ ...form, decision: e.target.value as PolicyDecision })}>
                  {Object.entries(DECISION_LABELS).map(([value, label]) => (
                    <option key={value} value={value}>
                      {label}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>优先级</span>
                <input
                  type="number"
                  min={0}
                  max={10000}
                  value={form.priority}
                  onChange={(e) => setForm({ ...form, priority: Number(e.target.value) })}
                />
              </label>
              <label className="policy-description">
                <span>说明</span>
                <input
                  maxLength={500}
                  value={form.description ?? ''}
                  onChange={(e) => setForm({ ...form, description: e.target.value })}
                  placeholder="说明适用场景和变更原因"
                />
              </label>
              <label className="check-field">
                <input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} />
                <span>保存后立即启用</span>
              </label>
            </div>
            <div className="drawer-actions">
              <button type="button" className="secondary-button" onClick={closeEditor}>
                取消
              </button>
              <button className="primary-button" disabled={busy !== null}>
                {busy ? '保存中…' : '保存规则'}
              </button>
            </div>
          </form>
        )}

        {sorted.length === 0 ? (
          <p className="empty-hint">暂无策略矩阵规则，系统将完全沿用 Rego 默认策略。</p>
        ) : (
          <div className="table-scroll">
            <table className="data-table policy-rule-table">
              <thead>
                <tr>
                  <th>优先级 / 规则</th>
                  <th>匹配范围</th>
                  <th>动作与身份</th>
                  <th>决策</th>
                  <th>命中</th>
                  <th>状态</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {sorted.map((rule) => {
                  const conflicts = rule.conflict_ids?.map((id) => ruleByID.get(id)?.name ?? id.slice(0, 8)) ?? []
                  const shadow = rule.shadowed_by ? (ruleByID.get(rule.shadowed_by)?.name ?? rule.shadowed_by.slice(0, 8)) : ''
                  return (
                    <tr key={rule.id} className={!rule.enabled ? 'row-disabled' : ''}>
                      <td>
                        <strong className="policy-rule-name">
                          <span>{rule.priority}</span>
                          {rule.name}
                        </strong>
                        {rule.description && <small className="muted">{rule.description}</small>}
                        {conflicts.length > 0 && (
                          <small className="policy-warning">
                            <AlertTriangle size={12} />
                            同优先级重叠：{conflicts.join('、')}
                          </small>
                        )}
                        {shadow && (
                          <small className="policy-warning">
                            <AlertTriangle size={12} />
                            被高优先级规则「{shadow}」遮蔽
                          </small>
                        )}
                      </td>
                      <td>
                        <span>{rule.project_name || '所有项目'}</span>
                        <small>
                          {rule.resource_type === '*' ? '所有资源' : resourceTypeLabel(rule.resource_type)} ·{' '}
                          {ENV_LABELS[rule.environment] || rule.environment} · {RISK_LABELS[rule.risk_level] || rule.risk_level}
                        </small>
                      </td>
                      <td>
                        <span>{rule.action_name === '*' ? '所有动作' : mapActionName(rule.action_name)}</span>
                        <small>{PRINCIPAL_LABELS[rule.principal_kind] || rule.principal_kind}</small>
                      </td>
                      <td>
                        <DecisionBadge decision={rule.decision} />
                      </td>
                      <td>
                        <strong>{rule.hit_count}</strong>
                        <small>{rule.last_hit_at ? new Date(rule.last_hit_at).toLocaleString('zh-CN') : '尚未命中'}</small>
                      </td>
                      <td>
                        <span className={`badge ${rule.enabled ? 'badge-success' : ''}`}>{rule.enabled ? '已启用' : '已停用'}</span>
                      </td>
                      <td>
                        <div className="row-actions">
                          <button className="icon-button" title="编辑" aria-label={`编辑规则 ${rule.name}`} onClick={() => openEdit(rule)}>
                            <Pencil size={15} />
                          </button>
                          <button
                            className="icon-button"
                            title="复制为停用规则"
                            aria-label={`复制规则 ${rule.name}`}
                            disabled={busy === rule.id}
                            onClick={() => copy(rule)}
                          >
                            <Copy size={15} />
                          </button>
                          <button
                            className="icon-button"
                            title={rule.enabled ? '停用' : '启用'}
                            aria-label={`${rule.enabled ? '停用' : '启用'}规则 ${rule.name}`}
                            disabled={busy === rule.id}
                            onClick={() => toggle(rule)}
                          >
                            <Power size={15} />
                          </button>
                          <button
                            className="icon-button danger"
                            title="删除"
                            aria-label={`删除规则 ${rule.name}`}
                            disabled={busy === rule.id}
                            onClick={() => remove(rule)}
                          >
                            <Trash2 size={15} />
                          </button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <div className="policy-secondary-grid">
        <section className="panel">
          <div className="panel-header">
            <div>
              <h2>测试规则命中</h2>
              <p className="panel-desc">预览将命中的矩阵规则和最终决策</p>
            </div>
            <button
              type="button"
              className="primary-button"
              disabled={busy === 'simulate' || !simulation.action_name}
              onClick={runSimulation}
            >
              <Play size={15} />
              {busy === 'simulate' ? '模拟中…' : '开始模拟'}
            </button>
          </div>
          <form className="policy-simulator" onSubmit={runSimulation}>
            <div className="form-grid policy-simulator-grid">
            <label>
              <span>项目</span>
              <select value={simulation.project_id ?? ''} onChange={(e) => setSimulation({ ...simulation, project_id: e.target.value })}>
                <option value="">未指定 / 所有项目</option>
                {projects.map((project) => (
                  <option key={project.id} value={project.id}>
                    {project.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>动作</span>
              <select required value={simulation.action_name} onChange={(e) => chooseAction(e.target.value, 'simulation')}>
                {actions.map((action) => (
                  <option key={action.id} value={action.name}>
                    {mapActionName(action.name)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>环境</span>
              <select
                value={simulation.environment}
                onChange={(e) => setSimulation({ ...simulation, environment: e.target.value as PolicySimulationInput['environment'] })}
              >
                <option value="development">开发环境</option>
                <option value="testing">测试环境</option>
                <option value="production">生产环境</option>
              </select>
            </label>
            <label>
              <span>执行身份</span>
              <select
                value={simulation.principal_kind}
                onChange={(e) =>
                  setSimulation({ ...simulation, principal_kind: e.target.value as PolicySimulationInput['principal_kind'] })
                }
              >
                {Object.entries(PRINCIPAL_LABELS)
                  .filter(([value]) => value)
                  .map(([value, label]) => (
                    <option key={value} value={value}>
                      {label}
                    </option>
                  ))}
              </select>
            </label>
            <label>
              <span>Rego 初始决策</span>
              <select
                value={simulation.base_decision}
                onChange={(e) => setSimulation({ ...simulation, base_decision: e.target.value as PolicyDecision })}
              >
                {Object.entries(DECISION_LABELS).map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
            </label>
          </div>
        </form>
        {simulationResult && (
          <div className="policy-simulation-result">
            <div className="policy-simulation-comparison">
              <div className="policy-simulation-decision">
                <span>Rego 初始决策</span>
                <DecisionBadge decision={simulationResult.base_decision} />
              </div>
              <ArrowRight className="policy-simulation-arrow" size={18} aria-hidden="true" />
              <div className="policy-simulation-decision">
                <span>矩阵最终决策</span>
                <DecisionBadge decision={simulationResult.effective_decision} />
              </div>
            </div>
            <p className="policy-simulation-detail">
              {simulationResult.matched_rule ? (
                <>
                  命中规则：<strong>{simulationResult.matched_rule.name}</strong>
                  <span>优先级 {simulationResult.matched_rule.priority}</span>
                </>
              ) : (
                '未命中矩阵规则，沿用 Rego 决策。'
              )}
              {simulationResult.rego_deny_protected && <span>Rego 拒绝受保护，矩阵不能放宽</span>}
            </p>
          </div>
        )}
      </section>
        <DingTalkChannelPanel />
      </div>
    </div>
  )
}
