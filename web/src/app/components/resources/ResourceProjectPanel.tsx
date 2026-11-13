import { useEffect, useState } from 'react'
import { ChevronDown, FolderKanban, Globe2, Loader2, LockKeyhole, RotateCcw, Save, Workflow, X } from 'lucide-react'
import type { ApiError, ApiProject, ApiResource } from '../../../shared/api'
import { clearResourceEnvironment, getResourceProjectScope, setResourceEnvironment, updateResourceProjectScope, type ResourceProjectScope } from '../../../shared/api'
import { mapEnvironment } from '../../../shared/types'
import { useApp } from '../Toast'

const sourceText: Record<string, string> = {
  RESOURCE: '单资源覆盖', INTEGRATION_RULE: 'Integration 路由规则', CLOUD_TAG: '云资源项目标签', NAMESPACE: 'Namespace 规则', INTEGRATION: 'Integration 默认项目', CONNECTOR: '主机/集群默认项目', DEFAULT: '系统默认项目',
}
const environmentSourceText: Record<string, string> = {
  RESOURCE: '单资源覆盖', DATABASE_SCHEMA: '继承数据库 Schema', DATABASE_INSTANCE: '继承数据库实例', INTEGRATION: 'Integration 默认环境', CONNECTOR: '主机/集群环境',
}

