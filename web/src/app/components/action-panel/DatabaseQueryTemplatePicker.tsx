import { type Dispatch, type SetStateAction } from 'react'
import { ChevronDown, Loader2 } from 'lucide-react'
import type { ApiQueryTemplate, ApiResource } from '../../../shared/api'
import { listQueryTemplates } from '../../../shared/api'
import { asRecord } from './utils'
import { EMPTY_ARRAY, useApiQuery } from '../../lib/useApiQuery'

// 已发布查询模板选择器：读取数据库集成下 PUBLISHED 的模板，
// 绑定模板 ID/版本与运行参数（bindings）。

function parseTemplateBinding(value: string): unknown {
  const trimmed = value.trim()
  if (!trimmed) return ''
  try {
    return JSON.parse(trimmed)
  } catch {
    return value
  }
}

function formatTemplateBinding(value: unknown): string {
  if (value === undefined || value === null) return ''
  return typeof value === 'string' ? value : JSON.stringify(value)
}

export function DatabaseQueryTemplatePicker({
  resource,
  parameters,
  setParameters,
}: {
  resource: ApiResource | null
  parameters: Record<string, unknown>
  setParameters: Dispatch<SetStateAction<Record<string, unknown>>>
}) {
  const templatesQuery = useApiQuery(
    async () => {
      if (!resource?.integration_id) return [] as ApiQueryTemplate[]
      const items = await listQueryTemplates(resource.integration_id, 'PUBLISHED')
      return (items ?? []).filter((item) => !item.resource_bindings?.length || item.resource_bindings.includes(resource.external_id))
    },
    [resource?.id, resource?.integration_id, resource?.external_id],
    { enabled: Boolean(resource?.integration_id) },
  )
  const templates = templatesQuery.data ?? EMPTY_ARRAY
  const loading = templatesQuery.loading
  const error = templatesQuery.error

  const selected = templates.find(
    (item) => item.id === String(parameters.templateId ?? '') && item.version === Number(parameters.templateVersion ?? 0),
  )
  const bindingProperties = asRecord(selected?.parameter_schema?.properties)
  const bindings = asRecord(parameters.bindings)
  const chooseTemplate = (value: string) => {
    const [id, versionText] = value.split('@')
    const template = templates.find((item) => item.id === id && item.version === Number(versionText))
    setParameters((current) =>
      template
        ? { ...current, templateId: template.id, templateVersion: template.version, bindings: {} }
        : (() => {
            const next = { ...current }
            delete next.templateId
            delete next.templateVersion
            delete next.bindings
            return next
          })(),
    )
  }
  const updateBinding = (name: string, value: string) =>
    setParameters((current) => ({
      ...current,
      bindings: { ...asRecord(current.bindings), [name]: parseTemplateBinding(value) },
    }))

  return (
    <fieldset className="database-query-builder">
      <legend>已发布查询模板</legend>
      {!resource?.integration_id ? (
        <div className="form-error" role="alert">
          当前表未关联数据库集成，无法选择模板。
        </div>
      ) : loading ? (
        <div className="onboarding-hint">
          <Loader2 size={16} className="spin" />
          <span>正在读取已发布模板…</span>
        </div>
      ) : error ? (
        <div className="form-error" role="alert">
          {error}
        </div>
      ) : (
        <>
          <label>
            <span>查询模板</span>
            <div className="select-wrap full">
              <select
                value={selected ? `${selected.id}@${selected.version}` : ''}
                onChange={(event) => chooseTemplate(event.target.value)}
                required
              >
                <option value="">选择已发布模板</option>
                {templates.map((template) => (
                  <option key={`${template.id}@${template.version}`} value={`${template.id}@${template.version}`}>
                    {template.name} · v{template.version}
                  </option>
                ))}
              </select>
              <ChevronDown size={15} />
            </div>
          </label>
          {templates.length === 0 && (
            <div className="onboarding-hint">
              当前数据库没有适用于该表的已发布模板。模板需由管理员通过 SQL 导入生成草稿，并完成发布审批。
            </div>
          )}
          {selected && (
            <>
              <div className="database-builder-meta">
                模板 ID <code>{selected.id}</code> · 名称 {selected.name} · v{selected.version} · 最多 {selected.max_rows} 行 · 超时{' '}
                {selected.timeout_seconds}s{selected.owner ? ` · Owner ${selected.owner}` : ''}
              </div>
              {Object.keys(bindingProperties).length > 0 ? (
                <div className="database-builder-filters">
                  <div className="database-builder-filter-head">
                    <strong>运行参数</strong>
                    <span>仅参数值可变，表、JOIN 与返回字段由模板固定</span>
                  </div>
                  {Object.keys(bindingProperties).map((name) => (
                    <label key={name}>
                      <span>{name}</span>
                      <input
                        value={formatTemplateBinding(bindings[name])}
                        onChange={(event) => updateBinding(name, event.target.value)}
                        required
                      />
                    </label>
                  ))}
                </div>
              ) : (
                <div className="onboarding-hint">该模板没有运行参数，可直接提交。</div>
              )}
            </>
          )}
        </>
      )}
    </fieldset>
  )
}
