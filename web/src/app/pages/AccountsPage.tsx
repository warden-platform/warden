import { useEffect, useMemo, useState } from 'react'
import { RefreshCw, Search, ShieldCheck, Users } from 'lucide-react'
import { accountDisplayName, ApiError, listAccounts, updateAccountRole, type AccountRole, type ApiAccount } from '../../shared/api'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { useApp } from '../components/Toast'

const roleLabels: Record<string, string> = {
  user: '项目成员',
  tenant_admin: '租户管理员',
  platform_admin: '平台管理员',
}
const roleOptions: AccountRole[] = ['user', 'tenant_admin', 'platform_admin']

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
  }, [])

  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    if (!keyword) return accounts
    return accounts.filter((item) =>
      `${accountDisplayName(item)} ${item.name} ${item.email} ${item.provider_subject ?? ''}`.toLowerCase().includes(keyword),
    )
  }, [query, accounts])
  const ssoCount = accounts.filter((item) => item.auth_source === 'oidc').length
  const adminCount = accounts.filter((item) => item.role === 'platform_admin').length

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
      .catch((e: ApiError) => notify(e.message || '更新角色失败'))
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
          <strong>{ssoCount}</strong>
          <span>单点登录</span>
        </div>
        <div>
          <strong>{adminCount}</strong>
          <span>平台管理员</span>
        </div>
        <div>
          <strong>{accounts.filter((item) => item.is_builtin_admin).length}</strong>
          <span>应急超管</span>
        </div>
      </section>
      <section className="panel">
        <div className="panel-header">
          <div>
            <h2>账号与权限</h2>
            <p>IdP 只负责认证，角色由平台管理员在这里指派。首次 SSO 登录默认最小权限，手工指派的角色不会被下次登录覆盖。</p>
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
                  <th>来源</th>
                  <th>角色</th>
                  <th>状态</th>
                  <th>最近登录</th>
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
                        {item.display_name ? item.name : item.id}
                        {item.email ? ` · ${item.email}` : ''}
                      </small>
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
                      <select
                        className="account-role-select"
                        value={roleOptions.includes(item.role as AccountRole) ? item.role : 'user'}
                        disabled={item.is_builtin_admin || busy !== ''}
                        title={item.is_builtin_admin ? '内置应急账号不可修改' : '修改后立即生效'}
                        aria-label={`修改 ${accountDisplayName(item)} 的角色`}
                        onChange={(event) => changeRole(item, event.target.value as AccountRole)}
                      >
                        {roleOptions.map((role) => (
                          <option key={role} value={role}>
                            {roleLabels[role]}
                          </option>
                        ))}
                      </select>
                    </td>
                    <td>
                      <span className={`badge ${item.status === 'ACTIVE' ? 'badge-success' : 'badge-neutral'}`}>
                        {item.status === 'ACTIVE' ? '可登录' : '已停用'}
                      </span>
                    </td>
                    <td>{formatTime(item.last_login_at)}</td>
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