export function ResourceProjectPanel({ resource, projects, onChanged, onClose }: {
  resource: ApiResource
  projects: ApiProject[]
  onChanged: () => void
  onClose: () => void
}) {
  const { notify } = useApp()
  const [scopeMode, setScopeMode] = useState<ResourceProjectScope['mode']>(resource.project_scope_mode ?? 'INHERITED')
  const [projectIds, setProjectIds] = useState<string[]>(resource.project_ids ?? [])
  const [environment, setEnvironment] = useState<'development' | 'testing' | 'production'>((resource.environment as 'development' | 'testing' | 'production') ?? 'testing')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    setScopeMode(resource.project_scope_mode ?? 'INHERITED')
    setProjectIds(resource.project_ids ?? [])
    setEnvironment((resource.environment as 'development' | 'testing' | 'production') ?? 'testing')
    setReason(''); setError('')
    getResourceProjectScope(resource.id).then((scope) => { setScopeMode(scope.mode); setProjectIds(scope.project_ids ?? []) }).catch(() => undefined)
  }, [resource])
  const toggleProject = (projectId: string) => setProjectIds((current) => current.includes(projectId) ? current.filter((id) => id !== projectId) : [...current, projectId])
  const saveScope = () => {
    if (scopeMode === 'SELECTED_PROJECTS' && projectIds.length === 0) { setError('请至少选择一个可用项目'); return }
    setBusy(true); setError('')
    updateResourceProjectScope(resource.id, { mode: scopeMode, projectIds: scopeMode === 'SELECTED_PROJECTS' ? projectIds : [], reason: reason.trim() })
      .then(() => { notify('资源可用范围已更新'); onChanged(); onClose() })
      .catch((e: ApiError) => setError(e.message || '更新失败')).finally(() => setBusy(false))
  }
  const saveEnvironment = () => {
    setBusy(true); setError('')
    setResourceEnvironment(resource.id, environment, reason.trim())
      .then(() => { notify('资源环境归属已更新'); onChanged(); onClose() })
      .catch((e: ApiError) => setError(e.message || '更新环境失败')).finally(() => setBusy(false))
  }
  const restoreEnvironment = () => {
    setBusy(true); setError('')
    clearResourceEnvironment(resource.id)
      .then(() => { notify('环境已恢复自动继承'); onChanged(); onClose() })
      .catch((e: ApiError) => setError(e.message || '恢复环境继承失败')).finally(() => setBusy(false))
  }
  return <><button className="drawer-scrim" aria-label="关闭资源归属设置" onClick={onClose} /><aside className="drawer resource-project-drawer" aria-label="调整资源项目与环境">
    <div className="drawer-header"><div><span className="eyebrow"><FolderKanban size={15} />资源范围</span><h2>调整可用范围与环境</h2><p>一个资源可以同时开放给多个项目；每次执行仍会记录代表的项目。</p></div><button className="icon-button" onClick={onClose} aria-label="关闭"><X size={20} /></button></div>
    <div className="resource-project-body">
      <dl className="resource-project-facts"><div><dt>资源</dt><dd>{resource.name}</dd></div><div><dt>类型</dt><dd>{resource.resource_type}</dd></div>{resource.namespace && <div><dt>Namespace</dt><dd>{resource.namespace}</dd></div>}<div><dt>当前范围</dt><dd>{scopeMode === 'ALL_PROJECTS' ? '全租户共享' : scopeMode === 'UNASSIGNED' ? '暂不开放' : scopeMode === 'SELECTED_PROJECTS' ? `${projectIds.length} 个指定项目` : '自动继承'}</dd></div><div><dt>继承来源</dt><dd>{sourceText[resource.project_source ?? 'DEFAULT']}</dd></div><div><dt>当前环境</dt><dd>{mapEnvironment(resource.environment)}</dd></div><div><dt>环境来源</dt><dd>{environmentSourceText[resource.environment_source ?? 'CONNECTOR']}</dd></div></dl>
      {scopeMode === 'INHERITED' && <div className="project-inheritance-chain" aria-label="自动继承优先级"><strong>自动继承</strong><span>资源配置</span><i>›</i><span>Namespace / 路由</span><i>›</i><span>Integration</span><i>›</i><span>执行通道（非中间件）</span><i>›</i><span>系统默认</span></div>}
      <div className="project-inheritance-chain" aria-label="环境归属优先级"><strong>环境优先级</strong><span>资源覆盖</span>{resource.resource_type === 'database_table' && <><i>›</i><span>Schema</span></>}{(resource.resource_type === 'database_schema' || resource.resource_type === 'database_table') && <><i>›</i><span>数据库实例</span></>}<i>›</i><span>Integration</span><i>›</i><span>执行通道</span></div>
      <section className="resource-scope-editor"><strong>项目可用范围</strong><div className="scope-mode-options">
        <button type="button" className={scopeMode === 'INHERITED' ? 'selected' : ''} onClick={() => setScopeMode('INHERITED')}><Workflow size={16} /><span><b>自动继承</b><small>沿用路由、Integration 或 Connector 配置</small></span></button>
        <button type="button" className={scopeMode === 'SELECTED_PROJECTS' ? 'selected' : ''} onClick={() => setScopeMode('SELECTED_PROJECTS')}><FolderKanban size={16} /><span><b>指定项目</b><small>可同时选择一个或多个项目</small></span></button>
        <button type="button" className={scopeMode === 'ALL_PROJECTS' ? 'selected' : ''} onClick={() => setScopeMode('ALL_PROJECTS')}><Globe2 size={16} /><span><b>全租户共享</b><small>当前及以后创建的项目都可使用</small></span></button>
        <button type="button" className={scopeMode === 'UNASSIGNED' ? 'selected' : ''} onClick={() => setScopeMode('UNASSIGNED')}><LockKeyhole size={16} /><span><b>暂不开放</b><small>仅租户管理员可配置，不能执行</small></span></button>
      </div>{scopeMode === 'SELECTED_PROJECTS' && <fieldset className="project-checks"><legend>选择可用项目</legend>{projects.map((project) => <label key={project.id}><input type="checkbox" checked={projectIds.includes(project.id)} onChange={() => toggleProject(project.id)} /><span>{project.name}{project.is_default ? '（默认）' : ''}</span></label>)}</fieldset>}<div className="form-actions"><button className="primary-button" disabled={busy || (scopeMode === 'SELECTED_PROJECTS' && projectIds.length === 0)} onClick={saveScope}>{busy ? <Loader2 size={15} className="spin" /> : <Save size={15} />}保存可用范围</button></div></section>
      <section className="resource-scope-editor"><strong>环境覆盖</strong><label className="field-label" htmlFor="resource-environment-select"><span>目标环境</span><div className="select-wrap"><select id="resource-environment-select" value={environment} onChange={(e) => setEnvironment(e.target.value as typeof environment)}><option value="development">开发环境</option><option value="testing">测试环境</option><option value="production">生产环境</option></select><ChevronDown size={15} /></div></label><div className="form-actions">{resource.environment_source === 'RESOURCE' && <button className="secondary-button" disabled={busy} onClick={restoreEnvironment}><RotateCcw size={15} />恢复环境继承</button>}<button className="primary-button" disabled={busy} onClick={saveEnvironment}>{busy ? <Loader2 size={15} className="spin" /> : <Save size={15} />}保存环境</button></div></section>
      <label className="field-label" htmlFor="resource-project-reason"><span>调整原因（选填）</span><textarea id="resource-project-reason" value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} placeholder="例如：该主机上的订单服务归属于交易项目" /></label>
      <p className="resource-project-hint">可用范围与环境相互独立；后续资源发现不会覆盖这里保存的手工配置。</p>
      {error && <div className="form-error" role="alert">{error}</div>}
    </div>
  </aside></>
}
