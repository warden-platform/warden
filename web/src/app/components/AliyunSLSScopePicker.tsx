import { useMemo, useState } from 'react'
import { Check, Loader2, MapPin } from 'lucide-react'
import type { ApiResource, Integration } from '../../shared/api'
import { Badge } from './Badge'
import { aliyunRegionLabel } from '../lib/labels'

type RegionPolicy = { mode: 'single' | 'all' | 'allowlist'; regions: string[]; deniedRegions: string[] }

export function AliyunSLSScopePicker({
  integration,
  resources,
  acting,
  onSave,
}: {
  integration: Integration
  resources: ApiResource[]
  acting: boolean
  onSave: (scope: { allowAll?: boolean; allowedProjects?: string[]; regionPolicy: RegionPolicy }) => void
}) {
  const candidates = useMemo(
    () =>
      resources
        .filter((resource) => resource.resource_type === 'aliyun_sls_account')
        .map((resource) => ({ name: resource.name, region: String(resource.metadata.region ?? '') }))
        .sort((a, b) => a.region.localeCompare(b.region) || a.name.localeCompare(b.name)),
    [resources],
  )
  const configured = (integration.scope.allowedProjects as string[] | undefined) ?? []
  const initialAllowAll = integration.scope.allowAll === true
  const initialPolicy = integration.scope.regionPolicy as Partial<RegionPolicy> | undefined
  // Keyed draft over the server scope; invalidated when the integration or
  // the discovered candidate list changes. No sync effect.
  const scopeKey = `${integration.id}:${integration.updated_at}`
  const serverState = {
    allowAll: initialAllowAll,
    selected: initialAllowAll ? candidates.map((item) => item.name) : configured,
    regionMode: (initialPolicy?.mode ?? 'single') as RegionPolicy['mode'],
    regions: (initialPolicy?.regions ?? []).join(', '),
    deniedRegions: (initialPolicy?.deniedRegions ?? []).join(', '),
  }
  const [draft, setDraft] = useState<({ key: string } & typeof serverState) | null>(null)
  const current = draft && draft.key === scopeKey ? draft : serverState
  const { allowAll, selected, regionMode, regions, deniedRegions } = current
  const dirty = draft !== null && draft.key === scopeKey
  const patch = (p: Partial<typeof serverState>) => setDraft({ key: scopeKey, ...current, ...p })

  const parseRegions = (value: string) => [
    ...new Set(
      value
        .split(/[\s,]+/)
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  ]
  const toggleProject = (name: string) => {
    patch({ allowAll: false, selected: selected.includes(name) ? selected.filter((item) => item !== name) : [...selected, name] })
  }
  const submit = () =>
    onSave({
      ...(allowAll ? { allowAll: true } : { allowedProjects: selected }),
      regionPolicy: { mode: regionMode, regions: parseRegions(regions), deniedRegions: parseRegions(deniedRegions) },
    })

  return (
    <div className="job-scope-picker">
      <div className="job-scope-head">
        <span className="job-scope-title">SLS 账号发现范围</span>
        {allowAll && <Badge tone="success">全部可访问 Project</Badge>}
        <button
          className="quiet-button"
          type="button"
          onClick={() => {
            patch({ allowAll: true, selected: candidates.map((item) => item.name) })
          }}
          disabled={acting}
        >
          允许全部
        </button>
        <button
          className="quiet-button"
          type="button"
          onClick={() => {
            patch({ allowAll: false, selected: [] })
          }}
          disabled={acting}
        >
          清空
        </button>
      </div>
      <small className="job-scope-empty">RAM 权限是硬边界；这里可以在账号权限之上进一步缩小 Warden 会发现和允许查询的 Project。</small>

      <fieldset className="scope-picker">
        <legend>Region 范围</legend>
        <label>
          <input
            type="radio"
            name={`aliyun-region-mode-${integration.id}`}
            checked={regionMode === 'all'}
            onChange={() => {
              patch({ regionMode: 'all' })
            }}
          />
          <span>
            <strong>全部可访问 Region</strong>
            <small>并发探测阿里云官方公网 Endpoint，只保留 RAM 实际有权列出的 Project。</small>
          </span>
        </label>
        <label>
          <input
            type="radio"
            name={`aliyun-region-mode-${integration.id}`}
            checked={regionMode === 'allowlist'}
            onChange={() => {
              patch({ regionMode: 'allowlist' })
            }}
          />
          <span>
            <strong>仅指定 Region</strong>
            <small>适合明确只接入杭州、上海、香港等区域。</small>
          </span>
        </label>
        <label>
          <input
            type="radio"
            name={`aliyun-region-mode-${integration.id}`}
            checked={regionMode === 'single'}
            onChange={() => {
              patch({ regionMode: 'single' })
            }}
          />
          <span>
            <strong>兼容单 Region</strong>
            <small>只使用设置中的种子 Region；旧项目级 Endpoint 保持原有单 Project 行为。</small>
          </span>
        </label>
        {regionMode === 'allowlist' && (
          <label>
            <span>
              <strong>允许 Region（逗号分隔）</strong>
              <input
                value={regions}
                placeholder="cn-hangzhou, cn-shanghai, cn-hongkong"
                onChange={(event) => {
                  patch({ regions: event.target.value })
                }}
              />
            </span>
          </label>
        )}
        {regionMode === 'all' && (
          <label>
            <span>
              <strong>排除 Region（选填）</strong>
              <input
                value={deniedRegions}
                placeholder="us-east-1"
                onChange={(event) => {
                  patch({ deniedRegions: event.target.value })
                }}
              />
            </span>
          </label>
        )}
      </fieldset>

      <div className="job-scope-head sls-project-scope-head">
        <span className="job-scope-title">已发现 Project</span>
        <Badge tone="neutral">{candidates.length}</Badge>
      </div>
      {candidates.length === 0 ? (
        <div className="empty-inline">
          尚未发现 Project。先保存 Region 范围并等待 Connector 同步；也请确认 RAM 具有 log:ListProject 权限。
        </div>
      ) : (
        <div className="job-scope-list">
          {candidates.map((project) => (
            <label key={`${project.region}:${project.name}`}>
              <input
                type="checkbox"
                checked={allowAll || selected.includes(project.name)}
                onChange={() => toggleProject(project.name)}
                disabled={acting}
              />
              <MapPin size={13} />
              <span>
                <strong>{project.name}</strong>
                <small>{aliyunRegionLabel(project.region)}</small>
              </span>
            </label>
          ))}
        </div>
      )}
      {!allowAll && (
        <label className="sls-project-input">
          <span>允许 Project（逗号分隔）</span>
          <input
            value={selected.join(', ')}
            placeholder="aipc-log, test-aipc-log"
            onChange={(event) => {
              patch({
                selected: [
                  ...new Set(
                    event.target.value
                      .split(/[\s,]+/)
                      .map((item) => item.trim())
                      .filter(Boolean),
                  ),
                ],
              })
            }}
          />
          <small>可直接补回当前未发现或刚新增的 Project；保存后 Connector 会重新探测。</small>
        </label>
      )}

      {dirty && (
        <button
          className="primary-button"
          style={{ marginTop: 8 }}
          disabled={acting || (!allowAll && selected.length === 0) || (regionMode === 'allowlist' && !regions.trim())}
          onClick={submit}
        >
          {acting ? <Loader2 size={14} className="spin" /> : <Check size={14} />}保存范围
        </button>
      )}
    </div>
  )
}
