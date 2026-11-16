import { useEffect, useState } from 'react'
import { ShieldCheck } from 'lucide-react'
import type { ApiMyProject } from '../../shared/api'
import { canUser, listMyProjects } from '../../shared/api'
import { useApp } from './Toast'

// "My Access" card: answers who-am-I / where-do-I-belong / what-can-I-do in one
// place, so members never have to infer privileges from error messages.
const projectRoleMeta: Record<string, { label: string; hint: string }> = {
  PROJECT_ADMIN: { label: '项目管理员', hint: '管理本项目成员与资源' },
  OPERATOR: { label: '操作员', hint: '可在本项目发起执行' },
  VIEWER: { label: '只读', hint: '仅查看，不能发起执行' },
}

const capabilityNotes: Array<{ scope: string; label: string }> = [
  { scope: 'pam:resources:read', label: '浏览本组织资源目录' },
  { scope: 'pam:executions:create', label: '发起受控执行（按项目权限）' },
  { scope: 'pam:executions:cancel:self', label: '取消自己发起的执行' },
  { scope: 'pam:approve', label: '审批他人的授权申请' },
  { scope: 'pam:audit:read', label: '查看本组织审计追踪' },
  { scope: 'pam:tenant:admin', label: '管理本组织资产、成员与策略' },
  { scope: 'pam:admin', label: '跨租户管理平台组织与账号' },
]

export function MyAccessCard() {
  const { auth } = useApp()
  const [myProjects, setMyProjects] = useState<ApiMyProject[]>([])
  const user = auth.user
  useEffect(() => {
    if (!user) return
    listMyProjects()
      .then((items) => setMyProjects(items ?? []))
      .catch(() => setMyProjects([]))
  }, [user?.id])
  if (!user) return null
  const roleLabel = canUser(user, 'pam:admin') ? '超级管理员' : canUser(user, 'pam:tenant:admin') ? '组织管理员' : '成员'
  const held = capabilityNotes.filter((c) => canUser(user, c.scope))
  return (
    <section className="panel my-access-card">
      <div className="ops-ph">
        <span>
          <ShieldCheck size={14} /> My Access · 我的权限
        </span>
      </div>
      <div className="my-access-grid">
        <div className="my-access-block">
          <small>所属组织</small>
          <strong>{user.tenant?.name || '未分配'}</strong>
          {user.tenant?.slug && <code>{user.tenant.slug}</code>}
        </div>
        <div className="my-access-block">
          <small>账号角色</small>
          <strong>{roleLabel}</strong>
        </div>
        <div className="my-access-block my-access-projects">
          <small>我的项目角色</small>
          {myProjects.length === 0 ? (
            <span className="muted">尚未加入任何项目</span>
          ) : (
            <ul>
              {myProjects.map((p) => {
                const meta = projectRoleMeta[p.my_role] ?? { label: p.my_role || '成员', hint: '' }
                return (
                  <li key={p.id} title={meta.hint}>
                    <span>{p.name}</span>
                    <em className={`project-role-chip role-${(p.my_role || '').toLowerCase()}`}>{meta.label}</em>
                  </li>
                )
              })}
            </ul>
          )}
        </div>
        <div className="my-access-block my-access-caps">
          <small>我能做什么</small>
          <ul>
            {held.map((c) => (
              <li key={c.scope}>{c.label}</li>
            ))}
          </ul>
        </div>
      </div>
    </section>
  )
}