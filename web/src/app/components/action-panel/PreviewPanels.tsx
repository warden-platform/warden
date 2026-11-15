import { stringify as stringifyYaml } from 'yaml'
import { ChevronDown, Loader2, ShieldAlert, ShieldCheck } from 'lucide-react'
import { GENERIC_KUBERNETES_KIND_OPTIONS, type NetworkInterfacePreview } from './constants'

// ActionPanel 内嵌的状态条与预检面板：Kubernetes Manifest 编辑器、H3C 接口预检、
// 变更前状态读取提示。

export function MutationPrefillState({ loading, error, readyText, onRetry }: { loading: boolean; error: string; readyText: string; onRetry: () => void }) {
  return <div className={`grafana-prefill-state${error ? ' error' : ''}`} role={error ? 'alert' : 'status'}>
    {loading ? <Loader2 size={16} className="spin" /> : error ? <ShieldAlert size={16} /> : <ShieldCheck size={16} />}
    <span><strong>{loading ? '正在读取目标当前状态' : error ? '当前状态读取失败' : '已读取当前状态'}</strong><small>{error || readyText}</small></span>
    {error && <button type="button" className="quiet-button" onClick={onRetry}>重新读取</button>}
  </div>
}

export function GenericKubernetesEditor({ actionName, parameters, manifestText, manifestError, preview, previewLoading, previewError, onManifestChange, onKindChange, onPreview }: {
  actionName: string
  parameters: Record<string, unknown>
  manifestText: string
  manifestError: string
  preview: { before?: unknown; after?: unknown; previewHash: string; signature: string } | null
  previewLoading: boolean
  previewError: string
  onManifestChange: (value: string) => void
  onKindChange: (value: string) => void
  onPreview: () => void
}) {
  const create = actionName === 'kubernetes.resource.create'
  return <fieldset className="kubernetes-manifest-editor">
    <legend>Kubernetes Manifest</legend>
    {create && <label><span>允许的资源类型</span><div className="select-wrap full"><select value={`${String(parameters.apiVersion ?? '')}/${String(parameters.kind ?? '')}`} onChange={(event) => onKindChange(event.target.value)}>{GENERIC_KUBERNETES_KIND_OPTIONS.map((item) => <option key={`${item.apiVersion}/${item.kind}`} value={`${item.apiVersion}/${item.kind}`}>{item.kind} · {item.apiVersion}</option>)}</select><ChevronDown size={15} /></div></label>}
    {!create && <div className="manifest-identity"><span>{String(parameters.apiVersion ?? '')}</span><strong>{String(parameters.kind ?? '')}/{String(parameters.namespace ?? '')}/{String(parameters.name ?? '')}</strong><small>身份字段由资源目录固定，不允许在编辑器中改为其他目标。</small></div>}
    <label><span>YAML / JSON</span><textarea className="manifest-textarea" rows={16} value={manifestText} onChange={(event) => onManifestChange(event.target.value)} spellCheck={false} aria-invalid={Boolean(manifestError)} /></label>
    {manifestError && <div className="form-error" role="alert">Manifest 格式错误：{manifestError}</div>}
    <div className="manifest-security-note">Secret、Token、密码、私钥和授权字段会被拒绝；status 与 managedFields 不会写回。</div>
    <button type="button" className="secondary-button preview-button" disabled={previewLoading || Boolean(manifestError) || !parameters.manifest} onClick={onPreview}>{previewLoading ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}{previewLoading ? '正在执行 server-side dry-run' : '预检变更并生成 Diff'}</button>
    {previewError && <div className="form-error" role="alert">{previewError}</div>}
    {preview && <div className="kubernetes-diff" role="status">
      <div className="diff-heading"><strong>Server-side dry-run 已通过</strong><code>{preview.previewHash.slice(0, 12)}</code></div>
      <div className="diff-columns">
        <section><span>Before</span><pre>{preview.before ? stringifyYaml(preview.before) : '# 新建资源，当前不存在'}</pre></section>
        <section><span>After</span><pre>{stringifyYaml(preview.after)}</pre></section>
      </div>
    </div>}
  </fieldset>
}

export function NetworkInterfacePreviewPanel({ preview, previewLoading, previewError, stale, onPreview }: {
  preview: NetworkInterfacePreview | null
  previewLoading: boolean
  previewError: string
  stale: boolean
  onPreview: () => void
}) {
  return <fieldset className="kubernetes-manifest-editor">
    <legend>接口变更预检</legend>
    <div className="manifest-security-note">预检会重新读取目标接口，固定实际下发命令并绑定当前状态、目标参数与保存选项。参数或设备状态变化后必须重新预检。</div>
    <button type="button" className="secondary-button preview-button" disabled={previewLoading} onClick={onPreview}>{previewLoading ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}{previewLoading ? '正在读取接口状态' : preview ? '重新预检' : '预检接口变更'}</button>
    {stale && <div className="form-error" role="alert">参数已经变化，之前的预检已失效，请重新预检。</div>}
    {previewError && <div className="form-error" role="alert">{previewError}</div>}
    {preview && <div className="kubernetes-diff" role="status">
      <div className="diff-heading"><strong>{preview.noOp ? '目标状态已满足，无需下发命令' : '接口变更预检已通过'}</strong><code>{preview.previewHash.slice(0, 12)}</code></div>
      <div className="diff-columns">
        <section><span>Before</span><pre>{stringifyYaml(preview.before ?? {})}</pre></section>
        <section><span>After</span><pre>{stringifyYaml(preview.after ?? {})}</pre></section>
      </div>
      <section><span>完整设备命令计划</span><pre>{preview.noOp ? '# 无需下发命令' : Array.isArray(preview.commands) && preview.commands.length === 0 ? '# 无设备命令' : Array.isArray(preview.commands) ? preview.commands.map(String).join('\n') : stringifyYaml(preview.commands ?? [])}</pre></section>
	  <div className="manifest-security-note">{preview.noOp ? '当前配置已经满足目标状态，提交后只记录审计，不进入配置模式。' : preview.save ? '将执行 save force，保存设备当前全部运行配置，包括本次操作以外尚未保存的改动。' : '不执行 save force；本次变更仅写入运行配置，设备重启后可能丢失。'}</div>
    </div>}
  </fieldset>
}
