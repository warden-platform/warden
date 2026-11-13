import { useEffect, useState } from 'react'
import { Check, Loader2 } from 'lucide-react'
import type { Integration } from '../../shared/api'
import { Badge } from './Badge'

export function SchemaScopePicker({ integration, schemas, acting, onSave }: {
  integration: Integration
  schemas: string[]
  acting: boolean
  onSave: (scope: { allowAll?: boolean; allowedSchemas?: string[] }) => void
}) {
  const allowedSchemas = (integration.scope.allowedSchemas as string[] | undefined) ?? []
  const allowAll = integration.scope.allowAll === true
  const [selected, setSelected] = useState<string[]>(allowAll ? schemas : allowedSchemas)
  const [dirty, setDirty] = useState(false)
  useEffect(() => {
    setSelected(allowAll ? schemas : allowedSchemas)
    setDirty(false)
  }, [integration.id, integration.updated_at, allowAll, schemas.join('\n')])

  const allSelected = schemas.length > 0 && selected.length === schemas.length
  const toggle = (schema: string) => {
    setSelected((current) => current.includes(schema) ? current.filter((item) => item !== schema) : [...current, schema])
    setDirty(true)
  }
  const submit = () => onSave(allSelected ? { allowAll: true } : { allowedSchemas: selected })

  if (schemas.length === 0) {
    return <div className="job-scope-picker"><div className="job-scope-head"><span className="job-scope-title">Schema 范围</span>{allowAll && <Badge tone="success">全部允许</Badge>}</div><small className="job-scope-empty">Connector 尚未发现 Schema。连接成功并同步后可在这里缩小范围。</small></div>
  }
  return <div className="job-scope-picker"><div className="job-scope-head"><span className="job-scope-title">Schema 范围</span>{allSelected && <Badge tone="success">全部允许</Badge>}<button className="quiet-button" type="button" onClick={() => { setSelected([...schemas]); setDirty(true) }} disabled={acting}>全选</button><button className="quiet-button" type="button" onClick={() => { setSelected([]); setDirty(true) }} disabled={acting}>清空</button></div><div className="job-scope-list">{schemas.map((schema) => <label key={schema}><input type="checkbox" checked={selected.includes(schema)} onChange={() => toggle(schema)} disabled={acting} /><span>{schema}</span></label>)}</div>{dirty && <button className="primary-button" style={{ marginTop: 8 }} disabled={acting || selected.length === 0} onClick={submit}>{acting ? <Loader2 size={14} className="spin" /> : <Check size={14} />}保存范围</button>}</div>
}
