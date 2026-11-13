import { useEffect, useState } from 'react'
import { Check, Loader2 } from 'lucide-react'
import type { Integration } from '../../shared/api'
import { Badge } from './Badge'

export function JobScopePicker({ integration, jobs, acting, onSave }: { integration: Integration; jobs: string[]; acting: boolean; onSave: (scope: { allowAll?: boolean; allowedJobs?: string[] }) => void }) {
  const allowedJobs = (integration.scope.allowedJobs as string[] | undefined) ?? []
  const allowAll = integration.scope.allowAll === true
  const [selected, setSelected] = useState<string[]>(allowAll ? jobs : allowedJobs)
  const [dirty, setDirty] = useState(false)
  useEffect(() => {
    setSelected(allowAll ? jobs : allowedJobs)
    setDirty(false)
  }, [integration.id, integration.updated_at, allowAll, jobs.join('\n')])

  const allSelected = jobs.length > 0 && selected.length === jobs.length
  const toggle = (job: string) => {
    setSelected((current) => current.includes(job) ? current.filter((j) => j !== job) : [...current, job])
    setDirty(true)
  }
  const selectAll = () => { setSelected([...jobs]); setDirty(true) }
  const clearAll = () => { setSelected([]); setDirty(true) }
  const submit = () => {
    const scope = allSelected ? { allowAll: true } : { allowedJobs: selected }
    onSave(scope)
  }

  if (jobs.length === 0) {
    return <div className="job-scope-picker"><div className="job-scope-head"><span className="job-scope-title">Job 范围</span>{allowAll && <Badge tone="success">全部允许</Badge>}</div><small className="job-scope-empty">Connector 尚未发现 Job。Connector 同步资源后这里会出现可勾选的 Job 列表。</small></div>
  }
  return <div className="job-scope-picker"><div className="job-scope-head"><span className="job-scope-title">Job 范围</span>{allSelected && <Badge tone="success">全部允许</Badge>}<button className="quiet-button" type="button" onClick={selectAll} disabled={acting}>全选</button><button className="quiet-button" type="button" onClick={clearAll} disabled={acting}>清空</button></div><div className="job-scope-list">{jobs.map((job) => <label key={job}><input type="checkbox" checked={selected.includes(job)} onChange={() => toggle(job)} disabled={acting} /><span>{job}</span></label>)}</div>{dirty && <button className="primary-button" style={{ marginTop: 8 }} disabled={acting || selected.length === 0} onClick={submit}>{acting ? <Loader2 size={14} className="spin" /> : <Check size={14} />}保存范围</button>}</div>
}
