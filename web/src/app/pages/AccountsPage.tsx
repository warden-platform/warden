import { useEffect, useMemo, useState } from 'react'
import { RefreshCw, Search, ShieldCheck, Users } from 'lucide-react'
import {
  accountDisplayName,
  ApiError,
  assignUnaffiliatedIdentity,
  listAccounts,
  listTenants,
  reenableAccount,
  removeAccountFromOrganization,
  updateAccountRole,
  type AccountRole,
  type ApiAccount,
  type ApiTenant,
} from '../../shared/api'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { useApp } from '../components/Toast'

const roleLabels: Record<string, string> = {
  user: '项目成员',
  tenant_admin: '租户管理员',
  platform_admin: '超级管理员',
}
// platform_admin is reserved for the single built-in super admin and is not
// assignable; administrators only grant member or tenant sub-admin roles.
const roleOptions: AccountRole[] = ['user', 'tenant_admin']

// A federated identity parked in the unaffiliated sentinel tenant has no
// organization yet. The super admin assigns it here — the platform-side answer
// to "who pulls a stranger into an organization".
const UNAFFILIATED_SLUG = 'unaffiliated'

function AssignIdentityRow({
  account,
  tenants,
  busy,
  onAssigned,
}: {
  account: ApiAccount
  tenants: ApiTenant[]
  busy: boolean
  onAssigned: (id: string) => void
}) {
  const { notify } = useApp()
  const [tenant, setTenant] = useState('')
  const [role, setRole] = useState<AccountRole>('user')
  const [submitting, setSubmitting] = useState(false)
  const submit = () => {
    if (!tenant) {
      notify('请先选择要指派到的组织', 'error')
      return
    }
    if (!window.confirm(`确认把「${accountDisplayName(account)}」指派到「${tenant}」为「${roleLabels[role]}」？\n\n该账号当前待分配的会话会失效。`)) return
    setSubmitting(true)
    assignUnaffiliatedIdentity(account.id, { tenant, role })
      .then(() => {
        notify(`已指派到 ${tenant}`)
        onAssigned(account.id)
      })
      .catch((e: ApiError) => notify(e.message || '指派失败', 'error'))
      .finally(() => setSubmitting(false))
  }
  return (
    <div className="assign-cell">
      <select className="account-role-select" value={tenant} disabled={busy || submitting} onChange={(e) => setTenant(e.target.value)} aria-label="目标组织">
        <option value="">选择组织…</option>
        {tenants.map((t) => (
          <option key={t.slug} value={t.slug}>
            {t.name}（{t.slug}）
          </option>
        ))}
      </select>
      <select className="account-role-select" value={role} disabled={busy || submitting} onChange={(e) => setRole(e.target.value as AccountRole)} aria-label="指派角色">
        {roleOptions.map((r) => (
          <option key={r} value={r}>
            {roleLabels[r]}
          </option>
        ))}
      </select>
      <button className="secondary-button" disabled={!tenant || busy || submitting} onClick={submit}>
        指派
      </button>
    </div>
  )
}

function formatTime(value?: string | null) {
  return value ? new Date(value).toLocaleString('zh-CN') : '从未登录'
}

