import { useEffect, useState } from 'react'
import { Check, Loader2 } from 'lucide-react'
import type { Integration } from '../../shared/api'
import { Badge } from './Badge'

// Dashboard scope picker for Grafana integrations. Mirrors JobScopePicker:
// Broad access is represented explicitly with allowAll.
export function DashboardScopePicker({
  integration,
  dashboards,
  acting,
  onSave,
}: {
  integration: Integration
  dashboards: { uid: string; title: string }[]
  acting: boolean
  onSave: (scope: { allowAll?: boolean; allowedDashboards?: string[] }) => void
}) {
  const allowedDashboards = (integration.scope.allowedDashboards as string[] | undefined) ?? []
  const allowAll = integration.scope.allowAll === true
  const uids = dashboards.map((d) => d.uid)
  const [selected, setSelected] = useState<string[]>(allowAll ? uids : allowedDashboards)
  const [dirty, setDirty] = useState(false)
  useEffect(() => {
    setSelected(allowAll ? uids : allowedDashboards)
    setDirty(false)
  }, [integration.id, integration.updated_at, allowAll, uids.join('\n')])

  const allSelected = dashboards.length > 0 && selected.length === dashboards.length
  const toggle = (uid: string) => {
    setSelected((current) => (current.includes(uid) ? current.filter((u) => u !== uid) : [...current, uid]))
    setDirty(true)
  }
  const selectAll = () => {
    setSelected([...uids])
    setDirty(true)
  }
  const clearAll = () => {
    setSelected([])
    setDirty(true)
  }
  const submit = () => {
    const scope = allSelected ? { allowAll: true } : { allowedDashboards: selected }
    onSave(scope)
  }

  if (dashboards.length === 0) {
    return (
      <div className="job-scope-picker">
        <div className="job-scope-head">
          <span className="job-scope-title">Dashboard 范围</span>
          {allowAll && <Badge tone="success">全部允许</Badge>}
        </div>
        <small className="job-scope-empty">Connector 尚未发现 Dashboard。Connector 同步资源后这里会出现可勾选的 Dashboard 列表。</small>
      </div>
    )
  }
  return (
    <div className="job-scope-picker">
      <div className="job-scope-head">
        <span className="job-scope-title">Dashboard 范围</span>
        {allSelected && <Badge tone="success">全部允许</Badge>}
        <button className="quiet-button" type="button" onClick={selectAll} disabled={acting}>
          全选
        </button>
        <button className="quiet-button" type="button" onClick={clearAll} disabled={acting}>
          清空
        </button>
      </div>
      <div className="job-scope-list">
        {dashboards.map((d) => (
          <label key={d.uid}>
            <input type="checkbox" checked={selected.includes(d.uid)} onChange={() => toggle(d.uid)} disabled={acting} />
            <span>{d.title || d.uid}</span>
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
