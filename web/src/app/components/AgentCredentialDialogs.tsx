import { useEffect, useRef, useState, type FormEvent } from 'react'
import {
  AlertTriangle, Bot, Check, Copy, Download, KeyRound, Loader2, ShieldCheck, X,
} from 'lucide-react'
import type {
  ApiError, ApiPrincipal, CreateAPIKeyRequest, CreateAPIKeyResponse, CreatePrincipalRequest, CreateRegistrationAPIKeyRequest,
} from '../../shared/api'
import { createPrincipal, createPrincipalAPIKey, createRegistrationAPIKey } from '../../shared/api'

const scopeOptions = [
  { value: 'pam:resources:read', label: '读取资源与动作', detail: '查看可执行资源和 Action Schema' },
  { value: 'pam:executions:create', label: '创建执行', detail: '向 PAM 提交受策略控制的 Execution' },
  { value: 'pam:executions:read:self', label: '读取自己的执行', detail: '只能查看该 Principal 创建的 Execution' },
  { value: 'pam:executions:cancel:self', label: '取消自己的执行', detail: '只能取消该 Principal 尚未完成的 Execution' },
] as const

function useDialogEscape(onClose: () => void) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onClose])
}

export function CreateAgentDrawer({ onClose, onCreated }: {
  onClose: () => void
  onCreated: (principal: ApiPrincipal) => void
}) {
  useDialogEscape(onClose)
  const inputRef = useRef<HTMLInputElement>(null)
  const [name, setName] = useState('')
  const [agentId, setAgentId] = useState('')
  const [workspaceId, setWorkspaceId] = useState('')
  const [ownerId, setOwnerId] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => inputRef.current?.focus(), [])

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const metadata: Record<string, unknown> = { platform: 'multica', verification: 'DECLARED' }
    if (agentId.trim()) metadata.multica_agent_id = agentId.trim()
    if (workspaceId.trim()) metadata.multica_workspace_id = workspaceId.trim()
    if (ownerId.trim()) metadata.multica_owner_id = ownerId.trim()
    const payload: CreatePrincipalRequest = { name: name.trim(), metadata }
    setSaving(true); setError('')
    createPrincipal(payload).then(onCreated).catch((e: ApiError) => setError(e.message)).finally(() => setSaving(false))
  }

  return <><button className="drawer-scrim" aria-label="关闭创建 Agent" onClick={onClose} /><aside className="drawer" role="dialog" aria-modal="true" aria-labelledby="create-agent-title">
    <div className="drawer-header"><div><span className="eyebrow"><Bot size={15} />Agent Principal</span><h2 id="create-agent-title">新增 Agent 身份</h2><p>一个 Multica Agent 对应一个 PAM Principal。</p></div><button className="icon-button" onClick={onClose} aria-label="关闭"><X size={20} /></button></div>
    <form onSubmit={submit}>
      <div className="identity-callout"><ShieldCheck size={19} /><div><strong>平台上下文按声明信息记录</strong><span>Multica 当前不签发身份 Token，因此 Agent ID、Workspace和 Owner只作为 <code>DECLARED</code> 元数据；真正认证以 PAM API Key为准。</span></div></div>
      <div className="form-grid">
        <label><span>显示名称 *</span><input ref={inputRef} value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 deploy-agent-prod-01" required /></label>
        <label><span>Multica Agent ID</span><input value={agentId} onChange={(e) => setAgentId(e.target.value)} placeholder="agent_xxx（可稍后补充）" /></label>
        <label><span>Workspace ID</span><input value={workspaceId} onChange={(e) => setWorkspaceId(e.target.value)} placeholder="workspace_xxx" /></label>
        <label><span>Owner ID</span><input value={ownerId} onChange={(e) => setOwnerId(e.target.value)} placeholder="owner_xxx" /></label>
      </div>
      {error && <div className="credential-note error-note"><AlertTriangle size={16} /><span>{error}</span></div>}
      <div className="drawer-actions"><button type="button" className="secondary-button" onClick={onClose}>取消</button><button className="primary-button" disabled={saving || !name.trim()}>{saving ? <Loader2 size={16} className="spin" /> : <Bot size={16} />}创建并配置 Key</button></div>
    </form>
  </aside></>
}

