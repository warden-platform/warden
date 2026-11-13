import { useEffect, useState } from 'react'
import { Check, Loader2 } from 'lucide-react'
import type { Integration } from '../../shared/api'
import { Badge } from './Badge'

const HUAWEI_SERVICES: { key: string; label: string }[] = [
  { key: 'ecs', label: 'ECS 弹性云服务器' },
  { key: 'rds', label: 'RDS 云数据库' },
  { key: 'elb', label: 'ELB 弹性负载均衡' },
  { key: 'obs', label: 'OBS 对象存储' },
  { key: 'lts', label: 'LTS 日志服务' },
  { key: 'swr', label: 'SWR 企业版镜像仓库' },
]

// Service-level scope picker for the huaweicloud protocol. Unlike jobs or
// schemas the candidate list is fixed. The cloud-side
// IAM policy remains the hard ceiling; this narrows what PAM even probes.
export function HuaweiServiceScopePicker({ integration, acting, onSave }: {
  integration: Integration
  acting: boolean
  onSave: (scope: { allowAll?: boolean; allowedServices?: string[]; regionPolicy?: { mode: 'single' | 'all' | 'allowlist'; regions: string[]; deniedRegions: string[] } }) => void
}) {
  const allowedServices = (integration.scope.allowedServices as string[] | undefined) ?? []
  const allowAll = integration.scope.allowAll === true
  const allKeys = HUAWEI_SERVICES.map((s) => s.key)
  const [selected, setSelected] = useState<string[]>(allowAll ? allKeys : allowedServices)
  const [dirty, setDirty] = useState(false)
  const initialPolicy = (integration.scope.regionPolicy as { mode?: 'single' | 'all' | 'allowlist'; regions?: string[]; deniedRegions?: string[] } | undefined)
  const [regionMode, setRegionMode] = useState<'single' | 'all' | 'allowlist'>(initialPolicy?.mode ?? 'single')
  const [regions, setRegions] = useState((initialPolicy?.regions ?? []).join(', '))
  const [deniedRegions, setDeniedRegions] = useState((initialPolicy?.deniedRegions ?? []).join(', '))
  useEffect(() => {
    setSelected(allowAll ? allKeys : allowedServices)
    const policy = (integration.scope.regionPolicy as { mode?: 'single' | 'all' | 'allowlist'; regions?: string[]; deniedRegions?: string[] } | undefined)
    setRegionMode(policy?.mode ?? 'single')
    setRegions((policy?.regions ?? []).join(', '))
    setDeniedRegions((policy?.deniedRegions ?? []).join(', '))
    setDirty(false)
  }, [integration.id, integration.updated_at, allowAll])

  const allSelected = selected.length === allKeys.length
  const toggle = (key: string) => {
    setSelected((current) => current.includes(key) ? current.filter((item) => item !== key) : [...current, key])
    setDirty(true)
  }
  const parseRegions = (value: string) => value.split(/[\s,]+/).map((item) => item.trim()).filter(Boolean)
  const submit = () => onSave({
    ...(allSelected ? { allowAll: true } : { allowedServices: selected }),
    regionPolicy: { mode: regionMode, regions: parseRegions(regions), deniedRegions: parseRegions(deniedRegions) },
  })

  return <div className="job-scope-picker"><div className="job-scope-head"><span className="job-scope-title">云服务范围</span>{allSelected && <Badge tone="success">全部允许</Badge>}<button className="quiet-button" type="button" onClick={() => { setSelected([...allKeys]); setDirty(true) }} disabled={acting}>全选</button><button className="quiet-button" type="button" onClick={() => { setSelected([]); setDirty(true) }} disabled={acting}>清空</button></div><div className="job-scope-list">{HUAWEI_SERVICES.map((service) => <label key={service.key}><input type="checkbox" checked={selected.includes(service.key)} onChange={() => toggle(service.key)} disabled={acting} /><span>{service.label}</span></label>)}</div><small className="job-scope-empty">范围之外的服务不会被探测和发现；账号 IAM 无权限的服务即使在范围内也不会出现资源。</small><fieldset className="scope-picker"><legend>Region 范围</legend><label><input type="radio" name="region-mode" checked={regionMode === 'all'} onChange={() => { setRegionMode('all'); setDirty(true) }} /><span><strong>全部可访问 Region</strong><small>自动从 IAM 项目列表发现，包括上海、广州、香港等账号有权限的区域。</small></span></label><label><input type="radio" name="region-mode" checked={regionMode === 'allowlist'} onChange={() => { setRegionMode('allowlist'); setDirty(true) }} /><span><strong>仅指定 Region</strong><small>只探测允许列表。</small></span></label><label><input type="radio" name="region-mode" checked={regionMode === 'single'} onChange={() => { setRegionMode('single'); setDirty(true) }} /><span><strong>兼容单 Region</strong><small>仅使用 Integration Endpoint 中的种子 Region。</small></span></label>{regionMode === 'allowlist' && <label><span><strong>允许 Region（逗号分隔）</strong><input value={regions} placeholder="cn-east-3, cn-south-1" onChange={(event) => { setRegions(event.target.value); setDirty(true) }} /></span></label>}{regionMode === 'all' && <label><span><strong>排除 Region（选填）</strong><input value={deniedRegions} placeholder="cn-north-9" onChange={(event) => { setDeniedRegions(event.target.value); setDirty(true) }} /></span></label>}</fieldset>{dirty && <button className="primary-button" style={{ marginTop: 8 }} disabled={acting || selected.length === 0 || (regionMode === 'allowlist' && !regions.trim())} onClick={submit}>{acting ? <Loader2 size={14} className="spin" /> : <Check size={14} />}保存范围</button>}</div>
}
