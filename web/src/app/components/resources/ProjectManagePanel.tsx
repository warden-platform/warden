import { useEffect, useMemo, useState } from 'react'
import { ChevronDown, FolderKanban, Loader2, Plus, Trash2, X } from 'lucide-react'
import type { ApiConnector, ApiError, ApiProject, ApiProjectNamespaceRule, ApiResource } from '../../../shared/api'
import {
  createProject,
  deleteProject,
  deleteProjectNamespaceRule,
  listProjectNamespaceRules,
  setConnectorEnvironment,
  setConnectorProject,
  updateProject,
  upsertProjectNamespaceRule,
} from '../../../shared/api'
import { connectorTypeLabel, mapEnvironment } from '../../../shared/types'
import { useApp } from '../Toast'

// Project assignment drawer: manage projects, connector-level assignment and
// per-namespace override rules. Effective resource project =
// namespace rule > connector project > default project.
export function ProjectManagePanel({
  projects,
  connectors,
  namespacesByConnector,
  resources,
  onChanged,
  onClose,
}: {
  projects: ApiProject[]
  connectors: ApiConnector[]
  namespacesByConnector: Map<string, string[]>
  resources: ApiResource[]
  onChanged: () => void
  onClose: () => void
}) {
  const { notify } = useApp()
  const [rules, setRules] = useState<ApiProjectNamespaceRule[]>([])
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [newDesc, setNewDesc] = useState('')
  const [editingId, setEditingId] = useState('')
  const [editName, setEditName] = useState('')
  const [busy, setBusy] = useState('')
  const [ruleConnector, setRuleConnector] = useState('')
  const [ruleNamespace, setRuleNamespace] = useState('')
  const [ruleProject, setRuleProject] = useState('')

  const loadRules = () => {
    listProjectNamespaceRules()
      .then((items) => setRules(items ?? []))
      .catch(() => setRules([]))
  }
  useEffect(loadRules, [])

  const projectName = useMemo(() => (id: string) => projects.find((p) => p.id === id)?.name ?? id.slice(0, 8), [projects])
  const connectorName = useMemo(() => (id: string) => connectors.find((c) => c.id === id)?.name ?? id.slice(0, 8), [connectors])
  const defaultProject = projects.find((p) => p.is_default)
  const k8sConnectors = connectors.filter((c) => c.connector_type === 'kubernetes')
  const ruleNamespaceOptions = ruleConnector ? (namespacesByConnector.get(ruleConnector) ?? []) : []
  const coveredProjects = useMemo(() => {
    const result = new Map<string, string[]>()
    for (const connector of connectors) {
      const names = [
        ...new Set(
          resources
            .filter((r) => r.connector_id === connector.id)
            .map((r) => r.project_name)
            .filter(Boolean) as string[],
        ),
      ]
      result.set(connector.id, names.sort())
    }
    return result
  }, [connectors, resources])

  const submitCreate = () => {
    if (!newName.trim()) return notify('请填写项目名称')
    setBusy('create')
    createProject({ name: newName.trim(), description: newDesc.trim() })
      .then(() => {
        setNewName('')
        setNewDesc('')
        setCreating(false)
        notify('项目已创建')
        onChanged()
      })
      .catch((e: ApiError) => notify(e.message || '创建项目失败'))
      .finally(() => setBusy(''))
  }

  const submitRename = (project: ApiProject) => {
    if (!editName.trim()) return notify('请填写项目名称')
    setBusy(project.id)
    updateProject(project.id, { name: editName.trim(), description: project.description })
      .then(() => {
        setEditingId('')
        notify('项目已更新')
        onChanged()
      })
      .catch((e: ApiError) => notify(e.message || '更新项目失败'))
      .finally(() => setBusy(''))
  }

  const submitDelete = (project: ApiProject) => {
    if (!window.confirm(`确认删除项目「${project.name}」？其下资源会回落到${defaultProject?.name ?? '默认项目'}。`)) return
    setBusy(project.id)
    deleteProject(project.id)
      .then(() => {
        notify('项目已删除')
        loadRules()
        onChanged()
      })
      .catch((e: ApiError) => notify(e.message || '删除项目失败'))
      .finally(() => setBusy(''))
  }

  const changeConnectorProject = (connector: ApiConnector, projectId: string) => {
    setBusy(connector.id)
    setConnectorProject(connector.id, projectId)
      .then(() => {
        notify(`「${connector.name}」的资源已划入${projectId ? projectName(projectId) : (defaultProject?.name ?? '默认项目')}`)
        onChanged()
      })
      .catch((e: ApiError) => notify(e.message || '更新归属失败'))
      .finally(() => setBusy(''))
  }

  const changeConnectorEnvironment = (connector: ApiConnector, environment: 'development' | 'testing' | 'production') => {
    setBusy(connector.id)
    setConnectorEnvironment(connector.id, environment)
      .then(() => {
        notify(`「${connector.name}」已设置为${mapEnvironment(environment)}环境`)
        onChanged()
      })
      .catch((e: ApiError) => notify(e.message || '更新环境失败'))
      .finally(() => setBusy(''))
  }

  const submitRule = () => {
    if (!ruleConnector || !ruleNamespace.trim() || !ruleProject) return notify('请选择执行通道、命名空间和项目')
    setBusy('rule')
    upsertProjectNamespaceRule({ project_id: ruleProject, connector_id: ruleConnector, namespace: ruleNamespace.trim() })
      .then(() => {
        setRuleNamespace('')
        notify('命名空间归属规则已保存')
        loadRules()
        onChanged()
      })
      .catch((e: ApiError) => notify(e.message || '保存规则失败'))
      .finally(() => setBusy(''))
  }

  const removeRule = (rule: ApiProjectNamespaceRule) => {
    setBusy(rule.id)
    deleteProjectNamespaceRule(rule.id)
      .then(() => {
        notify('规则已删除，命名空间回落到通道所属项目')
        loadRules()
        onChanged()
      })
      .catch((e: ApiError) => notify(e.message || '删除规则失败'))
      .finally(() => setBusy(''))
  }

  return (
    <>
      <button className="drawer-scrim" aria-label="关闭项目管理" onClick={onClose} />
      <aside className="drawer project-drawer" aria-label="项目划分管理">
        <div className="drawer-header">
          <div>
            <span className="eyebrow">
              <FolderKanban size={15} />
              项目划分
            </span>
            <h2>默认归属与细粒度覆盖</h2>
            <p>执行通道设置兜底项目，资源可通过 Namespace、Integration 或单资源规则覆盖，因此一个集群可以承载多个项目。</p>
          </div>
          <button className="icon-button" onClick={onClose} aria-label="关闭">
            <X size={20} />
          </button>
        </div>

        <section className="project-section">
          <header>
            <strong>项目</strong>
            {!creating && (
              <button className="quiet-button" onClick={() => setCreating(true)}>
                <Plus size={15} />
                新建项目
              </button>
            )}
          </header>
          {creating && (
            <div className="project-create-row">
              <input
                autoFocus
                value={newName}
                maxLength={100}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="项目名称，如 multica-bot"
              />
              <input value={newDesc} maxLength={500} onChange={(e) => setNewDesc(e.target.value)} placeholder="描述（可选）" />
              <button className="primary-button" disabled={busy === 'create'} onClick={submitCreate}>
                {busy === 'create' ? <Loader2 size={15} className="spin" /> : <Plus size={15} />}创建
              </button>
              <button className="quiet-button" onClick={() => setCreating(false)}>
                取消
              </button>
            </div>
          )}
          <ul className="project-list">
            {projects.map((project) => (
              <li key={project.id}>
                {editingId === project.id ? (
                  <div className="project-create-row">
                    <input autoFocus value={editName} maxLength={100} onChange={(e) => setEditName(e.target.value)} />
                    <button className="primary-button" disabled={busy === project.id} onClick={() => submitRename(project)}>
                      保存
                    </button>
                    <button className="quiet-button" onClick={() => setEditingId('')}>
                      取消
                    </button>
                  </div>
                ) : (
                  <>
                    <div>
                      <span className="project-name-row">
                        <strong>{project.name}</strong>
                        {project.is_default && <span className="badge badge-info">默认</span>}
                      </span>
                      <small>{project.description || (project.is_default ? '未划分项目的资源自动归入' : '')}</small>
                    </div>
                    {!project.is_default && (
                      <div className="project-item-actions">
                        <button
                          className="quiet-button"
                          onClick={() => {
                            setEditingId(project.id)
                            setEditName(project.name)
                          }}
                        >
                          重命名
                        </button>
                        <button className="quiet-button danger" disabled={busy === project.id} onClick={() => submitDelete(project)}>
                          <Trash2 size={14} />
                          删除
                        </button>
                      </div>
                    )}
                  </>
                )}
              </li>
            ))}
          </ul>
        </section>

        <section className="project-section">
          <header>
            <strong>执行通道归属</strong>
            <small>为通道设置项目和环境；只影响未被 Namespace、Integration 或单资源规则覆盖的资源</small>
          </header>
          {connectors.length === 0 ? (
            <p className="empty-hint">暂无执行通道。</p>
          ) : (
            <ul className="project-assign-list">
              {connectors.map((connector) => (
                <li key={connector.id}>
                  <div>
                    <strong>{connector.name}</strong>
                    <small>{connectorTypeLabel(connector.connector_type)}</small>
                    {(coveredProjects.get(connector.id)?.length ?? 0) > 0 && (
                      <div className="covered-project-list">
                        <span>实际覆盖</span>
                        {coveredProjects.get(connector.id)?.map((name) => (
                          <em key={name}>{name}</em>
                        ))}
                      </div>
                    )}
                  </div>
                  <div className="connector-governance-selects">
                    <label>
                      <span>项目</span>
                      <div className="select-wrap">
                        <select
                          value={connector.project_id ?? ''}
                          disabled={busy === connector.id}
                          onChange={(e) => changeConnectorProject(connector, e.target.value)}
                        >
                          <option value="">{defaultProject?.name ?? '默认项目'}</option>
                          {projects
                            .filter((p) => !p.is_default)
                            .map((p) => (
                              <option key={p.id} value={p.id}>
                                {p.name}
                              </option>
                            ))}
                        </select>
                        <ChevronDown size={15} />
                      </div>
                    </label>
                    <label>
                      <span>环境</span>
                      <div className="select-wrap">
                        <select
                          value={connector.environment}
                          disabled={busy === connector.id}
                          onChange={(e) =>
                            changeConnectorEnvironment(connector, e.target.value as 'development' | 'testing' | 'production')
                          }
                        >
                          <option value="development">开发</option>
                          <option value="testing">测试</option>
                          <option value="production">生产</option>
                        </select>
                        <ChevronDown size={15} />
                      </div>
                    </label>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className="project-section">
          <header>
            <strong>命名空间覆盖规则</strong>
            <small>同一集群中，将个别命名空间划到其他项目（优先级高于通道归属）</small>
          </header>
          {rules.length > 0 && (
            <ul className="project-rule-list">
              {rules.map((rule) => (
                <li key={rule.id}>
                  <div>
                    <code>{connectorName(rule.connector_id)}</code>
                    <span>/</span>
                    <code>{rule.namespace}</code>
                    <span>→</span>
                    <strong>{projectName(rule.project_id)}</strong>
                  </div>
                  <button className="icon-button" aria-label="删除规则" disabled={busy === rule.id} onClick={() => removeRule(rule)}>
                    <Trash2 size={15} />
                  </button>
                </li>
              ))}
            </ul>
          )}
          {k8sConnectors.length === 0 ? (
            <p className="empty-hint">接入 Kubernetes 执行通道后可配置命名空间级归属。</p>
          ) : (
            <div className="project-rule-form">
              <div className="select-wrap">
                <select
                  value={ruleConnector}
                  onChange={(e) => {
                    setRuleConnector(e.target.value)
                    setRuleNamespace('')
                  }}
                >
                  <option value="">选择执行通道</option>
                  {k8sConnectors.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                </select>
                <ChevronDown size={15} />
              </div>
              <input
                list="project-rule-ns-options"
                value={ruleNamespace}
                onChange={(e) => setRuleNamespace(e.target.value)}
                placeholder="命名空间"
              />
              <datalist id="project-rule-ns-options">
                {ruleNamespaceOptions.map((ns) => (
                  <option key={ns} value={ns} />
                ))}
              </datalist>
              <div className="select-wrap">
                <select value={ruleProject} onChange={(e) => setRuleProject(e.target.value)}>
                  <option value="">归入项目</option>
                  {projects.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
                <ChevronDown size={15} />
              </div>
              <button className="secondary-button" disabled={busy === 'rule'} onClick={submitRule}>
                {busy === 'rule' ? <Loader2 size={15} className="spin" /> : <Plus size={15} />}添加
              </button>
            </div>
          )}
        </section>
      </aside>
    </>
  )
}