export function CreateRegistrationBotDrawer({ onClose, onCreated }: {
  onClose: () => void
  onCreated: (principal: ApiPrincipal) => void
}) {
  useDialogEscape(onClose)
  const inputRef = useRef<HTMLInputElement>(null)
  const [name, setName] = useState('PAM注册机器人')
  const [agentId, setAgentId] = useState('')
  const [workspaceId, setWorkspaceId] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => inputRef.current?.focus(), [])

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const metadata: Record<string, unknown> = { platform: 'multica', verification: 'DECLARED', purpose: 'pam_resource_registration' }
    if (agentId.trim()) metadata.multica_agent_id = agentId.trim()
    if (workspaceId.trim()) metadata.multica_workspace_id = workspaceId.trim()
    const payload: CreatePrincipalRequest = { name: name.trim(), kind: 'workload', metadata }
    setSaving(true); setError('')
    createPrincipal(payload).then(onCreated).catch((e: ApiError) => setError(e.message)).finally(() => setSaving(false))
  }

  return <><button className="drawer-scrim" aria-label="关闭创建注册机器人" onClick={onClose} /><aside className="drawer" role="dialog" aria-modal="true" aria-labelledby="create-registration-bot-title">
    <div className="drawer-header"><div><span className="eyebrow"><ShieldCheck size={15} />Registration Workload</span><h2 id="create-registration-bot-title">新增 PAM 注册机器人</h2><p>创建独立 Workload Principal，不能执行任务或代签审批。</p></div><button className="icon-button" onClick={onClose} aria-label="关闭"><X size={20} /></button></div>
    <form onSubmit={submit}>
      <div className="identity-callout"><ShieldCheck size={19} /><div><strong>固定最小管理权限</strong><span>Key 只允许读取 Connector、Project、Integration，以及创建或更新 Project、Integration 和项目范围；不包含 <code>pam:admin</code>。</span></div></div>
      <div className="form-grid">
        <label><span>显示名称 *</span><input ref={inputRef} value={name} onChange={(e) => setName(e.target.value)} required /></label>
        <label><span>Multica Agent ID</span><input value={agentId} onChange={(e) => setAgentId(e.target.value)} placeholder="603c94f9-..." /></label>
        <label><span>Workspace ID</span><input value={workspaceId} onChange={(e) => setWorkspaceId(e.target.value)} placeholder="workspace UUID" /></label>
      </div>
      {error && <div className="credential-note error-note"><AlertTriangle size={16} /><span>{error}</span></div>}
      <div className="drawer-actions"><button type="button" className="secondary-button" onClick={onClose}>取消</button><button className="primary-button" disabled={saving || !name.trim()}>{saving ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}创建并生成专用 Key</button></div>
    </form>
  </aside></>
}

