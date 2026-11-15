import { useEffect, useMemo, useState } from 'react'
import { Check, Loader2, Plus, Trash2 } from 'lucide-react'
import type { ApiError, ApiProject, Integration, IntegrationProjectRule } from '../../shared/api'
import { getIntegrationProjectScope, listProjects, updateIntegrationProjectScope } from '../../shared/api'

type DraftRule = IntegrationProjectRule & { localId: string }

export function IntegrationProjectScopePanel({
  integration,
  onSaved,
}: {
  integration: Integration
  onSaved: (integration: Integration, message: string) => void
}) {
  const [projects, setProjects] = useState<ApiProject[]>([])
  const [defaultProjectId, setDefaultProjectId] = useState(integration.project_id ?? '')
  const [environment, setEnvironment] = useState(integration.environment ?? '')
  const [projectIds, setProjectIds] = useState<string[]>(integration.project_ids ?? [])
  const [rules, setRules] = useState<DraftRule[]>([])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all([listProjects(), getIntegrationProjectScope(integration.id)])
      .then(([items, scope]) => {
        const nextProjects = items ?? []
        setProjects(nextProjects)
        setDefaultProjectId(scope.default_project_id || nextProjects.find((project) => project.is_default)?.id || '')
        setEnvironment(scope.environment ?? '')
        setProjectIds(scope.project_ids ?? [])
        setRules((scope.rules ?? []).map((rule, index) => ({ ...rule, localId: rule.id ?? `rule-${index}` })))
      })
      .catch((e: ApiError) => setError(e.message))
  }, [integration.id])

  const toggleAllowed = (id: string) =>
    setProjectIds((current) => (current.includes(id) ? current.filter((item) => item !== id) : [...current, id]))
  const save = () => {
    setSaving(true)
    setError('')
    updateIntegrationProjectScope(integration.id, {
      defaultProjectId,
      environment,
      projectIds,
      rules: rules.map(({ localId: _, ...rule }) => rule),
    })
      .then((updated) => onSaved(updated, '项目、环境归属与路由规则已更新'))
      .catch((e: ApiError) => setError(e.message))
      .finally(() => setSaving(false))
  }
  return (
    <div className="project-scope-panel">
      <section className="scope-card">
        <div className="scope-card-head">
          <div>
            <strong>默认项目与环境</strong>
            <span>中间件项目与执行通道相互独立；未单独覆盖的服务继承这里的项目和环境。</span>
          </div>
        </div>
        <label>
          <span>默认项目</span>
          <select value={defaultProjectId} onChange={(event) => setDefaultProjectId(event.target.value)}>
            <option value="" disabled>
              选择中间件默认项目
            </option>
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
                {project.is_default ? '（系统默认）' : ''}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>默认环境</span>
          <select value={environment} onChange={(event) => setEnvironment(event.target.value as typeof environment)}>
            <option value="">继承执行通道</option>
            <option value="development">开发环境</option>
            <option value="testing">测试环境</option>
            <option value="production">生产环境</option>
          </select>
        </label>
        <fieldset className="project-checks">
          <legend>允许服务项目</legend>
          {projects.map((project) => (
            <label key={project.id}>
              <input type="checkbox" checked={projectIds.includes(project.id)} onChange={() => toggleAllowed(project.id)} />
              <span>{project.name}</span>
            </label>
          ))}
        </fieldset>
      </section>

      {integration.protocol === 'jenkins' && (
        <section className="scope-card">
          <div className="scope-card-head">
            <div>
              <strong>Folder / Job 路径自动归属</strong>
              <span>
                前缀按优先级从小到大匹配；相同优先级时更长路径优先。例如 <code>order-service/</code>。
              </span>
            </div>
            <button
              className="secondary-button"
              type="button"
              onClick={() =>
                setRules((current) => [
                  ...current,
                  {
                    localId: crypto.randomUUID(),
                    matcher_type: 'JENKINS_PATH_PREFIX',
                    pattern: '',
                    project_id: projectIds[0] ?? '',
                    priority: 100,
                  },
                ])
              }
            >
              <Plus size={14} />
              添加规则
            </button>
          </div>
          <div className="routing-rules">
            {rules.length === 0 ? (
              <div className="empty-inline">暂无规则，Job 继承 Integration 默认项目</div>
            ) : (
              rules.map((rule) => (
                <div className="routing-rule" key={rule.localId}>
                  <label>
                    <span>路径前缀</span>
                    <input
                      value={rule.pattern}
                      placeholder="team-a/"
                      onChange={(event) =>
                        setRules((current) =>
                          current.map((item) => (item.localId === rule.localId ? { ...item, pattern: event.target.value } : item)),
                        )
                      }
                    />
                  </label>
                  <label>
                    <span>主归属项目</span>
                    <select
                      value={rule.project_id}
                      onChange={(event) =>
                        setRules((current) =>
                          current.map((item) => (item.localId === rule.localId ? { ...item, project_id: event.target.value } : item)),
                        )
                      }
                    >
                      <option value="">选择项目</option>
                      {projects
                        .filter((project) => projectIds.includes(project.id))
                        .map((project) => (
                          <option key={project.id} value={project.id}>
                            {project.name}
                          </option>
                        ))}
                    </select>
                  </label>
                  <label>
                    <span>优先级</span>
                    <input
                      type="number"
                      min={1}
                      max={10000}
                      value={rule.priority}
                      onChange={(event) =>
                        setRules((current) =>
                          current.map((item) => (item.localId === rule.localId ? { ...item, priority: Number(event.target.value) } : item)),
                        )
                      }
                    />
                  </label>
                  <button
                    className="icon-button"
                    aria-label="删除路由规则"
                    type="button"
                    onClick={() => setRules((current) => current.filter((item) => item.localId !== rule.localId))}
                  >
                    <Trash2 size={15} />
                  </button>
                </div>
              ))
            )}
          </div>
        </section>
      )}

      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div className="form-actions">
        <button
          className="primary-button"
          type="button"
          disabled={saving || !defaultProjectId || rules.some((rule) => !rule.pattern.trim() || !rule.project_id)}
          onClick={save}
        >
          {saving ? <Loader2 size={15} className="spin" /> : <Check size={15} />}保存项目与环境
        </button>
      </div>
    </div>
  )
}
