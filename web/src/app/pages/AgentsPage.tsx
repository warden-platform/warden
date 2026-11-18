import { useEffect, useMemo, useState } from 'react'
import {
  Bot,
  Clock3,
  KeyRound,
  Loader2,
  Plus,
  RefreshCw,
  RotateCw,
  Search,
  ShieldCheck,
  Slash,
  Trash2,
  UserRoundCheck,
} from 'lucide-react'
import type { ApiError, ApiPrincipal, ApiPrincipalKey, CreateAPIKeyResponse } from '../../shared/api'
import { deletePrincipal, listPrincipalAPIKeys, listPrincipals, revokePrincipalAPIKey, rotatePrincipalAPIKey } from '../../shared/api'
import { Badge } from '../components/Badge'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { useApp } from '../components/Toast'
import { CreateAgentDrawer, CreateAgentKeyDrawer, OneTimeKeyDialog } from '../components/AgentCredentialDialogs'
import { CreateRegistrationBotDrawer, CreateRegistrationKeyDrawer } from '../components/AgentCredentialDialogs'

const scopeLabels: Record<string, string> = {
  'warden:resources:read': '资源读取',
  'warden:executions:create': '创建执行',
  'warden:executions:read:self': '读取自己',
  'warden:executions:cancel:self': '取消自己',
  'warden:delegation:exchange': '委托 Token 交换',
  'warden:registration:read': '注册读取',
  'warden:registration:write': '注册写入',
}

const formatTime = (value?: string) => (value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '从未使用')

