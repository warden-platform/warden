import { useState } from 'react'
import { Check, Loader2 } from 'lucide-react'
import type { Integration } from '../../shared/api'
import { Badge } from './Badge'

export function JobScopePicker({
  integration,
  jobs,
  acting,
  onSave,
}: {
  integration: Integration
  jobs: string[]
  acting: boolean
  onSave: (scope: { allowAll?: boolean; allowedJobs?: string[] }) => void
}) {
  const allowedJobs = (integration.scope.allowedJobs as string[] | undefined) ?? []
  const allowAll = integration.scope.allowAll === true
  // Keyed draft: edits persist until the server scope changes (id/updated_at),
  // no sync effect needed.
  const scopeKey = `${integration.id}:${integration.updated_at}`
  const serverSelected = allowAll ? jobs : allowedJobs
  const [draft, setDraft] = useState<{ key: string; value: string[] } | null>(null)
  const selected = draft && draft.key === scopeKey ? draft.value : serverSelected
  const dirty = draft !== null && draft.key === scopeKey
  const edit = (value: string[]) => setDraft({ key: scopeKey, value })

  const allSelected = jobs.length > 0 && selected.length === jobs.length
  const toggle = (job: string) => {
    edit(selected.includes(job) ? selected.filter((j) => j !== job) : [...selected, job])
  }
  const selectAll = () => {
    edit([...jobs])
  }
  const clearAll = () => {
    edit([])
  }
  const submit = () => {
    const scope = allSelected ? { allowAll: true } : { allowedJobs: selected }
    onSave(scope)
  }

  if (jobs.length === 0) {
    return (
      <div className="job-scope-picker">
        <div className="job-scope-head">
          <span className="job-scope-title">Job 范围</span>
          {allowAll && <Badge tone="success">全部允许</Badge>}
        </div>
        <small className="job-scope-empty">Connector 尚未发现 Job。Connector 同步资源后这里会出现可勾选的 Job 列表。</small>
      </div>
    )
  }
  return (
    <div className="job-scope-picker">
      <div className="job-scope-head">
        <span className="job-scope-title">Job 范围</span>
        {allSelected && <Badge tone="success">全部允许</Badge>}
        <button className="quiet-button" type="button" onClick={selectAll} disabled={acting}>
          全选
        </button>
        <button className="quiet-button" type="button" onClick={clearAll} disabled={acting}>
          清空
        </button>
      </div>
      <div className="job-scope-list">
        {jobs.map((job) => (
          <label key={job}>
            <input type="checkbox" checked={selected.includes(job)} onChange={() => toggle(job)} disabled={acting} />
            <span>{job}</span>
          </label>
        ))}
      </div>
      {dirty && (
        <button className="primary-button" style={{ marginTop: 8 }} disabled={acting || selected.length === 0} onClick={submit}>
          {acting ? <Loader2 size={14} className="spin" /> : <Check size={14} />}保存范围
        </button>
      )}
    </div>
  )
}