export function CreateAgentKeyDrawer({ principal, onClose, onCreated }: {
  principal: ApiPrincipal
  onClose: () => void
  onCreated: (result: CreateAPIKeyResponse) => void
}) {
  useDialogEscape(onClose)
  const inputRef = useRef<HTMLInputElement>(null)
  const [name, setName] = useState('multica-agent')
  const [expiresOn, setExpiresOn] = useState('')
  const [scopes, setScopes] = useState<string[]>(scopeOptions.map((scope) => scope.value))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => inputRef.current?.focus(), [])

  const toggleScope = (scope: string) => setScopes((current) => current.includes(scope) ? current.filter((item) => item !== scope) : [...current, scope])
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const payload: CreateAPIKeyRequest = { name: name.trim(), scopes }
    if (expiresOn) payload.expiresAt = new Date(`${expiresOn}T23:59:59`).toISOString()
    setSaving(true); setError('')
    createPrincipalAPIKey(principal.id, payload).then(onCreated).catch((e: ApiError) => setError(e.message)).finally(() => setSaving(false))
  }

  return <><button className="drawer-scrim" aria-label="关闭创建 API Key" onClick={onClose} /><aside className="drawer" role="dialog" aria-modal="true" aria-labelledby="create-key-title">
    <div className="drawer-header"><div><span className="eyebrow"><KeyRound size={15} />Agent Credential</span><h2 id="create-key-title">为 {principal.name} 创建 Key</h2><p>Key只属于当前 Principal，不能用于管理端操作。</p></div><button className="icon-button" onClick={onClose} aria-label="关闭"><X size={20} /></button></div>
    <form onSubmit={submit}>
      <div className="form-grid compact-grid">
        <label><span>Key 名称 *</span><input ref={inputRef} value={name} onChange={(e) => setName(e.target.value)} placeholder="multica-agent" required /></label>
        <label><span>有效期（可选）</span><input type="date" min={new Date().toISOString().slice(0, 10)} value={expiresOn} onChange={(e) => setExpiresOn(e.target.value)} /></label>
      </div>
      <fieldset className="scope-picker"><legend>最小权限范围</legend>{scopeOptions.map((scope) => <label key={scope.value}><input type="checkbox" checked={scopes.includes(scope.value)} onChange={() => toggleScope(scope.value)} /><span><strong>{scope.label}</strong><small>{scope.detail}</small><code>{scope.value}</code></span></label>)}</fieldset>
      <div className="identity-callout warning"><AlertTriangle size={19} /><div><strong>明文只展示一次</strong><span>创建后数据库只保存 Hash。关闭下一步的安全窗口后，无法再次查看原 Key，只能轮换。</span></div></div>
      {error && <div className="credential-note error-note"><AlertTriangle size={16} /><span>{error}</span></div>}
      <div className="drawer-actions"><button type="button" className="secondary-button" onClick={onClose}>取消</button><button className="primary-button" disabled={saving || !name.trim() || scopes.length === 0}>{saving ? <Loader2 size={16} className="spin" /> : <KeyRound size={16} />}生成 API Key</button></div>
    </form>
  </aside></>
}

export function CreateRegistrationKeyDrawer({ principal, onClose, onCreated }: {
  principal: ApiPrincipal
  onClose: () => void
  onCreated: (result: CreateAPIKeyResponse) => void
}) {
  useDialogEscape(onClose)
  const inputRef = useRef<HTMLInputElement>(null)
  const [name, setName] = useState('pam-resource-registration')
  const [expiresOn, setExpiresOn] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => inputRef.current?.focus(), [])

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const payload: CreateRegistrationAPIKeyRequest = { name: name.trim() }
    if (expiresOn) payload.expiresAt = new Date(`${expiresOn}T23:59:59`).toISOString()
    setSaving(true); setError('')
    createRegistrationAPIKey(principal.id, payload).then(onCreated).catch((e: ApiError) => setError(e.message)).finally(() => setSaving(false))
  }

  return <><button className="drawer-scrim" aria-label="关闭创建注册凭据" onClick={onClose} /><aside className="drawer" role="dialog" aria-modal="true" aria-labelledby="create-registration-key-title">
    <div className="drawer-header"><div><span className="eyebrow"><KeyRound size={15} />Registration Credential</span><h2 id="create-registration-key-title">为 {principal.name} 创建专用 Key</h2><p>服务端自动绑定当前 PAM API Endpoint；默认 30 天，最长 90 天。</p></div><button className="icon-button" onClick={onClose} aria-label="关闭"><X size={20} /></button></div>
    <form onSubmit={submit}>
      <div className="form-grid compact-grid">
        <label><span>Key 名称 *</span><input ref={inputRef} value={name} onChange={(e) => setName(e.target.value)} required /></label>
        <label><span>有效期（可选）</span><input type="date" min={new Date().toISOString().slice(0, 10)} value={expiresOn} onChange={(e) => setExpiresOn(e.target.value)} /></label>
      </div>
      <fieldset className="scope-picker"><legend>固定权限范围</legend>
        <label><input type="checkbox" checked disabled /><span><strong>读取注册上下文</strong><small>Connector、Project、Integration 和项目范围</small><code>pam:registration:read</code></span></label>
        <label><input type="checkbox" checked disabled /><span><strong>写入注册配置</strong><small>创建/更新 Project、Integration 和项目范围</small><code>pam:registration:write</code></span></label>
      </fieldset>
      <div className="identity-callout warning"><AlertTriangle size={19} /><div><strong>不能替代 Admin Key</strong><span>该 Key 无法审批、执行、删除 Connector/Project/Integration、修改策略或读取 Secret 明文。</span></div></div>
      {error && <div className="credential-note error-note"><AlertTriangle size={16} /><span>{error}</span></div>}
      <div className="drawer-actions"><button type="button" className="secondary-button" onClick={onClose}>取消</button><button className="primary-button" disabled={saving || !name.trim()}>{saving ? <Loader2 size={16} className="spin" /> : <KeyRound size={16} />}生成 Registration Key</button></div>
    </form>
  </aside></>
}

