import { useEffect, useMemo, useState } from 'react'
import { KeyRound, LogOut, Mail, Power, RefreshCw, Search, UserRoundCheck, Users, X } from 'lucide-react'
import {
  ApiError,
  createInvitation,
  listInvitations,
  listProjectUsers,
  projectUserDisplayName,
  resetProjectUserPassword,
  revokeInvitation,
  revokeProjectUserSessions,
  updateProjectUserStatus,
  type ApiProjectUser,
  type Invitation,
} from '../../shared/api'
import { EmptyState, ErrorState, LoadingState } from '../components/States'
import { useApp } from '../components/Toast'

const roleLabels = { PROJECT_ADMIN: '项目管理员', OPERATOR: '操作员', VIEWER: '只读' } as const

const invitationRoleLabels: Record<string, string> = { user: '项目成员', tenant_admin: '租户管理员' }
const invitationStatusLabels: Record<string, string> = { PENDING: '待处理', ACCEPTED: '已加入', REVOKED: '已撤销' }

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString('zh-CN') : '从未登录'
}

// InvitationsPanel lets a tenant sub-administrator invite a trusted email into
// the current organization; the invitee accepts it from the unaffiliated
// waiting screen after their SSO login. Destination organization is the
// caller's own tenant (server-derived), so no slug is needed here.
function InvitationsPanel() {
  const { notify } = useApp()
  const [invites, setInvites] = useState<Invitation[]>([])
  const [loading, setLoading] = useState(true)
  const [email, setEmail] = useState('')
  const [role, setRole] = useState('user')
  const [busy, setBusy] = useState(false)

  const refresh = () => {
    setLoading(true)
    listInvitations()
      .then((items) => setInvites(items ?? []))
      .catch(() => setInvites([]))
      .finally(() => setLoading(false))
  }
  useEffect(() => {
    refresh()
  }, [])

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (!email.trim()) return
    setBusy(true)
    createInvitation({ email: email.trim(), role })
      .then((created) => {
        setInvites((items) => [created, ...items])
        setEmail('')
        notify(`已邀请 ${created.email}`)
      })
      .catch((e: ApiError) => notify(e.message || '创建邀请失败', 'error'))
      .finally(() => setBusy(false))
  }

  const revoke = (invite: Invitation) => {
    if (!window.confirm(`撤销对「${invite.email}」的邀请？`)) return
    setBusy(true)
    revokeInvitation(invite.id)
      .then(() => {
        setInvites((items) => items.map((item) => (item.id === invite.id ? { ...item, status: 'REVOKED' } : item)))
        notify('邀请已撤销')
      })
      .catch((e: ApiError) => notify(e.message || '撤销邀请失败', 'error'))
      .finally(() => setBusy(false))
  }

  return (
    <section className="panel">
      <div className="panel-header">
        <div>
          <h2>邀请成员</h2>
          <p>用可信邮箱邀请成员加入本组织。对方用单点登录进来后，会在「未分配」页面看到并接受邀请。</p>
        </div>
        <button className="icon-button" title="刷新邀请" aria-label="刷新邀请列表" onClick={refresh}>
          <RefreshCw size={16} />
        </button>
      </div>
      <form className="invitation-form" onSubmit={submit}>
        <label>
          <span className="muted">邮箱</span>
          <input type="email" required value={email} onChange={(e) => setEmail(e.target.value)} placeholder="member@company.com" />
        </label>
        <label>
          <span className="muted">角色</span>
          <select value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="user">项目成员</option>
            <option value="tenant_admin">租户管理员</option>
          </select>
        </label>
        <button className="primary-button" type="submit" disabled={busy || !email.trim()}>
          <Mail size={15} />
          发出邀请
        </button>
      </form>
      {loading ? (
        <div className="empty-inline">正在加载邀请…</div>
      ) : invites.length === 0 ? (
        <div className="empty-inline">还没有邀请记录。</div>
      ) : (
        <div className="table-wrap">
          <table className="project-user-table">
            <thead>
              <tr>
                <th>邮箱</th>
                <th>角色</th>
                <th>状态</th>
                <th>创建时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {invites.map((invite) => (
                <tr key={invite.id}>
                  <td>
                    <strong>{invite.email}</strong>
                  </td>
                  <td>{invitationRoleLabels[invite.role] ?? invite.role}</td>
                  <td>
                    <span className={`badge ${invite.status === 'PENDING' ? 'badge-info' : invite.status === 'ACCEPTED' ? 'badge-success' : 'badge-neutral'}`}>
                      {invitationStatusLabels[invite.status] ?? invite.status}
                    </span>
                  </td>
                  <td>{formatTime(invite.created_at)}</td>
                  <td>
                    {invite.status === 'PENDING' ? (
                      <button className="icon-button danger" title="撤销邀请" aria-label={`撤销对 ${invite.email} 的邀请`} disabled={busy} onClick={() => revoke(invite)}>
                        <X size={15} />
                      </button>
                    ) : (
                      <span className="cell-sub">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

export function ProjectUsersPage() {
  const { notify, auth } = useApp()
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
    listProjectUsers()
      .then((items) => setUsers(items ?? []))
      .catch((e: ApiError) => setError(e.message || '查询项目用户失败'))
      .finally(() => setLoading(false))
  }
  useEffect(() => {
    load()
  }, [])

  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    if (!keyword) return users
    return users.filter(
      (user) =>
        `${projectUserDisplayName(user)} ${user.name}`.toLowerCase().includes(keyword) ||
        user.memberships.some((membership) => membership.project_name.toLowerCase().includes(keyword)),
    )
  }, [query, users])
  const activeCount = users.filter((user) => user.status === 'ACTIVE').length
  const membershipCount = users.reduce(
    (total, user) => total + user.memberships.filter((membership) => membership.status === 'ACTIVE').length,
    0,
  )

  const toggleStatus = (user: ApiProjectUser) => {
    const next = user.status === 'ACTIVE' ? 'DISABLED' : 'ACTIVE'
    const verb = next === 'DISABLED' ? '停用' : '启用'
    if (
      !window.confirm(
        `确认${verb}项目用户「${projectUserDisplayName(user)}」？${next === 'DISABLED' ? '该用户的全部登录会话会立即失效。' : ''}`,
      )
    )
      return
    setBusy(`status-${user.id}`)
    updateProjectUserStatus(user.id, next)
      .then((updated) => {
        setUsers((items) => items.map((item) => (item.id === updated.id ? updated : item)))
        notify(`已${verb}项目用户`)
      })
      .catch((e: ApiError) => notify(e.message || `${verb}项目用户失败`, 'error'))
      .finally(() => setBusy(''))
  }

  const revokeSessions = (user: ApiProjectUser) => {
    if (!window.confirm(`撤销「${projectUserDisplayName(user)}」的全部登录会话？`)) return
    setBusy(`sessions-${user.id}`)
    revokeProjectUserSessions(user.id)
      .then(({ revoked_sessions }) => {
        notify(revoked_sessions > 0 ? `已撤销 ${revoked_sessions} 个会话` : '当前没有有效会话')
      })
      .catch((e: ApiError) => notify(e.message || '撤销会话失败', 'error'))
      .finally(() => setBusy(''))
  }

  const resetPassword = (event: React.FormEvent) => {
    event.preventDefault()
    if (!resetUser) return
    setBusy(`password-${resetUser.id}`)
    resetProjectUserPassword(resetUser.id, password)
      .then(({ revoked_sessions }) => {
        notify(revoked_sessions > 0 ? `密码已重置，并撤销 ${revoked_sessions} 个会话` : '密码已重置')
        setResetUser(null)
        setPassword('')
      })
      .catch((e: ApiError) => notify(e.message || '重置密码失败', 'error'))
      .finally(() => setBusy(''))
  }

  if (loading) return <LoadingState label="正在加载项目用户" />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  return (
    <div className="view-stack">
      {auth.user?.is_platform_admin && (
        <p className="resource-access-note">
          超级管理员视角：本页按租户隔离，仅显示当前组织（{auth.user.tenant?.name || 'Platform'}）的项目用户。
          业务组织的成员请在「账号与权限」跨租户管理，或以该组织管理员登录查看详情。
        </p>
      )}
      <section className="project-user-summary" aria-label="项目用户概况">
        <div>
          <strong>{users.length}</strong>
          <span>项目用户</span>
        </div>
        <div>
          <strong>{activeCount}</strong>
          <span>可登录</span>
        </div>
        <div>
          <strong>{users.length - activeCount}</strong>
          <span>已停用</span>
        </div>
        <div>
          <strong>{membershipCount}</strong>
          <span>有效项目关系</span>
        </div>
      </section>
      <section className="panel">
        <div className="panel-header">
          <div>
            <h2>项目用户</h2>
            <p>管理当前租户内通过项目成员关系获得权限的登录用户。</p>
          </div>
          <button className="icon-button" title="刷新" aria-label="刷新项目用户" onClick={() => load(false)}>
            <RefreshCw size={16} />
          </button>
        </div>
        <div className="toolbar">
          <label className="search-box">
            <Search size={15} />
            <span className="sr-only">搜索项目用户</span>
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索用户名或项目" />
          </label>
          <span className="project-user-result-count">{filtered.length} 个用户</span>
        </div>
        {users.length === 0 ? (
          <EmptyState icon={Users} title="暂无项目用户" text="可在项目成员管理中创建项目登录用户。" />
        ) : (
          <div className="table-wrap">
            <table className="project-user-table">
              <thead>
                <tr>
                  <th>用户</th>
                  <th>状态</th>
                  <th>项目与角色</th>
                  <th>最近登录</th>
                  <th>更新时间</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((user) => (
                  <tr key={user.id}>
                    <td>
                      <strong>{projectUserDisplayName(user)}</strong>
                      <small className="cell-sub" title={user.id}>
                        {user.display_name ? user.name : user.id}
                      </small>
                    </td>
                    <td>
                      <span className={`exec-led ${user.status === 'ACTIVE' ? 'ok' : 'deny'}`}>
                        <i aria-hidden="true" />
                        <span className={`badge ${user.status === 'ACTIVE' ? 'badge-success' : 'badge-neutral'}`}>
                          {user.status === 'ACTIVE' ? '可登录' : '已停用'}
                        </span>
                      </span>
                    </td>
                    <td>
                      <div className="project-user-memberships">
                        {user.memberships.length === 0 ? (
                          <span className="muted-copy">待分配</span>
                        ) : (
                          user.memberships.map((membership) => (
                            <span
                              className={`membership-chip ${membership.status === 'DISABLED' ? 'disabled' : ''}`}
                              key={membership.project_id}
                            >
                              {membership.project_name}
                              <small>
                                {roleLabels[membership.role]}
                                {membership.status === 'DISABLED' ? ' · 已停用' : ''}
                              </small>
                            </span>
                          ))
                        )}
                      </div>
                    </td>
                    <td>{formatTime(user.last_login_at)}</td>
                    <td>{formatTime(user.updated_at)}</td>
                    <td>
                      <div className="row-actions">
                        <button
                          className="icon-button"
                          title="重置密码"
                          aria-label={`重置 ${projectUserDisplayName(user)} 的密码`}
                          disabled={busy !== ''}
                          onClick={() => {
                            setResetUser(user)
                            setPassword('')
                          }}
                        >
                          <KeyRound size={15} />
                        </button>
                        <button
                          className="icon-button"
                          title="撤销会话"
                          aria-label={`撤销 ${projectUserDisplayName(user)} 的会话`}
                          disabled={busy !== ''}
                          onClick={() => revokeSessions(user)}
                        >
                          <LogOut size={15} />
                        </button>
                        <button
                          className={`icon-button ${user.status === 'ACTIVE' ? 'danger' : ''}`}
                          title={user.status === 'ACTIVE' ? '停用用户' : '启用用户'}
                          aria-label={`${user.status === 'ACTIVE' ? '停用' : '启用'} ${projectUserDisplayName(user)}`}
                          disabled={busy !== ''}
                          onClick={() => toggleStatus(user)}
                        >
                          {user.status === 'ACTIVE' ? <Power size={15} /> : <UserRoundCheck size={15} />}
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {filtered.length === 0 && <div className="empty-inline">没有符合条件的项目用户。</div>}
          </div>
        )}
      </section>
      <InvitationsPanel />
      {resetUser && (
        <div className="modal-layer" role="presentation">
          <button className="modal-scrim" aria-label="关闭密码重置" onClick={() => setResetUser(null)} />
          <form
            className="secret-dialog project-user-password-dialog form-grid"
            role="dialog"
            aria-modal="true"
            aria-labelledby="reset-project-user-password"
            onSubmit={resetPassword}
          >
            <div className="dialog-heading">
              <div>
                <span className="eyebrow">账号安全</span>
                <h2 id="reset-project-user-password">重置 {projectUserDisplayName(resetUser)} 的密码</h2>
              </div>
              <button type="button" className="icon-button" aria-label="关闭" onClick={() => setResetUser(null)}>
                <X size={18} />
              </button>
            </div>
            <label>
              <span>新密码</span>
              <input
                type="password"
                autoComplete="new-password"
                minLength={8}
                maxLength={72}
                required
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </label>
            <div className="form-actions">
              <button type="button" className="quiet-button" onClick={() => setResetUser(null)}>
                取消
              </button>
              <button className="primary-button" disabled={password.length < 8 || busy === `password-${resetUser.id}`}>
                <KeyRound size={15} />
                重置密码
              </button>
            </div>
          </form>
        </div>
      )}
    </div>
  )
}
