import { useState } from 'react'
import { Boxes, FolderKanban, Network, Pencil, Plus, RefreshCw, ServerCog, Trash2, Users, X } from 'lucide-react'
import { useNavigate } from '../../shared/router'
import {
  ApiError,
  addProjectMember,
  apiPrincipalDisplayName,
  createProject,
  createProjectUser,
  deleteProject,
  getProjectMembershipPolicy,
  getResourceStats,
  listConnectors,
  listIntegrations,
  listProjectMemberCandidates,
  listProjectMembers,
  listProjects,
  membershipDisplayName,
  removeProjectMember,
  updateProjectMembershipPolicy,
  updateProjectMember,
  updateProject,
  type ApiProject,
  type ApiProjectMembership,
  type ApiPrincipal,
} from '../../shared/api'
import { canUser } from '../../shared/api'
import { MyAccessCard } from '../components/MyAccessCard'
import { ErrorState, LoadingState } from '../components/States'
import { Kpi } from '../components/Kpi'
import { useApp } from '../components/Toast'
import { useApiQuery } from '../lib/useApiQuery'

export function ProjectsPage() {
  const navigate = useNavigate()
  const { notify, auth } = useApp()
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [editing, setEditing] = useState<ApiProject | null>(null)
  const [editName, setEditName] = useState('')
  const [editDescription, setEditDescription] = useState('')
  const [busy, setBusy] = useState('')
  const [memberProject, setMemberProject] = useState<ApiProject | null>(null)
  const [members, setMembers] = useState<ApiProjectMembership[]>([])
  const [candidates, setCandidates] = useState<ApiPrincipal[]>([])
  const [memberRole, setMemberRole] = useState<ApiProjectMembership['role']>('VIEWER')
  const [candidateID, setCandidateID] = useState('')
  const [newUserName, setNewUserName] = useState('')
  const [newUserPassword, setNewUserPassword] = useState('')
  const [policyEnforced, setPolicyEnforced] = useState(false)
  const isTenantAdmin = canUser(auth.user, 'warden:tenant:admin')
  const canManageMembers =
    isTenantAdmin ||
    Boolean(
      auth.user &&
      members.some((member) => member.principal_id === auth.user?.id && member.role === 'PROJECT_ADMIN' && member.status === 'ACTIVE'),
    )

  const projectsQuery = useApiQuery(
    () =>
      Promise.all([
        listProjects(),
        getResourceStats(),
        isTenantAdmin ? listConnectors() : Promise.resolve([]),
        isTenantAdmin ? listIntegrations() : Promise.resolve([]),
        getProjectMembershipPolicy(),
      ]),
    [isTenantAdmin],
    { onSuccess: ([, , , , policy]) => setPolicyEnforced(policy.enforced) },
  )
  const projects = projectsQuery.data?.[0] ?? []
  const resourceStats = projectsQuery.data?.[1] ?? null
  const connectors = projectsQuery.data?.[2] ?? []
  const integrations = projectsQuery.data?.[3] ?? []
  const loading = projectsQuery.loading
  const error = projectsQuery.error
  const load = (spinner = true) => projectsQuery.reload(spinner)

  const openMembers = (project: ApiProject) => {
    setMemberProject(project)
    setMembers([])
    setCandidates([])
    setCandidateID('')
    listProjectMembers(project.id)
      .then((items) => {
        setMembers(items ?? [])
        const canManage =
          isTenantAdmin ||
          Boolean(
            auth.user &&
            items.some((member) => member.principal_id === auth.user?.id && member.role === 'PROJECT_ADMIN' && member.status === 'ACTIVE'),
          )
        if (canManage) return listProjectMemberCandidates(project.id).then((people) => setCandidates(people ?? []))
      })
      .catch((e: ApiError) => notify(e.message || '加载成员失败', 'error'))
  }
  const addMember = () => {
    if (!memberProject || !candidateID) return
    setBusy(`member-add-${memberProject.id}`)
    addProjectMember(memberProject.id, { principalId: candidateID, role: memberRole })
      .then((item) => {
        setMembers((items) => [...items.filter((value) => value.principal_id !== item.principal_id), item])
        setCandidates((items) => items.filter((value) => value.id !== item.principal_id))
        setCandidateID('')
        notify('成员已添加')
      })
      .catch((e: ApiError) => notify(e.message || '添加成员失败', 'error'))
      .finally(() => setBusy(''))
  }
  const createUser = () => {
    if (!memberProject || !newUserName.trim() || newUserPassword.length < 8) return
    setBusy(`user-create-${memberProject.id}`)
    createProjectUser(memberProject.id, { name: newUserName.trim(), password: newUserPassword, role: memberRole })
      .then(({ membership }) => {
        setMembers((items) => [...items.filter((value) => value.principal_id !== membership.principal_id), membership])
        setNewUserName('')
        setNewUserPassword('')
        notify(`项目用户已创建，登录名 ${newUserName.trim()}@${auth.user?.tenant?.slug ?? 'platform'}`)
        return listProjectMemberCandidates(memberProject.id).then((people) => setCandidates(people ?? []))
      })
      .catch((e: ApiError) => notify(e.message || '创建项目用户失败', 'error'))
      .finally(() => setBusy(''))
  }
  const changeMember = (member: ApiProjectMembership, role: ApiProjectMembership['role'], status: ApiProjectMembership['status']) => {
    if (!memberProject) return
    setBusy(member.principal_id)
    updateProjectMember(memberProject.id, member.principal_id, { role, status })
      .then((item) => {
        setMembers((items) => items.map((value) => (value.principal_id === item.principal_id ? item : value)))
        notify('成员已更新')
      })
      .catch((e: ApiError) => notify(e.message || '更新成员失败', 'error'))
      .finally(() => setBusy(''))
  }
  const disableMember = (member: ApiProjectMembership) => {
    if (!memberProject || !window.confirm(`停用成员「${member.principal_name}」？`)) return
    setBusy(member.principal_id)
    removeProjectMember(memberProject.id, member.principal_id)
      .then(() => {
        setMembers((items) => items.map((value) => (value.principal_id === member.principal_id ? { ...value, status: 'DISABLED' } : value)))
        notify('成员已停用')
      })
      .catch((e: ApiError) => notify(e.message || '停用成员失败', 'error'))
      .finally(() => setBusy(''))
  }
  const togglePolicy = () => {
    if (
      !isTenantAdmin ||
      !window.confirm(policyEnforced ? '关闭后普通用户将恢复兼容模式，确认关闭？' : '开启后普通用户只能访问所属项目，确认开启？')
    )
      return
    updateProjectMembershipPolicy(!policyEnforced)
      .then((value) => {
        setPolicyEnforced(value.enforced)
        notify(value.enforced ? '项目成员权限已生效' : '已恢复兼容模式')
      })
      .catch((e: ApiError) => notify(e.message || '更新策略失败', 'error'))
  }

  const activeResources = resourceStats?.current ?? 0
  const resourceCount = (id: string) =>
    Object.values(resourceStats?.catalogByProjectDomainStatus[id] ?? {}).reduce(
      (total, statuses) =>
        total + Object.entries(statuses).reduce((domainTotal, [status, count]) => domainTotal + (status === 'ORPHANED' ? 0 : count), 0),
      0,
    )
  const projectStats = (project: ApiProject) => ({
    resources: resourceCount(project.id),
    connectors: connectors.filter((connector) => connector.project_id === project.id || (project.is_default && !connector.project_id))
      .length,
    integrations: integrations.filter(
      (integration) =>
        integration.project_id === project.id ||
        (project.is_default && !integration.project_id) ||
        integration.project_ids.includes(project.id),
    ).length,
  })

  const create = (event: React.FormEvent) => {
    event.preventDefault()
    setBusy('create')
    createProject({ name: name.trim(), description: description.trim() })
      .then(() => {
        notify('项目已创建')
        setName('')
        setDescription('')
        setCreating(false)
        load(false)
      })
      .catch((e: ApiError) => notify(e.message || '创建项目失败', 'error'))
      .finally(() => setBusy(''))
  }
  const save = (event: React.FormEvent) => {
    event.preventDefault()
    if (!editing) return
    setBusy(editing.id)
    updateProject(editing.id, { name: editName.trim(), description: editDescription.trim() })
      .then(() => {
        notify('项目已更新')
        setEditing(null)
        load(false)
      })
      .catch((e: ApiError) => notify(e.message || '更新项目失败', 'error'))
      .finally(() => setBusy(''))
  }
  const remove = (project: ApiProject) => {
    if (!window.confirm(`确认删除项目「${project.name}」？关联资源将按继承规则回落到默认项目。`)) return
    setBusy(project.id)
    deleteProject(project.id)
      .then(() => {
        notify('项目已删除')
        load(false)
      })
      .catch((e: ApiError) => notify(e.message || '删除项目失败', 'error'))
      .finally(() => setBusy(''))
  }

  if (loading) return <LoadingState label="正在加载项目" />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  return (
    <div className="view-stack">
      {!isTenantAdmin && <MyAccessCard />}
      <section className="kpi-grid project-kpis">
        <Kpi icon={FolderKanban} tone="blue" label="项目" value={projects.length} detail="含租户默认项目" />
        <Kpi icon={Boxes} tone="green" label="当前资源" value={activeResources} detail={`${resourceStats?.abnormal ?? 0} 个异常资源`} />
        <Kpi icon={Network} tone="amber" label="执行通道" value={connectors.length} detail="项目是业务归属，不限定执行位置" />
        <Kpi icon={ServerCog} tone="red" label="中间件" value={integrations.length} detail="可配置多项目服务范围" />
      </section>
      <section className="panel">
        <div className="panel-header">
          <div>
            <h2>项目目录</h2>
            <p>项目是租户内资源归属、策略匹配和服务授权的治理边界。</p>
            {policyEnforced ? (
              <span className="badge badge-success">项目成员权限已生效</span>
            ) : (
              <span className="badge badge-info">兼容模式：尚未按成员限制</span>
            )}
          </div>
          <div className="header-actions">
            <button className="icon-button" aria-label="刷新项目" title="刷新" onClick={() => load()}>
              <RefreshCw size={16} />
            </button>
            {isTenantAdmin && (
              <>
                <button className="quiet-button" onClick={togglePolicy}>
                  {policyEnforced ? '关闭成员强制' : '开启成员强制'}
                </button>
                <button className="primary-button compact" onClick={() => setCreating((value) => !value)}>
                  <Plus size={16} />
                  新建项目
                </button>
              </>
            )}
          </div>
        </div>
        {creating && (
          <form className="project-page-form form-grid" onSubmit={create}>
            <label>
              <span>项目名称</span>
              <input required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} placeholder="例如订单平台" />
            </label>
            <label>
              <span>项目描述</span>
              <input
                maxLength={500}
                value={description}
                onChange={(event) => setDescription(event.target.value)}
                placeholder="负责的系统和资源范围"
              />
            </label>
            <div className="form-actions">
              <button type="button" className="quiet-button" onClick={() => setCreating(false)}>
                取消
              </button>
              <button className="primary-button" disabled={busy === 'create'}>
                <Plus size={15} />
                创建
              </button>
            </div>
          </form>
        )}
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>项目</th>
                <th>资源</th>
                <th>执行通道</th>
                <th>中间件范围</th>
                <th>更新时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {projects.map((project) => {
                const stats = projectStats(project)
                return (
                  <tr key={project.id}>
                    <td>
                      <strong>{project.name}</strong>
                      {project.is_default && <span className="badge badge-info project-default-badge">默认</span>}
                      <small className="cell-sub">{project.description || '暂无描述'}</small>
                    </td>
                    <td>
                      <strong>{stats.resources}</strong>
                    </td>
                    <td>{stats.connectors}</td>
                    <td>{stats.integrations}</td>
                    <td>{new Date(project.updated_at).toLocaleDateString('zh-CN')}</td>
                    <td>
                      <div className="row-actions">
                        <button className="quiet-button" onClick={() => navigate(`/resources?project=${encodeURIComponent(project.id)}`)}>
                          查看资源
                        </button>
                        <button
                          className="icon-button"
                          title="管理成员"
                          aria-label={`管理项目成员 ${project.name}`}
                          onClick={() => openMembers(project)}
                        >
                          <Users size={15} />
                        </button>
                        {isTenantAdmin && !project.is_default && (
                          <>
                            <button
                              className="icon-button"
                              title="编辑"
                              aria-label={`编辑项目 ${project.name}`}
                              onClick={() => {
                                setEditing(project)
                                setEditName(project.name)
                                setEditDescription(project.description)
                              }}
                            >
                              <Pencil size={15} />
                            </button>
                            <button
                              className="icon-button danger"
                              title="删除"
                              aria-label={`删除项目 ${project.name}`}
                              disabled={busy === project.id}
                              onClick={() => remove(project)}
                            >
                              <Trash2 size={15} />
                            </button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </section>
      {editing && (
        <div className="modal-layer" role="presentation">
          <button className="modal-scrim" aria-label="关闭项目编辑" onClick={() => setEditing(null)} />
          <form className="secret-dialog project-edit-dialog form-grid" role="dialog" aria-modal="true" onSubmit={save}>
            <div className="dialog-heading">
              <div>
                <span className="eyebrow">项目设置</span>
                <h2>编辑 {editing.name}</h2>
              </div>
              <button type="button" className="icon-button" aria-label="关闭" onClick={() => setEditing(null)}>
                <X size={18} />
              </button>
            </div>
            <label>
              <span>项目名称</span>
              <input required maxLength={100} value={editName} onChange={(event) => setEditName(event.target.value)} />
            </label>
            <label>
              <span>项目描述</span>
              <textarea rows={4} maxLength={500} value={editDescription} onChange={(event) => setEditDescription(event.target.value)} />
            </label>
            <div className="form-actions">
              <button type="button" className="quiet-button" onClick={() => setEditing(null)}>
                取消
              </button>
              <button className="primary-button" disabled={busy === editing.id}>
                保存
              </button>
            </div>
          </form>
        </div>
      )}
      {memberProject && (
        <div className="modal-layer" role="presentation">
          <button className="modal-scrim" aria-label="关闭成员管理" onClick={() => setMemberProject(null)} />
          <section className="secret-dialog project-member-dialog" role="dialog" aria-modal="true">
            <div className="dialog-heading">
              <div>
                <span className="eyebrow">项目成员</span>
                <h2>{memberProject.name}</h2>
              </div>
              <button className="icon-button" aria-label="关闭" onClick={() => setMemberProject(null)}>
                <X size={18} />
              </button>
            </div>
            {canManageMembers && (
              <>
                <div className="member-add-row">
                  <select value={candidateID} onChange={(event) => setCandidateID(event.target.value)}>
                    <option value="">选择现有身份</option>
                    {candidates.map((candidate) => (
                      <option key={candidate.id} value={candidate.id}>
                        {apiPrincipalDisplayName(candidate)}（{candidate.kind}）
                      </option>
                    ))}
                  </select>
                  <select value={memberRole} onChange={(event) => setMemberRole(event.target.value as ApiProjectMembership['role'])}>
                    <option value="PROJECT_ADMIN">项目管理员</option>
                    <option value="OPERATOR">操作员</option>
                    <option value="VIEWER">只读</option>
                  </select>
                  <button className="primary-button" disabled={!candidateID || busy.startsWith('member-add-')} onClick={addMember}>
                    添加
                  </button>
                </div>
                {isTenantAdmin && (
                  <div className="member-add-row member-user-create-row">
                    <input placeholder="新用户登录名" value={newUserName} onChange={(event) => setNewUserName(event.target.value)} />
                    <input
                      type="password"
                      minLength={8}
                      placeholder="初始密码（至少 8 位）"
                      value={newUserPassword}
                      onChange={(event) => setNewUserPassword(event.target.value)}
                    />
                    <button
                      className="quiet-button"
                      disabled={!newUserName.trim() || newUserPassword.length < 8 || busy.startsWith('user-create-')}
                      onClick={createUser}
                    >
                      创建登录用户
                    </button>
                  </div>
                )}
              </>
            )}
            <div className="member-list">
              {members.length === 0 ? (
                <div className="empty-inline">还没有成员。</div>
              ) : (
                members.map((member) => (
                  <div className="member-row" key={member.principal_id}>
                    <div>
                      <strong>{membershipDisplayName(member)}</strong>
                      <small>
                        {member.principal_kind} · {member.status === 'ACTIVE' ? '有效' : '已停用'}
                      </small>
                    </div>
                    <div className="member-controls">
                      <select
                        disabled={!canManageMembers || busy === member.principal_id}
                        value={member.role}
                        onChange={(event) => changeMember(member, event.target.value as ApiProjectMembership['role'], member.status)}
                      >
                        <option value="PROJECT_ADMIN">项目管理员</option>
                        <option value="OPERATOR">操作员</option>
                        <option value="VIEWER">只读</option>
                      </select>
                      {member.status === 'ACTIVE' ? (
                        <button
                          className="quiet-button"
                          disabled={!canManageMembers || busy === member.principal_id}
                          onClick={() => disableMember(member)}
                        >
                          停用
                        </button>
                      ) : (
                        <button
                          className="quiet-button"
                          disabled={!canManageMembers || busy === member.principal_id}
                          onClick={() => changeMember(member, member.role, 'ACTIVE')}
                        >
                          恢复
                        </button>
                      )}
                    </div>
                  </div>
                ))
              )}
            </div>
          </section>
        </div>
      )}
    </div>
  )
}
