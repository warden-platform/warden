import { useEffect, useState } from 'react'
import { Check, FilePlus2, Loader2, Send, XCircle } from 'lucide-react'
import type { ApiError, ApiQueryTemplate, ApiResource, ApiTemplatePublishRequest, Integration } from '../../shared/api'
import {
  approveTemplatePublish,
  importQueryTemplate,
  listQueryTemplates,
  listTemplatePublishRequests,
  rejectTemplatePublish,
  submitQueryTemplatePublish,
} from '../../shared/api'
import { Badge } from './Badge'
import { useApp } from './Toast'

export function DatabaseQueryTemplatesPanel({ integration, resources }: { integration: Integration; resources: ApiResource[] }) {
  const { notify } = useApp()
  const [templates, setTemplates] = useState<ApiQueryTemplate[]>([])
  const [requests, setRequests] = useState<ApiTemplatePublishRequest[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [acting, setActing] = useState('')
  const [creating, setCreating] = useState(false)
  const schemas = resources.filter((resource) => resource.resource_type === 'database_schema' && resource.status === 'ACTIVE')
  const [form, setForm] = useState({ schemaResourceId: '', name: '', owner: '', sql: '' })

  const load = () => {
    setLoading(true)
    setError('')
    Promise.all([listQueryTemplates(integration.id, ''), listTemplatePublishRequests()])
      .then(([templateItems, requestItems]) => {
        setTemplates(templateItems ?? [])
        const ids = new Set((templateItems ?? []).map((item) => item.id))
        setRequests((requestItems ?? []).filter((item) => ids.has(item.template_id)))
      })
      .catch((err: ApiError) => setError(err.message || '读取查询模板失败'))
      .finally(() => setLoading(false))
  }
  useEffect(load, [integration.id]) // eslint-disable-line react-hooks/exhaustive-deps

  const create = () => {
    if (!form.schemaResourceId || !form.name.trim() || !form.sql.trim()) return
    setActing('create')
    importQueryTemplate({
      integrationId: integration.id,
      schemaResourceId: form.schemaResourceId,
      name: form.name.trim(),
      sql: form.sql,
      owner: form.owner.trim(),
    })
      .then((template) => {
        notify(`模板草稿 ${template.name} v${template.version} 已生成，ID：${template.id}`)
        setForm({ schemaResourceId: '', name: '', owner: '', sql: '' })
        setCreating(false)
        load()
      })
      .catch((err: ApiError) => notify(`创建失败：${err.message}`, 'error'))
      .finally(() => setActing(''))
  }

  const submit = (template: ApiQueryTemplate) => {
    setActing(template.id)
    submitQueryTemplatePublish(template.id)
      .then(() => {
        notify('已发起模板发布审批')
        load()
      })
      .catch((err: ApiError) => notify(`提交失败：${err.message}`, 'error'))
      .finally(() => setActing(''))
  }
  const decide = (template: ApiQueryTemplate, request: ApiTemplatePublishRequest, approve: boolean) => {
    const reason = approve ? '' : window.prompt('请输入拒绝原因')
    if (!approve && reason === null) return
    setActing(request.id)
    const operation = approve
      ? approveTemplatePublish(template.id, request.id)
      : rejectTemplatePublish(template.id, request.id, reason ?? '')
    operation
      .then(() => {
        notify(approve ? '模板已发布，可在执行动作中选择' : '模板发布已拒绝')
        load()
      })
      .catch((err: ApiError) => notify(`审批失败：${err.message}`, 'error'))
      .finally(() => setActing(''))
  }

  if (loading)
    return (
      <div className="onboarding-hint">
        <Loader2 size={16} className="spin" />
        <span>正在读取查询模板…</span>
      </div>
    )
  return (
    <div className="detail-section">
      <div className="detail-section-head">
        <div>
          <h3>查询模板</h3>
          <small>SQL 仅用于导入并转换为受治理 AST；草稿通过发布审批后才会出现在“执行查询模板”的选择器中。</small>
        </div>
        <button className="primary-button compact" onClick={() => setCreating((value) => !value)}>
          <FilePlus2 size={16} />
          {creating ? '收起' : '导入 SQL 模板'}
        </button>
      </div>
      {error && (
        <div className="form-error" role="alert">
          {error}
          <button className="quiet-button" onClick={load}>
            重试
          </button>
        </div>
      )}
      {creating && (
        <div className="form-grid panel">
          <label>
            <span>默认 Schema</span>
            <select
              value={form.schemaResourceId}
              onChange={(event) => setForm((current) => ({ ...current, schemaResourceId: event.target.value }))}
            >
              <option value="">选择 Schema</option>
              {schemas.map((schema) => (
                <option key={schema.id} value={schema.external_id}>
                  {schema.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>模板名称</span>
            <input
              value={form.name}
              onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))}
              placeholder="例如 order_customer_summary"
            />
          </label>
          <label>
            <span>Owner</span>
            <input
              value={form.owner}
              onChange={(event) => setForm((current) => ({ ...current, owner: event.target.value }))}
              placeholder="团队或负责人"
            />
          </label>
          <label className="full-span">
            <span>只读 SELECT SQL</span>
            <textarea
              rows={8}
              value={form.sql}
              onChange={(event) => setForm((current) => ({ ...current, sql: event.target.value }))}
              placeholder="SELECT ... FROM orders o INNER JOIN customers c ON ... WHERE ..."
            />
          </label>
          <div className="full-span">
            <button
              className="primary-button"
              disabled={acting === 'create' || !form.schemaResourceId || !form.name.trim() || !form.sql.trim()}
              onClick={create}
            >
              {acting === 'create' ? <Loader2 size={16} className="spin" /> : <FilePlus2 size={16} />}生成模板草稿
            </button>
          </div>
        </div>
      )}
      {templates.length === 0 ? (
        <div className="empty-inline">尚未创建查询模板。</div>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>名称 / ID</th>
                <th>版本</th>
                <th>状态</th>
                <th>表范围</th>
                <th>参数</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {templates.map((template) => {
                const pending = requests.find((request) => request.template_id === template.id && request.status === 'PENDING')
                const parameterCount = Object.keys(
                  (template.parameter_schema?.properties as Record<string, unknown> | undefined) ?? {},
                ).length
                return (
                  <tr key={template.id}>
                    <td>
                      <strong>{template.name}</strong>
                      <small>
                        <code>{template.id}</code>
                      </small>
                    </td>
                    <td>v{template.version}</td>
                    <td>
                      <Badge tone={template.status === 'PUBLISHED' ? 'success' : pending ? 'warning' : 'neutral'}>
                        {pending ? '待发布审批' : template.status}
                      </Badge>
                    </td>
                    <td>{template.resource_bindings?.length ?? 0} 张表</td>
                    <td>{parameterCount} 个</td>
                    <td>
                      {template.status === 'DRAFT' && !pending && (
                        <button className="quiet-button" disabled={acting === template.id} onClick={() => submit(template)}>
                          <Send size={14} />
                          发起发布
                        </button>
                      )}
                      {pending && (
                        <div className="header-actions">
                          <button
                            className="quiet-button"
                            disabled={acting === pending.id}
                            onClick={() => decide(template, pending, false)}
                          >
                            <XCircle size={14} />
                            拒绝
                          </button>
                          <button
                            className="primary-button compact"
                            disabled={acting === pending.id}
                            onClick={() => decide(template, pending, true)}
                          >
                            <Check size={14} />
                            批准发布
                          </button>
                        </div>
                      )}
                      {template.status === 'PUBLISHED' && <span>可执行</span>}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