export function AgentsPage() {
  const { notify } = useApp()
  const [principals, setPrincipals] = useState<ApiPrincipal[]>([])
  const [keysByPrincipal, setKeysByPrincipal] = useState<Record<string, ApiPrincipalKey[]>>({})
  const [selectedId, setSelectedId] = useState('')
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [createAgentOpen, setCreateAgentOpen] = useState(false)
  const [createRegistrationOpen, setCreateRegistrationOpen] = useState(false)
  const [createKeyFor, setCreateKeyFor] = useState<ApiPrincipal | null>(null)
  const [oneTimeKey, setOneTimeKey] = useState<{ result: CreateAPIKeyResponse; principal: ApiPrincipal } | null>(null)
  const [busyKey, setBusyKey] = useState('')
  const [busyDelete, setBusyDelete] = useState(false)

  const load = () => {
    setLoading(true)
    setError('')
    Promise.all([listPrincipals('agent_instance'), listPrincipals('workload')])
      .then(async ([agentItems, workloadItems]) => {
        const registrationBots = (workloadItems ?? []).filter((item) => item.metadata?.purpose === 'warden_resource_registration')
        const agents = [...(agentItems ?? []), ...registrationBots]
        const keyPairs = await Promise.all(
          agents.map(async (agent) => {
            try {
              return [agent.id, (await listPrincipalAPIKeys(agent.id)) ?? []] as const
            } catch {
              // A single stale/disabled Principal must not blank the whole Agent page.
              return [agent.id, []] as const
            }
          }),
        )
        setPrincipals(agents)
        setKeysByPrincipal(Object.fromEntries(keyPairs))
        setSelectedId((current) => (agents.some((agent) => agent.id === current) ? current : (agents[0]?.id ?? '')))
      })
      .catch((e: ApiError) => setError(e.message))
      .finally(() => setLoading(false))
  }
  useEffect(load, [])

  const allKeys = useMemo(() => Object.values(keysByPrincipal).flatMap((keys) => keys ?? []), [keysByPrincipal])
  const selected = principals.find((principal) => principal.id === selectedId) ?? null
  const selectedKeys = selected ? (keysByPrincipal[selected.id] ?? []) : []
  const filtered = principals.filter((principal) => {
    const metadata = Object.values(principal.metadata ?? {}).join(' ')
    return `${principal.name} ${principal.id} ${metadata}`.toLowerCase().includes(query.toLowerCase())
  })
  const activeKeys = allKeys.filter((key) => key.status === 'ACTIVE' && (!key.expires_at || new Date(key.expires_at) > new Date())).length
  const usedRecently = allKeys.filter((key) => key.last_used_at && Date.now() - new Date(key.last_used_at).getTime() < 86_400_000).length

  const refreshPrincipalKeys = (principalId: string) =>
    listPrincipalAPIKeys(principalId).then((items) => setKeysByPrincipal((current) => ({ ...current, [principalId]: items ?? [] })))
  const onPrincipalCreated = (principal: ApiPrincipal) => {
    setPrincipals((current) => [principal, ...current])
    setKeysByPrincipal((current) => ({ ...current, [principal.id]: [] }))
    setSelectedId(principal.id)
    setCreateAgentOpen(false)
    setCreateRegistrationOpen(false)
    setCreateKeyFor(principal)
    notify(principal.kind === 'workload' ? '注册机器人身份已创建，请继续生成专用 Key' : 'Agent Principal 已创建，请继续生成独立 Key')
  }
  const onKeyCreated = (principal: ApiPrincipal, result: CreateAPIKeyResponse) => {
    setCreateKeyFor(null)
    setOneTimeKey({ principal, result })
    refreshPrincipalKeys(principal.id)
    notify(result.credential.purpose === 'REGISTRATION' ? 'Registration Key 已生成' : 'Agent API Key 已生成')
  }
  const revoke = (key: ApiPrincipalKey) => {
    if (!selected || !window.confirm(`确认撤销 ${key.name}？对应 Agent会立即收到 401，且无法恢复。`)) return
    setBusyKey(key.id)
    revokePrincipalAPIKey(key.id)
      .then(() => {
        notify('API Key 已撤销')
        return refreshPrincipalKeys(selected.id)
      })
      .catch((e: ApiError) => notify(`撤销失败：${e.message}`, 'error'))
      .finally(() => setBusyKey(''))
  }
  const rotate = (key: ApiPrincipalKey) => {
    if (!selected || !window.confirm(`轮换 ${key.name} 后旧 Key会立即失效。请确认你可以及时更新外部 Agent 环境变量。`)) return
    setBusyKey(key.id)
    rotatePrincipalAPIKey(key.id)
      .then((result) => {
        setOneTimeKey({ principal: selected, result })
        notify('Key 已轮换，旧 Key 已撤销')
        return refreshPrincipalKeys(selected.id)
      })
      .catch((e: ApiError) => notify(`轮换失败：${e.message}`, 'error'))
      .finally(() => setBusyKey(''))
  }
  const deleteAgent = () => {
    if (!selected || !window.confirm(`确认删除 Agent「${selected.name}」？\n\n所有 API Key 将立即失效，此操作不可逆。`)) return
    setBusyDelete(true)
    deletePrincipal(selected.id)
      .then(() => {
        setPrincipals((current) => current.filter((p) => p.id !== selected.id))
        setKeysByPrincipal((current) => {
          const next = { ...current }
          delete next[selected.id]
          return next
        })
        setSelectedId('')
        notify('Agent 已删除，所有 Key 已失效')
      })
      .catch((e: ApiError) => notify(`删除失败：${e.message}`, 'error'))
      .finally(() => setBusyDelete(false))
  }

  if (loading) return <LoadingState label="加载 Agent 身份" />
  if (error) return <ErrorState message={error} onRetry={load} />

  return (
    <div className="view-stack">
      <div className="summary-strip agent-summary">
        <div>
          <UserRoundCheck size={19} />
          <span>Agent Principal</span>
          <strong>{principals.length}</strong>
        </div>
        <div>
          <KeyRound size={19} />
          <span>有效 Key</span>
          <strong>{activeKeys}</strong>
        </div>
        <div>
          <Clock3 size={19} />
          <span>24h 内使用</span>
          <strong>{usedRecently}</strong>
        </div>
        <div>
          <Slash size={19} />
          <span>已撤销 Key</span>
          <strong>{allKeys.filter((key) => key.status === 'REVOKED').length}</strong>
        </div>
      </div>
      <div className="agent-page-actions">
        <div className="identity-security-note">
          <ShieldCheck size={17} />
          <span>Execution和管理注册均按 Principal + Credential双重归属</span>
        </div>
        <div className="agent-detail-actions">
          <button className="secondary-button" onClick={() => setCreateRegistrationOpen(true)}>
            <ShieldCheck size={16} />
            新增注册机器人
          </button>
          <button className="primary-button" onClick={() => setCreateAgentOpen(true)}>
            <Plus size={16} />
            新增 Agent
          </button>
        </div>
      </div>
      {principals.length === 0 ? (
        <EmptyState
          icon={Bot}
          title="尚未创建 Agent 身份"
          text="为每个外部 Agent 创建独立 Principal 和 API Key，才能实现逐 Agent 追踪与撤销。"
        />
      ) : (
        <div className="agent-management-grid">
          <section className="panel agent-list-panel">
            <div className="ops-ph">
              <span>Agent Registry · 身份名册</span>
              <span className="font-mono text-[10px] text-content-subtle dark:text-content-dark-subtle">{filtered.length}/{principals.length}</span>
            </div>
            <div className="toolbar">
              <div className="search-box">
                <Search size={17} />
                <input
                  aria-label="搜索 Agent"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="搜索名称、Agent ID或 Workspace"
                />
              </div>
              <button className="icon-button" onClick={load} aria-label="刷新 Agent列表" title="刷新">
                <RefreshCw size={16} />
              </button>
            </div>
            <div className="agent-list" role="listbox" aria-label="Agent Principal列表">
              {filtered.map((principal) => {
                const keys = keysByPrincipal[principal.id] ?? []
                const active = keys.filter((key) => key.status === 'ACTIVE').length
                return (
                  <button
                    key={principal.id}
                    role="option"
                    aria-selected={selectedId === principal.id}
                    className={`agent-list-item ${selectedId === principal.id ? 'selected' : ''}`}
                    onClick={() => setSelectedId(principal.id)}
                  >
                    <span className="agent-avatar">{principal.kind === 'workload' ? <ShieldCheck size={18} /> : <Bot size={18} />}</span>
                    <span className="agent-list-copy">
                      <strong>{principal.name}</strong>
                      <small>
                        {principal.kind === 'workload'
                          ? 'Warden 资源注册机器人'
                          : String(principal.metadata?.external_agent_id ?? principal.metadata?.multica_agent_id ?? principal.id)}
                      </small>
                    </span>
                    <span className="agent-list-state">
                      <span className={`exec-led ${principal.status === 'ACTIVE' ? 'ok' : 'deny'}`}>
                        <i aria-hidden="true" />
                        <Badge tone={principal.status === 'ACTIVE' ? 'success' : 'neutral'}>{principal.status}</Badge>
                      </span>
                      <small>{active} 个有效 Key</small>
                    </span>
                  </button>
                )
              })}
              {filtered.length === 0 && <div className="empty-inline">没有匹配的 Agent。</div>}
            </div>
          </section>
          {selected && (
            <section className="panel agent-detail-panel">
              <div className="ops-ph">
                <span>Identity Dossier · 身份档案</span>
                <span className="font-mono text-[10px] text-content-subtle dark:text-content-dark-subtle">{selected.id.slice(0, 8)}</span>
              </div>
              <div className="agent-detail-header">
                <div className="agent-detail-identity">
                  <span className="agent-avatar large">{selected.kind === 'workload' ? <ShieldCheck size={22} /> : <Bot size={22} />}</span>
                  <div>
                    <div>
                      <h2>{selected.name}</h2>
                      <Badge tone={selected.status === 'ACTIVE' ? 'success' : 'neutral'}>{selected.status}</Badge>
                    </div>
                    <code>{selected.id}</code>
                  </div>
                </div>
                <div className="agent-detail-actions">
                  {selected.status === 'ACTIVE' && (
                    <button className="primary-button compact" onClick={() => setCreateKeyFor(selected)}>
                      <Plus size={15} />
                      创建 Key
                    </button>
                  )}
                  <button className="danger-button compact" disabled={busyDelete} onClick={deleteAgent}>
                    {busyDelete ? <Loader2 size={15} className="spin" /> : <Trash2 size={15} />}删除身份
                  </button>
                </div>
              </div>
              <div className="agent-metadata">
                <div>
                  <span>平台</span>
                  <strong>{String(selected.metadata?.platform ?? '-')}</strong>
                </div>
                <div>
                  <span>身份验证</span>
                  <Badge tone="warning">{String(selected.metadata?.verification ?? 'DECLARED')}</Badge>
                </div>
                <div>
                  <span>Workspace</span>
                  <code>{String(selected.metadata?.external_workspace_id ?? selected.metadata?.multica_workspace_id ?? selected.metadata?.workspace_id ?? '-')}</code>
                </div>
                <div>
                  <span>创建时间</span>
                  <strong>{formatTime(selected.created_at)}</strong>
                </div>
              </div>
              <div className="credential-section-head">
                <div>
                  <h3>{selected.kind === 'workload' ? 'Registration API Keys' : 'Agent API Keys'}</h3>
                  <p>明文不可恢复；轮换会立即撤销旧 Key。</p>
                </div>
                <span>{selectedKeys.length} 条凭据</span>
              </div>
              {selectedKeys.length === 0 ? (
                <div className="agent-key-empty">
                  <KeyRound size={24} />
                  <strong>这个 Agent还没有 API Key</strong>
                  <span>创建后将 Key安全注入对应 Multica Agent。</span>
                  <button className="primary-button compact" onClick={() => setCreateKeyFor(selected)}>
                    <Plus size={15} />
                    创建第一把 Key
                  </button>
                </div>
              ) : (
                <div className="credential-list">
                  {selectedKeys.map((key) => {
                    const expired = !!key.expires_at && new Date(key.expires_at) <= new Date()
                    const active = key.status === 'ACTIVE' && !expired
                    return (
                      <article className="credential-row" key={key.id}>
                        <div className={`credential-key-icon ${active ? 'active' : ''}`}>
                          <KeyRound size={18} />
                        </div>
                        <div className="credential-main">
                          <div>
                            <strong>{key.name}</strong>
                            <Badge
                              tone={
                                key.purpose === 'REGISTRATION'
                                  ? 'info'
                                  : active
                                    ? 'success'
                                    : key.status === 'REVOKED'
                                      ? 'danger'
                                      : 'warning'
                              }
                            >
                              {key.purpose === 'REGISTRATION'
                                ? '注册专用'
                                : key.status === 'REVOKED'
                                  ? '已撤销'
                                  : expired
                                    ? '已过期'
                                    : '有效'}
                            </Badge>
                            <Badge tone={active ? 'success' : 'neutral'}>
                              {active ? '有效' : key.status === 'REVOKED' ? '已撤销' : '已过期'}
                            </Badge>
                          </div>
                          <code>{key.key_prefix}••••••••</code>
                          {key.audience && <small>Endpoint：{key.audience}</small>}
                          <div className="credential-scopes">
                            {key.scopes.map((scope) => (
                              <span key={scope}>{scopeLabels[scope] ?? scope}</span>
                            ))}
                          </div>
                        </div>
                        <div className="credential-usage">
                          <span>最后使用</span>
                          <strong className="font-mono">{formatTime(key.last_used_at)}</strong>
                          {key.last_used_ip && <code>{key.last_used_ip}</code>}
                          {key.expires_at && <small className="font-mono">到期 {formatTime(key.expires_at)}</small>}
                        </div>
                        <div className="credential-actions">
                          {active && (
                            <>
                              <button className="quiet-button" disabled={busyKey === key.id} onClick={() => rotate(key)}>
                                {busyKey === key.id ? <Loader2 size={15} className="spin" /> : <RotateCw size={15} />}轮换
                              </button>
                              <button className="danger-button compact" disabled={busyKey === key.id} onClick={() => revoke(key)}>
                                撤销
                              </button>
                            </>
                          )}
                        </div>
                      </article>
                    )
                  })}
                </div>
              )}
            </section>
          )}
        </div>
      )}
      {createAgentOpen && <CreateAgentDrawer onClose={() => setCreateAgentOpen(false)} onCreated={onPrincipalCreated} />}
      {createRegistrationOpen && (
        <CreateRegistrationBotDrawer onClose={() => setCreateRegistrationOpen(false)} onCreated={onPrincipalCreated} />
      )}
      {createKeyFor &&
        (createKeyFor.kind === 'workload' ? (
          <CreateRegistrationKeyDrawer
            principal={createKeyFor}
            onClose={() => setCreateKeyFor(null)}
            onCreated={(result) => onKeyCreated(createKeyFor, result)}
          />
        ) : (
          <CreateAgentKeyDrawer
            principal={createKeyFor}
            onClose={() => setCreateKeyFor(null)}
            onCreated={(result) => onKeyCreated(createKeyFor, result)}
          />
        ))}
      {oneTimeKey && (
        <OneTimeKeyDialog result={oneTimeKey.result} principalName={oneTimeKey.principal.name} onClose={() => setOneTimeKey(null)} />
      )}
    </div>
  )
}
