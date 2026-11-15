import { useState } from 'react'
import { AlertTriangle, Check, Copy, KeyRound, Loader2, X } from 'lucide-react'
import type { ApiError, ApiProject } from '../../shared/api'
import { createNetworkConnector } from '../../shared/api'

export function NetworkConnectorProvisionModal({
  projects,
  onClose,
  onComplete,
}: {
  projects: ApiProject[]
  onClose: () => void
  onComplete: () => void
}) {
  const [name, setName] = useState('h3c-network-connector')
  const [environment, setEnvironment] = useState<'development' | 'testing' | 'production'>('production')
  const [projectId, setProjectId] = useState(projects.find((p) => p.is_default)?.id ?? '')
  const [result, setResult] = useState<{ key: string; id: string } | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [copied, setCopied] = useState(false)
  const submit = () => {
    setSaving(true)
    setError('')
    createNetworkConnector({ name: name.trim(), environment, projectId: projectId || undefined })
      .then((created) => {
        setResult({ key: created.taskSigningKey, id: created.id })
        onComplete()
      })
      .catch((e: ApiError) => setError(e.message || '创建 Network Connector 失败'))
      .finally(() => setSaving(false))
  }
  const shellQuote = (value: string) => `'${value.replaceAll("'", `"'"'`)}'`
  const command = result
    ? `sudo install -d -m 0750 /etc/warden-network\nprintf '%s' ${shellQuote(result.key)} | sudo install -o root -g warden-network -m 0640 /dev/stdin /etc/warden-network/task-signing-key\nWARDEN_CONTROL_URL=${shellQuote('https://warden.example.com')} WARDEN_CONNECTOR_NAME=${shellQuote(name.trim())} WARDEN_CONNECTOR_ENVIRONMENT=${shellQuote(environment)} WARDEN_TASK_SIGNING_KEY_FILE=${shellQuote('/etc/warden-network/task-signing-key')} /usr/local/bin/warden-network-connector`
    : ''
  const copy = () =>
    navigator.clipboard.writeText(command).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1800)
    })
  return (
    <>
      <button className="drawer-scrim" aria-label="关闭 Network Connector 配置" onClick={onClose} />
      <aside className="drawer onboarding-drawer" aria-label="配置 Network Connector">
        <div className="drawer-header">
          <div>
            <span className="eyebrow">
              <KeyRound size={15} />
              Network Connector
            </span>
            <h2>配置 H3C 内网执行通道</h2>
            <p>先在 Warden 中预置 Connector 身份，再把一次性任务密钥放到内网主机。</p>
          </div>
          <button className="icon-button" onClick={onClose} aria-label="关闭">
            <X size={20} />
          </button>
        </div>
        {!result ? (
          <div className="onboarding-body">
            <label>
              <span>Connector 名称</span>
              <input value={name} onChange={(e) => setName(e.target.value)} />
            </label>
            <label>
              <span>环境</span>
              <select value={environment} onChange={(e) => setEnvironment(e.target.value as typeof environment)}>
                <option value="development">开发</option>
                <option value="testing">测试</option>
                <option value="production">生产</option>
              </select>
            </label>
            <label>
              <span>归属项目</span>
              <select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
                <option value="">系统默认项目</option>
                {projects.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
            <div className="onboarding-hint">
              <AlertTriangle size={17} />
              <span>创建后会返回一次性 taskSigningKey。请只在受控终端保存，不要写入聊天、工单或日志。</span>
            </div>
            {error && (
              <div className="credential-note error-note">
                <AlertTriangle size={16} />
                <span>{error}</span>
              </div>
            )}
            <div className="drawer-actions">
              <button className="secondary-button" onClick={onClose}>
                取消
              </button>
              <button className="primary-button" disabled={saving || !name.trim()} onClick={submit}>
                {saving ? <Loader2 size={16} className="spin" /> : <Check size={16} />}创建并显示一次性密钥
              </button>
            </div>
          </div>
        ) : (
          <div className="onboarding-body">
            <div className="onboarding-hint">
              <Check size={17} />
              <span>Connector 已预置（{result.id}）。下面的密钥只在本次响应中展示，请立即复制到内网主机后关闭此窗口。</span>
            </div>
            <div className="command-block">
              <code>{command}</code>
              <button className="icon-button" onClick={copy} aria-label="复制部署命令">
                {copied ? <Check size={16} /> : <Copy size={16} />}
              </button>
            </div>
            <div className="credential-note">
              <AlertTriangle size={16} />
              <span>部署完成后，启动日志中不应打印 taskSigningKey；如密钥泄露，请删除该 Connector 并重新创建。</span>
            </div>
            <div className="drawer-actions">
              <button className="primary-button" onClick={onClose}>
                我已保存，关闭
              </button>
            </div>
          </div>
        )}
      </aside>
    </>
  )
}
