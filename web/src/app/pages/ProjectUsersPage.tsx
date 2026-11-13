import { useEffect, useMemo, useState } from 'react'
import { KeyRound, LogOut, Power, RefreshCw, Search, UserRoundCheck, Users, X } from 'lucide-react'
import {
  ApiError, listProjectUsers, resetProjectUserPassword, revokeProjectUserSessions, updateProjectUserStatus,
  type ApiProjectUser,
} from '../../shared/api'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { useApp } from '../components/Toast'

const roleLabels = { PROJECT_ADMIN: '项目管理员', OPERATOR: '操作员', VIEWER: '只读' } as const

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString('zh-CN') : '从未登录'
}

export function ProjectUsersPage() {
  const { notify } = useApp()
  const [users, setUsers] = useState<ApiProjectUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [busy, setBusy] = useState('')
  const [resetUser, setResetUser] = useState<ApiProjectUser | null>(null)
  const [password, setPassword] = useState('')

  const load = (spinner = true) => {
    if (spinner) setLoading(true)
    setError('')
    listProjectUsers().then((items) => setUsers(items ?? []))
      .catch((e: ApiError) => setError(e.message || '查询项目用户失败'))
      .finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [])

  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    if (!keyword) return users
    return users.filter((user) => user.name.toLowerCase().includes(keyword) ||
      user.memberships.some((membership) => membership.project_name.toLowerCase().includes(keyword)))
  }, [query, users])
  const activeCount = users.filter((user) => user.status === 'ACTIVE').length
  const membershipCount = users.reduce((total, user) => total + user.memberships.filter((membership) => membership.status === 'ACTIVE').length, 0)

  const toggleStatus = (user: ApiProjectUser) => {
    const next = user.status === 'ACTIVE' ? 'DISABLED' : 'ACTIVE'
    const verb = next === 'DISABLED' ? '停用' : '启用'
    if (!window.confirm(`确认${verb}项目用户「${user.name}」？${next === 'DISABLED' ? '该用户的全部登录会话会立即失效。' : ''}`)) return
    setBusy(`status-${user.id}`)
    updateProjectUserStatus(user.id, next).then((updated) => {
      setUsers((items) => items.map((item) => item.id === updated.id ? updated : item))
      notify(`已${verb}项目用户`)
    }).catch((e: ApiError) => notify(e.message || `${verb}项目用户失败`)).finally(() => setBusy(''))
  }

  const revokeSessions = (user: ApiProjectUser) => {
    if (!window.confirm(`撤销「${user.name}」的全部登录会话？`)) return
    setBusy(`sessions-${user.id}`)
    revokeProjectUserSessions(user.id).then(({ revoked_sessions }) => {
      notify(revoked_sessions > 0 ? `已撤销 ${revoked_sessions} 个会话` : '当前没有有效会话')
    }).catch((e: ApiError) => notify(e.message || '撤销会话失败')).finally(() => setBusy(''))
  }

  const resetPassword = (event: React.FormEvent) => {
    event.preventDefault()
    if (!resetUser) return
    setBusy(`password-${resetUser.id}`)
    resetProjectUserPassword(resetUser.id, password).then(({ revoked_sessions }) => {
      notify(revoked_sessions > 0 ? `密码已重置，并撤销 ${revoked_sessions} 个会话` : '密码已重置')
      setResetUser(null); setPassword('')
    }).catch((e: ApiError) => notify(e.message || '重置密码失败')).finally(() => setBusy(''))
  }

  if (loading) return <LoadingState label="正在加载项目用户" />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  return <div className="view-stack">
    <section className="project-user-summary" aria-label="项目用户概况">
      <div><strong>{users.length}</strong><span>项目用户</span></div>
      <div><strong>{activeCount}</strong><span>可登录</span></div>
      <div><strong>{users.length - activeCount}</strong><span>已停用</span></div>
      <div><strong>{membershipCount}</strong><span>有效项目关系</span></div>
    </section>
    <section className="panel">
      <div className="panel-header"><div><h2>项目用户</h2><p>管理当前租户内通过项目成员关系获得权限的登录用户。</p></div><button className="icon-button" title="刷新" aria-label="刷新项目用户" onClick={() => load(false)}><RefreshCw size={16} /></button></div>
      <div className="toolbar"><label className="search-box"><Search size={15} /><span className="sr-only">搜索项目用户</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索用户名或项目" /></label><span className="project-user-result-count">{filtered.length} 个用户</span></div>
      {users.length === 0 ? <EmptyState icon={Users} title="暂无项目用户" text="可在项目成员管理中创建项目登录用户。" /> :
        <div className="table-wrap"><table className="project-user-table"><thead><tr><th>用户</th><th>状态</th><th>项目与角色</th><th>最近登录</th><th>更新时间</th><th>操作</th></tr></thead><tbody>{filtered.map((user) => <tr key={user.id}>
          <td><strong>{user.name}</strong><small className="cell-sub">{user.id}</small></td>
          <td><span className={`badge ${user.status === 'ACTIVE' ? 'badge-success' : 'badge-neutral'}`}>{user.status === 'ACTIVE' ? '可登录' : '已停用'}</span></td>
          <td><div className="project-user-memberships">{user.memberships.length === 0 ? <span className="muted-copy">待分配</span> : user.memberships.map((membership) => <span className={`membership-chip ${membership.status === 'DISABLED' ? 'disabled' : ''}`} key={membership.project_id}>{membership.project_name}<small>{roleLabels[membership.role]}{membership.status === 'DISABLED' ? ' · 已停用' : ''}</small></span>)}</div></td>
          <td>{formatTime(user.last_login_at)}</td><td>{formatTime(user.updated_at)}</td>
          <td><div className="row-actions">
            <button className="icon-button" title="重置密码" aria-label={`重置 ${user.name} 的密码`} disabled={busy !== ''} onClick={() => { setResetUser(user); setPassword('') }}><KeyRound size={15} /></button>
            <button className="icon-button" title="撤销会话" aria-label={`撤销 ${user.name} 的会话`} disabled={busy !== ''} onClick={() => revokeSessions(user)}><LogOut size={15} /></button>
            <button className={`icon-button ${user.status === 'ACTIVE' ? 'danger' : ''}`} title={user.status === 'ACTIVE' ? '停用用户' : '启用用户'} aria-label={`${user.status === 'ACTIVE' ? '停用' : '启用'} ${user.name}`} disabled={busy !== ''} onClick={() => toggleStatus(user)}>{user.status === 'ACTIVE' ? <Power size={15} /> : <UserRoundCheck size={15} />}</button>
          </div></td>
        </tr>)}</tbody></table>{filtered.length === 0 && <div className="empty-inline">没有符合条件的项目用户。</div>}</div>}
    </section>
    {resetUser && <div className="modal-layer" role="presentation"><button className="modal-scrim" aria-label="关闭密码重置" onClick={() => setResetUser(null)} /><form className="secret-dialog project-user-password-dialog form-grid" role="dialog" aria-modal="true" aria-labelledby="reset-project-user-password" onSubmit={resetPassword}><div className="dialog-heading"><div><span className="eyebrow">账号安全</span><h2 id="reset-project-user-password">重置 {resetUser.name} 的密码</h2></div><button type="button" className="icon-button" aria-label="关闭" onClick={() => setResetUser(null)}><X size={18} /></button></div><label><span>新密码</span><input type="password" autoComplete="new-password" minLength={8} maxLength={72} required value={password} onChange={(event) => setPassword(event.target.value)} /></label><div className="form-actions"><button type="button" className="quiet-button" onClick={() => setResetUser(null)}>取消</button><button className="primary-button" disabled={password.length < 8 || busy === `password-${resetUser.id}`}><KeyRound size={15} />重置密码</button></div></form></div>}
  </div>
}