export function OneTimeKeyDialog({ result, principalName, onClose }: {
  result: CreateAPIKeyResponse
  principalName: string
  onClose: () => void
}) {
  useDialogEscape(onClose)
  const closeRef = useRef<HTMLButtonElement>(null)
  const [copied, setCopied] = useState(false)
  useEffect(() => closeRef.current?.focus(), [])
  const copy = () => navigator.clipboard.writeText(result.apiKey).then(() => { setCopied(true); window.setTimeout(() => setCopied(false), 1800) })
  const download = () => {
    const blob = new Blob([result.apiKey + '\n'], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const prefix = result.credential.purpose === 'REGISTRATION' ? 'pam-registration' : 'pam-agent'
    const link = document.createElement('a'); link.href = url; link.download = `${prefix}-${principalName.replace(/[^a-zA-Z0-9._-]+/g, '-')}.key`; link.click()
    URL.revokeObjectURL(url)
  }
  return <div className="modal-layer" role="presentation"><button className="modal-scrim" aria-label="关闭 API Key 窗口" onClick={onClose} /><section className="secret-dialog" role="dialog" aria-modal="true" aria-labelledby="one-time-key-title">
    <div className="secret-dialog-icon"><KeyRound size={24} /></div>
    <div><span className="eyebrow">One-time secret</span><h2 id="one-time-key-title">立即保存 {principalName} 的 API Key</h2><p>离开此窗口后，PAM不会再次返回这段明文。</p></div>
    <div className="secret-value"><code>{result.apiKey}</code><button className="icon-button" onClick={copy} aria-label="复制 API Key" title="复制 API Key">{copied ? <Check size={17} /> : <Copy size={17} />}</button></div>
    <div className="secret-guidance"><AlertTriangle size={18} /><span>请把 Key注入对应 Multica Agent的 Secret环境变量 <code>{result.credential.purpose === 'REGISTRATION' ? 'PAM_REGISTRATION_API_KEY' : 'PAM_API_KEY'}</code>，不要粘贴到 Issue、日志、代码仓库或聊天消息中。{result.apiEndpoint && <>Endpoint 固定为 <code>{result.apiEndpoint}</code>。</>}下载后请执行 <code>chmod 600</code>。</span></div>
    <div className="secret-dialog-actions"><button className="secondary-button" onClick={download}><Download size={16} />下载 Key 文件</button><button ref={closeRef} className="primary-button" onClick={onClose}><Check size={16} />我已安全保存</button></div>
  </section></div>
}