export function AccountsPage() {
  const { notify } = useApp()
  const [accounts, setAccounts] = useState<ApiAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [busy, setBusy] = useState('')
  const [tenants, setTenants] = useState<ApiTenant[]>([])

  const load = (spinner = true) => {
    if (spinner) setLoading(true)
    setError('')
    listAccounts()
      .then((items) => setAccounts(items ?? []))
      .catch((e: ApiError) => setError(e.message || '查询账号失败'))
      .finally(() => setLoading(false))
  }
  useEffect(() => {
    load()
    listTenants().then((items) => setTenants((items ?? []).filter((t) => t.slug !== UNAFFILIATED_SLUG))).catch(() => undefined)
  }, [])

  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    if (!keyword) return accounts
    return accounts.filter((item) =>
      `${accountDisplayName(item)} ${item.name} ${item.email} ${item.tenant_name ?? ''} ${item.provider_subject ?? ''}`.toLowerCase().includes(keyword),
    )
  }, [query, accounts])
  const ssoCount = accounts.filter((item) => item.auth_source === 'oidc').length
  const tenantAdminCount = accounts.filter((item) => item.role === 'tenant_admin').length
  const tenantCount = new Set(accounts.map((item) => item.tenant_id).filter((t) => t !== 'platform' && t !== UNAFFILIATED_SLUG)).size

  const changeRole = (account: ApiAccount, role: AccountRole) => {
    if (role === account.role) return
    const label = accountDisplayName(account)
    if (
      !window.confirm(`确认把「${label}」的角色改为「${roleLabels[role]}」？\n\n该账号的登录会话会立即失效，需要重新登录才能获得新权限。`)
    )
      return
    setBusy(account.id)
    updateAccountRole(account.id, role)
      .then((result) => {
        setAccounts((items) => items.map((item) => (item.id === result.id ? { ...item, role: result.role } : item)))
        notify(result.force_relogin ? `角色已更新，已撤销 ${result.revoked_sessions} 个会话，需重新登录` : '角色已更新')
      })
      .catch((e: ApiError) => notify(e.message || '更新角色失败', 'error'))
      .finally(() => setBusy(''))
  }

  const removeMember = (account: ApiAccount) => {
    const label = accountDisplayName(account)
    const viaSSO = account.auth_source === 'oidc'
    const hint = viaSSO
      ? '该 SSO 成员会被移回“未分配”大厅（下次登录零业务权限）；要彻底禁止其登录，请在身份提供方（如飞书）停用其账号。'
      : '该本地成员将被停用（不能登录），账号与历史审计保留。'
    if (!window.confirm(`确认将「${label}」移出「${account.tenant_name || account.tenant_id}」？\n\n${hint}\n\n其当前登录会话会立即失效。`)) return
    setBusy(account.id)
    removeAccountFromOrganization(account.id)
      .then((result) => {
        notify(result.action === 'removed' ? `已移回未分配大厅（撤销 ${result.revoked_sessions} 个会话）` : `已停用该成员（撤销 ${result.revoked_sessions} 个会话）`)
        load(false)
      })
      .catch((e: ApiError) => notify(e.message || '移出成员失败', 'error'))
      .finally(() => setBusy(''))
  }

  const reenableMember = (account: ApiAccount) => {
    const label = accountDisplayName(account)
    if (!window.confirm(`确认恢复启用本地账号「${label}」？\n\n恢复后该账号可重新登录，原有角色与项目权限保持不变。`)) return
    setBusy(account.id)
    reenableAccount(account.id)
      .then(() => {
        notify(`已恢复启用「${label}」`)
        load(false)
      })
      .catch((e: ApiError) => notify(e.message || '恢复启用失败', 'error'))
      .finally(() => setBusy(''))
  }

  if (loading) return <LoadingState label="正在加载账号与权限" />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  return (
    <div className="view-stack">
      <section className="project-user-summary" aria-label="账号概况">
        <div>
          <strong>{accounts.length}</strong>
          <span>登录账号</span>
        </div>
        <div>
          <strong>{tenantCount}</strong>
          <span>业务组织</span>
        </div>
        <div>
          <strong>{tenantAdminCount}</strong>
          <span>租户管理员</span>
        </div>
        <div>
          <strong>{ssoCount}</strong>
          <span>单点登录</span>
        </div>
      </section>
      <section className="panel">
        <div className="panel-header">
          <div>
            <h2>账号与权限</h2>
            <p>超级管理员视角：此处列出所有租户的登录账号。超级管理员仅内置 admin 一个；各租户管理员是子管理员，只能在「租户管理 → 成员」里管理本租户成员。</p>
          </div>
          <button className="icon-button" title="刷新" aria-label="刷新账号列表" onClick={() => load(false)}>
            <RefreshCw size={16} />
          </button>
        </div>
        <div className="toolbar">
          <label className="search-box">
            <Search size={15} />
            <span className="sr-only">搜索账号</span>
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索姓名、账号或邮箱" />
          </label>
          <span className="project-user-result-count">{filtered.length} 个账号</span>
        </div>
        <div className="account-hint">
          <ShieldCheck size={15} />
          内置超级管理员为故障应急入口，不可修改；角色变更会立即撤销该账号的会话并写入审计。
        </div>
        {accounts.length === 0 ? (
          <EmptyState icon={Users} title="暂无登录账号" text="用户通过 SSO 或密码登录后会自动出现在这里。" />
        ) : (
          <div className="table-wrap">
            <table className="project-user-table">
              <thead>
                <tr>
                  <th>账号</th>
                  <th>租户</th>
                  <th>来源</th>
                  <th>角色</th>
                  <th>状态</th>
                  <th>最近登录</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((item) => (
                  <tr key={item.id}>
                    <td>
                      <strong>
                        {accountDisplayName(item)}
                        {item.is_self && <span className="account-self-tag">当前账号</span>}
                      </strong>
                      <small className="cell-sub" title={item.id}>
                        {item.auth_source === 'oidc' ? 'SSO 登录' : `登录名 ${item.name}@${item.tenant_id}`}
                        {item.email ? ` · ${item.email}` : ''}
                      </small>
                    </td>
                    <td>
                      <strong className="text-xs">
                        {item.tenant_id === 'platform' ? '平台管理域' : item.tenant_name || item.tenant_id}
                      </strong>
                      <small className="cell-sub font-mono">{item.tenant_id}</small>
                    </td>
                    <td>
                      {item.is_builtin_admin ? (
                        <span className="badge badge-neutral">内置应急账号</span>
                      ) : (
                        <span className={`badge ${item.auth_source === 'oidc' ? 'badge-success' : 'badge-neutral'}`}>
                          {item.auth_source === 'oidc' ? '单点登录' : '本地密码'}
                        </span>
                      )}
                    </td>
                    <td>
                      {item.is_builtin_admin ? (
                        <span className="badge badge-info" title="唯一的内置超级管理员，不可修改">超级管理员</span>
                      ) : item.tenant_id === UNAFFILIATED_SLUG ? (
                        <span className="badge badge-neutral" title="角色在右侧指派时确定">待指派</span>
                      ) : (
                        <select
                          className="account-role-select"
                          value={roleOptions.includes(item.role as AccountRole) ? item.role : 'user'}
                          disabled={busy !== ''}
                          title="修改后立即生效"
                          aria-label={`修改 ${accountDisplayName(item)} 的角色`}
                          onChange={(event) => changeRole(item, event.target.value as AccountRole)}
                        >
                          {roleOptions.map((role) => (
                            <option key={role} value={role}>
                              {roleLabels[role]}
                            </option>
                          ))}
                        </select>
                      )}
                    </td>
                    <td>
                      <span className={`exec-led ${item.status === 'ACTIVE' ? 'ok' : 'deny'}`}>
                        <i aria-hidden="true" />
                        <span className={`badge ${item.status === 'ACTIVE' ? 'badge-success' : 'badge-neutral'}`}>
                          {item.status === 'ACTIVE' ? '可登录' : '已停用'}
                        </span>
                      </span>
                    </td>
                    <td className="font-mono text-xs">{formatTime(item.last_login_at)}</td>
                    <td>
                      {item.tenant_id === UNAFFILIATED_SLUG ? (
                        <AssignIdentityRow account={item} tenants={tenants} busy={busy !== ''} onAssigned={() => load(false)} />
                      ) : item.is_builtin_admin || item.is_self ? (
                        <span className="cell-sub">—</span>
                      ) : item.status === 'DISABLED' && item.auth_source === 'local' ? (
                        <button className="secondary-button" disabled={busy !== ''} onClick={() => reenableMember(item)}>
                          恢复启用
                        </button>
                      ) : (
                        <button className="secondary-button danger" disabled={busy !== ''} onClick={() => removeMember(item)}>
                          移出组织
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {filtered.length === 0 && <div className="empty-inline">没有符合条件的账号。</div>}
          </div>
        )}
      </section>
    </div>
  )
}
