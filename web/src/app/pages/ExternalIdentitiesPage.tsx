import { useEffect, useMemo, useState } from 'react'
import { Bot, Link2, Loader2, Plus, RefreshCw, ShieldCheck, Trash2, UserRound } from 'lucide-react'
import type { ApiError, ApiExternalBinding, ApiFederatedProjectBinding, ApiFederatedTenantBinding, ApiPrincipal, ApiProject } from '../../shared/api'
import {
  bindFederatedProject, bindFederatedTenant, createExternalBinding, listExternalBindings, listFederatedProjects,
  listFederatedTenants, listPrincipals, listProjects, revokeExternalBinding, revokeFederatedProject,
} from '../../shared/api'
import { Badge } from '../components/Badge'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { useApp } from '../components/Toast'

type BindingType = ApiExternalBinding['identityType']

const EMPTY_FORM = {
  source: 'federated', workspaceId: '', externalProjectId: '', identityType: 'AGENT' as BindingType,
  externalId: '', principalId: '', projectId: '',
}
const EMPTY_TENANT_FORM = { externalEnterpriseId: '', externalWorkspaceId: '' }
const EMPTY_PROJECT_FORM = { externalWorkspaceId: '', externalProjectId: '', externalProjectName: '', projectId: '' }

const formatTime = (value?: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'

export function ExternalIdentitiesPage() {
  const { notify } = useApp()
  const [bindings, setBindings] = useState<ApiExternalBinding[]>([])
  const [projects, setProjects] = useState<ApiProject[]>([])
  const [agents, setAgents] = useState<ApiPrincipal[]>([])
  const [users, setUsers] = useState<ApiPrincipal[]>([])
  const [tenantBindings, setTenantBindings] = useState<ApiFederatedTenantBinding[]>([])
  const [projectBindings, setProjectBindings] = useState<ApiFederatedProjectBinding[]>([])
  const [form, setForm] = useState(EMPTY_FORM)
  const [tenantForm, setTenantForm] = useState(EMPTY_TENANT_FORM)
  const [projectForm, setProjectForm] = useState(EMPTY_PROJECT_FORM)
  const [creating, setCreating] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

  const load = () => {
    setLoading(true); setError('')
    Promise.all([
      listExternalBindings(), listProjects(), listPrincipals('agent_instance'), listPrincipals('workload'), listPrincipals('user'),
      listFederatedTenants(), listFederatedProjects(),
    ]).then(([bindingItems, projectItems, agentItems, workloadItems, userItems, tenantItems, federatedProjectItems]) => {
      setBindings(bindingItems ?? [])
      setProjects(projectItems ?? [])
      setAgents([...(agentItems ?? []), ...(workloadItems ?? [])])
      setUsers(userItems ?? [])
      setTenantBindings(tenantItems ?? [])
      setProjectBindings(federatedProjectItems ?? [])
    }).catch((e: ApiError) => setError(e.message || '加载外部身份失败')).finally(() => setLoading(false))
  }
  useEffect(load, [])

  const principalNames = useMemo(() => new Map([...agents, ...users].map((p) => [p.id, p.name])), [agents, users])
  const projectNames = useMemo(() => new Map(projects.map((p) => [p.id, p.name])), [projects])
  const selectedPrincipals = form.identityType === 'AGENT' ? agents : users
  const selectedPrincipal = selectedPrincipals.find((p) => p.id === form.principalId)

  const updateType = (identityType: BindingType) => {
    setForm((current) => ({ ...current, identityType, principalId: '', projectId: identityType === 'USER' ? '' : current.projectId }))
  }

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    setBusy('create')
    const body = {
      source: form.source.trim(), workspaceId: form.workspaceId.trim(), externalProjectId: form.externalProjectId.trim() || undefined,
      identityType: form.identityType, externalId: form.externalId.trim(), principalId: form.principalId,
      projectId: form.identityType === 'AGENT' ? form.projectId : undefined,
    }
    createExternalBinding(body).then((created) => {
      setBindings((current) => [created, ...current]); setForm(EMPTY_FORM); setCreating(false); notify('外部身份 Binding 已创建')
    }).catch((e: ApiError) => notify(`创建 Binding 失败：${e.message}`)).finally(() => setBusy(''))
  }

  const revoke = (binding: ApiExternalBinding) => {
    if (!window.confirm(`确认撤销 ${binding.source}/${binding.externalId}？关联的委托 Token 会立即失效。`)) return
    setBusy(binding.id)
    revokeExternalBinding(binding.id).then(() => {
      setBindings((current) => current.filter((item) => item.id !== binding.id)); notify('Binding 已撤销')
    }).catch((e: ApiError) => notify(`撤销失败：${e.message}`)).finally(() => setBusy(''))
  }

  const submitTenant = (event: React.FormEvent) => {
    event.preventDefault(); setBusy('federated-tenant')
    bindFederatedTenant({ source: 'federated', externalEnterpriseId: tenantForm.externalEnterpriseId.trim(), externalWorkspaceId: tenantForm.externalWorkspaceId.trim() }).then((binding) => {
      setTenantBindings((items) => [binding, ...items.filter((item) => item.id !== binding.id && item.externalEnterpriseId !== binding.externalEnterpriseId)])
      setTenantForm(EMPTY_TENANT_FORM); notify('联邦企业已绑定当前 PAM Tenant')
    }).catch((e: ApiError) => notify(`企业绑定失败：${e.message}`)).finally(() => setBusy(''))
  }

  const submitProject = (event: React.FormEvent) => {
    event.preventDefault(); setBusy('federated-project')
    bindFederatedProject({ source: 'federated', externalWorkspaceId: projectForm.externalWorkspaceId.trim(), externalProjectId: projectForm.externalProjectId.trim(), externalProjectName: projectForm.externalProjectName.trim() || undefined, projectId: projectForm.projectId }).then((binding) => {
      setProjectBindings((items) => [binding, ...items.filter((item) => item.id !== binding.id && !(item.externalWorkspaceId === binding.externalWorkspaceId && item.externalProjectId === binding.externalProjectId))])
      setProjectForm(EMPTY_PROJECT_FORM); notify('联邦 Project 已绑定 PAM Project')
    }).catch((e: ApiError) => notify(`Project 绑定失败：${e.message}`)).finally(() => setBusy(''))
  }

  const revokeProject = (binding: ApiFederatedProjectBinding) => {
    if (!window.confirm(`确认解除 ${binding.externalProjectName || binding.externalProjectId} 的 PAM Project 绑定？`)) return
    setBusy(`federated-project-${binding.id}`)
    revokeFederatedProject(binding.id).then(() => {
      setProjectBindings((items) => items.filter((item) => item.id !== binding.id)); notify('Project 绑定已解除')
    }).catch((e: ApiError) => notify(`解除绑定失败：${e.message}`)).finally(() => setBusy(''))
  }

  if (loading) return <LoadingState label="加载外部身份 Binding" />
  if (error) return <ErrorState message={error} onRetry={load} />

  return <div className="view-stack">
    <div className="summary-strip external-identity-summary">
      <div><Link2 size={19} /><span>有效 Binding</span><strong>{bindings.length}</strong></div>
      <div><Bot size={19} /><span>Agent 映射</span><strong>{bindings.filter((item) => item.identityType === 'AGENT').length}</strong></div>
      <div><UserRound size={19} /><span>User 映射</span><strong>{bindings.filter((item) => item.identityType === 'USER').length}</strong></div>
      <div><ShieldCheck size={19} /><span>安全边界</span><strong>Token Exchange</strong></div>
    </div>
    <div className="external-identity-actions">
      <div className="identity-security-note"><ShieldCheck size={17} /><span>长期 PAM Key 只保留在 Broker；Agent 通过受信 Binding 换取短期委托 Token。</span></div>
      <div className="header-actions"><button className="icon-button" onClick={load} aria-label="刷新 Binding" title="刷新"><RefreshCw size={16} /></button><button className="primary-button" onClick={() => setCreating((current) => !current)}><Plus size={16} />新增 Binding</button></div>
    </div>
    <section className="panel">
      <div className="panel-header"><div><h2>联邦企业授权</h2><p>企业标识使用已认证的统一社会信用代码；Workspace 只作为企业内的外部上下文。绑定不会自动导入普通 Workspace 成员。</p></div></div>
      <form className="form-grid external-identity-form" onSubmit={submitTenant}>
        <label><span>企业标识（统一社会信用代码）</span><input required maxLength={255} value={tenantForm.externalEnterpriseId} onChange={(event) => setTenantForm({ ...tenantForm, externalEnterpriseId: event.target.value })} placeholder="9131..." /></label>
        <label><span>联邦 Workspace ID</span><input required maxLength={255} value={tenantForm.externalWorkspaceId} onChange={(event) => setTenantForm({ ...tenantForm, externalWorkspaceId: event.target.value })} placeholder="workspace UUID" /></label>
        <div className="external-identity-form-hint">只建立 联邦企业与当前 PAM Tenant 的关系；用户和 Project 权限仍由 PAM 管理员显式分配。</div>
        <div className="form-actions"><button className="primary-button" disabled={busy === 'federated-tenant'}>{busy === 'federated-tenant' ? <Loader2 size={16} className="spin" /> : <Link2 size={16} />}保存企业绑定</button></div>
      </form>
      {tenantBindings.length > 0 && <div className="table-wrap"><table><thead><tr><th>企业标识</th><th>Workspace</th><th>来源</th><th>更新时间</th></tr></thead><tbody>{tenantBindings.map((binding) => <tr key={binding.id}>
        <td><strong>{binding.externalEnterpriseId}</strong></td><td>{binding.externalWorkspaceId}</td><td><Badge tone="success">{binding.source}</Badge></td><td>{formatTime(binding.updatedAt)}</td>
      </tr>)}</tbody></table></div>}
    </section>
    <section className="panel">
      <div className="panel-header"><div><h2>联邦 Project 手工绑定</h2><p>选择已有 PAM Project；不会自动创建项目，也不会把 联邦 Workspace member 自动加入 PAM。</p></div></div>
      <form className="form-grid external-identity-form" onSubmit={submitProject}>
        <label><span>联邦 Workspace ID</span><input required maxLength={255} value={projectForm.externalWorkspaceId} onChange={(event) => setProjectForm({ ...projectForm, externalWorkspaceId: event.target.value })} placeholder="workspace UUID" /></label>
        <label><span>联邦 Project ID</span><input required maxLength={255} value={projectForm.externalProjectId} onChange={(event) => setProjectForm({ ...projectForm, externalProjectId: event.target.value })} placeholder="project UUID" /></label>
        <label><span>联邦 Project 名称（可选）</span><input maxLength={255} value={projectForm.externalProjectName} onChange={(event) => setProjectForm({ ...projectForm, externalProjectName: event.target.value })} /></label>
        <label><span>PAM Project</span><select required value={projectForm.projectId} onChange={(event) => setProjectForm({ ...projectForm, projectId: event.target.value })}><option value="">选择已有 PAM Project</option>{projects.map((project) => <option key={project.id} value={project.id}>{project.name}{project.is_default ? '（默认）' : ''}</option>)}</select></label>
        <div className="external-identity-form-hint">绑定后，联邦 Assertion 中的 Project ID 只能解析到这里选择的 PAM Project。</div>
        <div className="form-actions"><button className="primary-button" disabled={busy === 'federated-project'}>{busy === 'federated-project' ? <Loader2 size={16} className="spin" /> : <Link2 size={16} />}保存 Project 绑定</button></div>
      </form>
      {projectBindings.length > 0 && <div className="table-wrap"><table><thead><tr><th>联邦 Project</th><th>Workspace</th><th>PAM Project</th><th>更新时间</th><th>操作</th></tr></thead><tbody>{projectBindings.map((binding) => <tr key={binding.id}>
        <td><strong>{binding.externalProjectName || binding.externalProjectId}</strong><small className="cell-sub">{binding.externalProjectId}</small></td>
        <td>{binding.externalWorkspaceId}</td><td><strong>{projectNames.get(binding.projectId) || binding.projectId}</strong></td><td>{formatTime(binding.updatedAt)}</td>
        <td><button className="danger-button compact" disabled={busy === `federated-project-${binding.id}`} onClick={() => revokeProject(binding)}>{busy === `federated-project-${binding.id}` ? <Loader2 size={14} className="spin" /> : <Trash2 size={14} />}解除绑定</button></td>
      </tr>)}</tbody></table></div>}
    </section>
    {creating && <section className="panel external-identity-form-panel">
      <div className="panel-header"><div><h2>新增外部身份 Binding</h2><p>Binding 由租户管理员维护，Exchange 时会重新验证身份和项目权限。</p></div></div>
      <form className="form-grid external-identity-form" onSubmit={submit}>
        <label><span>外部平台</span><input required maxLength={64} pattern="[a-z][a-z0-9._-]*" value={form.source} onChange={(e) => setForm({ ...form, source: e.target.value.toLowerCase() })} placeholder="federated" /></label>
        <label><span>Workspace ID</span><input required maxLength={255} value={form.workspaceId} onChange={(e) => setForm({ ...form, workspaceId: e.target.value })} placeholder="workspace-prod" /></label>
        <label><span>身份类型</span><select value={form.identityType} onChange={(e) => updateType(e.target.value as BindingType)}><option value="AGENT">AGENT · 外部 Agent</option><option value="USER">USER · 外部用户</option></select></label>
        <label><span>外部 ID</span><input required maxLength={255} value={form.externalId} onChange={(e) => setForm({ ...form, externalId: e.target.value })} placeholder={form.identityType === 'AGENT' ? 'agent-release-01' : 'user-1001'} /></label>
        <label><span>外部 Project ID {form.identityType === 'USER' ? '（可选）' : ''}</span><input maxLength={255} value={form.externalProjectId} onChange={(e) => setForm({ ...form, externalProjectId: e.target.value })} placeholder={form.identityType === 'USER' ? '留空表示整个 Workspace' : 'external-project-payments'} /></label>
        <label><span>PAM Principal</span><select required value={form.principalId} onChange={(e) => setForm({ ...form, principalId: e.target.value })}><option value="">选择 Principal</option>{selectedPrincipals.map((principal) => <option key={principal.id} value={principal.id}>{principal.name} · {principal.kind}</option>)}</select></label>
        {form.identityType === 'AGENT' && <label><span>PAM Project</span><select required value={form.projectId} onChange={(e) => setForm({ ...form, projectId: e.target.value })}><option value="">选择项目</option>{projects.map((project) => <option key={project.id} value={project.id}>{project.name}{project.is_default ? '（默认）' : ''}</option>)}</select></label>}
        <div className="external-identity-form-hint">{selectedPrincipal ? `已选择：${selectedPrincipal.name}` : form.identityType === 'AGENT' ? 'Agent Binding 必须固定一个 PAM Project。' : 'User Binding 可限制到外部 Project，也可覆盖整个 Workspace。'}</div>
        <div className="form-actions"><button type="button" className="quiet-button" onClick={() => setCreating(false)}>取消</button><button className="primary-button" disabled={busy === 'create' || !form.principalId}>{busy === 'create' ? <Loader2 size={16} className="spin" /> : <Link2 size={16} />}创建 Binding</button></div>
      </form>
    </section>}
    {bindings.length === 0 ? <EmptyState icon={Link2} title="尚未配置外部身份 Binding" text="先把 外部平台 Agent 和用户映射到 PAM Principal，Broker 才能申请委托 Token。" /> : <section className="panel">
      <div className="panel-header"><div><h2>外部身份映射</h2><p>撤销 Binding 会立即阻断使用其身份上下文签发的委托 Token。</p></div></div>
      <div className="table-wrap"><table><thead><tr><th>类型</th><th>外部身份</th><th>Workspace / Project</th><th>PAM Principal</th><th>PAM Project</th><th>创建时间</th><th>操作</th></tr></thead><tbody>{bindings.map((binding) => <tr key={binding.id}>
        <td><Badge tone={binding.identityType === 'AGENT' ? 'info' : 'success'}>{binding.identityType}</Badge></td>
        <td><strong>{binding.externalId}</strong><small className="cell-sub">{binding.source}</small></td>
        <td><strong>{binding.workspaceId}</strong><small className="cell-sub">{binding.externalProjectId || '整个 Workspace'}</small></td>
        <td><strong>{principalNames.get(binding.principalId) || binding.principalId.slice(0, 8)}</strong><small className="cell-sub">{binding.principalId}</small></td>
        <td>{binding.projectId ? projectNames.get(binding.projectId) || binding.projectId.slice(0, 8) : <span className="text-muted">不适用</span>}</td>
        <td>{formatTime(binding.createdAt)}</td>
        <td><button className="danger-button compact" disabled={busy === binding.id} onClick={() => revoke(binding)}>{busy === binding.id ? <Loader2 size={14} className="spin" /> : <Trash2 size={14} />}撤销</button></td>
      </tr>)}</tbody></table></div>
    </section>}
  </div>
}
