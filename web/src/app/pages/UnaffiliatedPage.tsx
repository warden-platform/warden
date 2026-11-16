import { useCallback, useEffect, useState } from 'react'
import { myInvitations, acceptInvitation, type MyInvitation } from '../../shared/api'
import { useApp } from '../components/Toast'
import { WardenLogo } from '../components/WardenLogo'

const roleLabels: Record<string, string> = { user: '项目成员', tenant_admin: '租户管理员' }

// UnaffiliatedPage is the entire console for a federated SSO identity that has
// authenticated but has not been placed into any business organization yet. It
// exposes no business navigation (the session carries only the self-profile
// capability) and lists any pending invitations matched by the identity's
// trusted email, which the user can accept to join an organization.
export function UnaffiliatedPage() {
  const { auth, signOut, notify } = useApp()
  const user = auth.user
  const who = user?.display_name?.trim() || user?.email?.trim() || user?.name || ''
  const [invites, setInvites] = useState<MyInvitation[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')

  const refresh = useCallback(() => {
    setLoading(true)
    myInvitations()
      .then((items) => setInvites(items ?? []))
      .catch(() => setInvites([]))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  const accept = (invite: MyInvitation) => {
    if (!window.confirm(`确认加入「${invite.tenant_name}」，角色「${roleLabels[invite.role] ?? invite.role}」？\n\n加入后需要重新登录以启用新组织权限。`)) return
    setBusy(invite.id)
    acceptInvitation(invite.id)
      .then(() => {
        notify(`已加入 ${invite.tenant_name}，请重新登录`)
        setTimeout(() => signOut(), 1200)
      })
      .catch((e: Error) => notify(e.message || '接受邀请失败'))
      .finally(() => setBusy(''))
  }

  return (
    <div className="unaffiliated-screen">
      <div className="unaffiliated-card panel">
        <div className="unaffiliated-brand">
          <WardenLogo />
          <span>Warden</span>
        </div>
        <p className="eyebrow">IDENTITY VERIFIED · NO ORGANIZATION</p>
        <h1>身份已验证，但尚未加入任何组织</h1>
        <p className="unaffiliated-lede">
          你已通过单点登录确认了身份，但还没有被分配到任何业务组织。接受下方邀请，或等待管理员为你指派后，即可访问对应组织的资源与执行。
        </p>
        {who && (
          <div className="unaffiliated-who">
            <span className="muted">当前登录身份</span>
            <strong>{who}</strong>
          </div>
        )}

        <div className="unaffiliated-invites">
          <p className="eyebrow">PENDING INVITATIONS · 待处理邀请</p>
          {loading ? (
            <p className="muted">正在检查邀请…</p>
          ) : invites.length === 0 ? (
            <p className="muted">暂无待处理邀请。</p>
          ) : (
            invites.map((invite) => (
              <div className="unaffiliated-invite-row" key={invite.id}>
                <div>
                  <strong>{invite.tenant_name}</strong>
                  <small className="cell-sub">角色：{roleLabels[invite.role] ?? invite.role}</small>
                </div>
                <button className="secondary-button" disabled={busy !== ''} onClick={() => accept(invite)}>
                  {busy === invite.id ? '处理中…' : '接受邀请'}
                </button>
              </div>
            ))
          )}
        </div>

        <div className="unaffiliated-actions">
          <button className="secondary-button" onClick={() => signOut()}>
            退出登录
          </button>
        </div>
      </div>
    </div>
  )
}
