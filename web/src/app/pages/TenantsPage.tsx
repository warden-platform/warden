import { useEffect, useMemo, useState } from 'react'
import { Building2, Gauge, Loader2, Plus, RefreshCw, ShieldCheck, Users, X } from 'lucide-react'
import {
  ApiError,
  bootstrapTenant,
  createTenantMember,
  disableTenant,
  getTenant,
  listTenantMembers,
  listTenants,
  updateAccountRole,
  updateTenantQuota,
  updateTenantSettings,
  type ApiTenant,
  type BootstrapTenantInput,
  type TenantMember,
  type TenantQuotaInput,
} from '../../shared/api'
import { ErrorState, LoadingState } from '../components/States'
import { Kpi } from '../components/Kpi'
import { useApp } from '../components/Toast'

const DEFAULT_QUOTA: TenantQuotaInput = {
  maxConcurrentTasks: 100,
}

const EMPTY_TENANT: BootstrapTenantInput = {
  slug: '',
  name: '',
  adminName: '',
  adminPassword: '',
  quota: DEFAULT_QUOTA,
}

const quotaFromTenant = (tenant: ApiTenant): TenantQuotaInput => ({
  maxConcurrentTasks: tenant.quota.max_concurrent_tasks,
})

// Warden 不做商业化计量：身份与通道数量不设上限。唯一的租户级限制是
// 并发保护阈值——保护共享控制面不被失控执行洪峰淹没的安全阀。
function QuotaFields({
  value,
  onChange,
  disabled = false,
}: {
  value: TenantQuotaInput
  onChange: (value: TenantQuotaInput) => void
  disabled?: boolean
}) {
  return (
    <div className="form-grid">
      <label>
        <span>并发保护阈值（在途执行上限）</span>
        <input
          type="number"
          min={1}
          disabled={disabled}
          value={value.maxConcurrentTasks}
          onChange={(event) => onChange({ maxConcurrentTasks: Number(event.target.value) })}
        />
        <small className="quota-hint">
          安全阀，非商业配额：在途执行（含待审批/排队/执行中）达到该值时暂缓新执行。身份与执行通道数量不限。
        </small>
      </label>
    </div>
  )
}

export function TenantsPage() {
  const { notify } = useApp()
  const [tenants, setTenants] = useState<ApiTenant[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)
  const [createForm, setCreateForm] = useState<BootstrapTenantInput>(EMPTY_TENANT)
  const [selected, setSelected] = useState<ApiTenant | null>(null)
  const [quota, setQuota] = useState<TenantQuotaInput>(DEFAULT_QUOTA)
  const [emailDomains, setEmailDomains] = useState('')
  const [members, setMembers] = useState<TenantMember[]>([])
  const [memberForm, setMemberForm] = useState({ name: '', password: '', role: 'user' })
  const [busy, setBusy] = useState('')

  const load = (spinner = true) => {
    if (spinner) setLoading(true)
    setError('')
    listTenants()
      .then((items) => setTenants(items ?? []))
      .catch((e: ApiError) => setError(e.message || '加载租户失败'))
      .finally(() => setLoading(false))
  }
  useEffect(() => load(), [])

  const active = tenants.filter((tenant) => tenant.status === 'ACTIVE').length
  const connectorCount = useMemo(() => tenants.reduce((total, tenant) => total + tenant.usage.connectors, 0), [tenants])
  const principalCount = useMemo(() => tenants.reduce((total, tenant) => total + tenant.usage.principals, 0), [tenants])

  const openTenant = (slug: string) => {
    setBusy('detail')
    Promise.all([getTenant(slug), listTenantMembers(slug)])
      .then(([tenant, memberItems]) => {
        setSelected(tenant)
        setQuota(quotaFromTenant(tenant))
        setEmailDomains((((tenant.settings?.emailDomains as string[] | undefined) ?? [])).join(', '))
        setMembers(memberItems ?? [])
      })
      .catch((e: ApiError) => notify(e.message || '加载租户详情失败', 'error'))
      .finally(() => setBusy(''))
  }

  const addMember = (event: React.FormEvent) => {
    event.preventDefault()
    if (!selected) return
    setBusy('member')
    createTenantMember(selected.slug, {
      name: memberForm.name.trim(),
      password: memberForm.password,
      role: memberForm.role,
    })
      .then((res) => {
        notify(`成员已创建，登录名 ${res.login_name}`)
        setMemberForm({ name: '', password: '', role: 'user' })
        return openTenant(selected.slug)
      })
      .catch((e: ApiError) => notify(e.message || '创建成员失败', 'error'))
      .finally(() => setBusy(''))
  }

  const changeMemberRole = (member: TenantMember, role: string) => {
    if (!selected || role === member.role) return
    const label = member.display_name || member.name
    if (!window.confirm(`确认把「${label}」的角色改为「${role === 'tenant_admin' ? '租户管理员' : '项目成员'}」？\n\n该账号会话会立即失效，需重新登录。`)) return
    setBusy(member.id)
    updateAccountRole(member.id, role as 'user' | 'tenant_admin')
      .then(() => openTenant(selected.slug))
      .then(() => notify('成员角色已更新'))
      .catch((e: ApiError) => notify(e.message || '更新角色失败', 'error'))
      .finally(() => setBusy(''))
  }

  const create = (event: React.FormEvent) => {
    event.preventDefault()
    setBusy('create')
    bootstrapTenant({
      ...createForm,
      slug: createForm.slug.trim().toLowerCase(),
      name: createForm.name.trim(),
      adminName: createForm.adminName.trim(),
    })
      .then(({ tenant }) => {
        notify(`租户「${tenant.name}」已创建`)
        setCreating(false)
        setCreateForm(EMPTY_TENANT)
        load(false)
      })
      .catch((e: ApiError) => notify(e.message || '创建租户失败', 'error'))
      .finally(() => setBusy(''))
  }

  const saveQuota = () => {
    if (!selected) return
    setBusy('quota')
    updateTenantQuota(selected.slug, quota)
      .then(() => {
        notify('并发保护阈值已更新')
        return openTenant(selected.slug)
      })
      .then(() => load(false))
      .catch((e: ApiError) => notify(e.message || '更新并发保护阈值失败', 'error'))
      .finally(() => setBusy(''))
  }

  const saveDomains = () => {
    if (!selected) return
    setBusy('domains')
    const domains = emailDomains.split(/[,，\s]+/).map((d) => d.trim().toLowerCase()).filter(Boolean)
    updateTenantSettings(selected.slug, { emailDomains: domains })
      .then((res) => {
        setEmailDomains(res.emailDomains.join(', '))
        notify('SSO 邮箱域名已更新')
      })
      .catch((e: ApiError) => notify(e.message || '更新邮箱域名失败', 'error'))
      .finally(() => setBusy(''))
  }

  const disable = () => {
    if (!selected || !window.confirm(`确认停用租户「${selected.name}」？该租户的管理员和 Connector 将同时停用。`)) return
    setBusy('disable')
    disableTenant(selected.slug)
      .then(() => {
        notify('租户已停用')
        setSelected(null)
        load(false)
      })
      .catch((e: ApiError) => notify(e.message || '停用租户失败', 'error'))
      .finally(() => setBusy(''))
  }

  if (loading) return <LoadingState label="正在加载租户" />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  return (
    <div className="view-stack">
      <section className="kpi-grid">
        <Kpi icon={Building2} tone="blue" label="租户总数" value={tenants.length} detail={`${active} 个正在使用`} />
        <Kpi icon={ShieldCheck} tone="green" label="活跃租户" value={active} detail={`${tenants.length - active} 个已停用`} />
        <Kpi icon={Users} tone="amber" label="有效 Principal" value={principalCount} detail="所有租户合计" />
        <Kpi icon={Gauge} tone={connectorCount > 0 ? 'green' : 'amber'} label="有效 Connector" value={connectorCount} detail="未归档且未停用" />
      </section>

      <section className="panel">
        <div className="panel-header">
          <div>
            <h2>共享控制面租户</h2>
            <p>租户之间的身份、资源、策略、执行和审计相互隔离。</p>
          </div>
          <div className="header-actions">
            <button className="icon-button" aria-label="刷新租户" title="刷新" onClick={() => load()}>
              <RefreshCw size={16} />
            </button>
            <button className="primary-button compact" onClick={() => setCreating((value) => !value)}>
              <Plus size={16} />
              新建租户
            </button>
          </div>
        </div>
        {creating && (
          <form className="tenant-create-form" onSubmit={create}>
            <div className="form-grid tenant-identity-grid">
              <label>
                <span>租户名称</span>
                <input
                  required
                  maxLength={255}
                  value={createForm.name}
                  onChange={(event) => setCreateForm({ ...createForm, name: event.target.value })}
                  placeholder="例如 Acme Technology"
                />
              </label>
              <label>
                <span>租户标识</span>
                <input
                  required
                  maxLength={100}
                  pattern="[a-z0-9](?:[a-z0-9-]*[a-z0-9])?"
                  value={createForm.slug}
                  onChange={(event) => setCreateForm({ ...createForm, slug: event.target.value.toLowerCase() })}
                  placeholder="acme"
                />
              </label>
              <label>
                <span>首个管理员</span>
                <input
                  required
                  maxLength={255}
                  value={createForm.adminName}
                  onChange={(event) => setCreateForm({ ...createForm, adminName: event.target.value })}
                  placeholder="acme-admin"
                />
              </label>
              <label>
                <span>初始密码</span>
                <input
                  required
                  type="password"
                  minLength={8}
                  maxLength={72}
                  autoComplete="new-password"
                  placeholder="至少 8 位"
                  value={createForm.adminPassword}
                  onChange={(event) => setCreateForm({ ...createForm, adminPassword: event.target.value })}
                />
              </label>
            </div>
            <QuotaFields value={createForm.quota} onChange={(value) => setCreateForm({ ...createForm, quota: value })} />
            <div className="form-actions">
              <button type="button" className="quiet-button" onClick={() => setCreating(false)}>
                取消
              </button>
              <button className="primary-button" disabled={busy === 'create'}>
                {busy === 'create' ? <Loader2 size={16} className="spin" /> : <Plus size={16} />}创建租户
              </button>
            </div>
          </form>
        )}
        {tenants.length === 0 ? (
          <div className="empty-inline">尚未创建租户。</div>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>租户</th>
                  <th>状态</th>
                  <th>资源观测</th>
                  <th>在途执行</th>
                  <th>管理员</th>
                  <th>创建时间</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {tenants.map((tenant) => (
                  <tr key={tenant.id}>
                    <td>
                      <strong>{tenant.name}</strong>
                      <code className="cell-sub">{tenant.slug}</code>
                    </td>
                    <td>
                      <span className={`exec-led ${tenant.status === 'ACTIVE' ? 'ok' : 'deny'}`}>
                        <i aria-hidden="true" />
                        <span className={`badge ${tenant.status === 'ACTIVE' ? 'badge-success' : 'badge-neutral'}`}>
                          {tenant.status === 'ACTIVE' ? '使用中' : '已停用'}
                        </span>
                      </span>
                    </td>
                    <td>
                      <span className="font-mono text-xs">
                        {tenant.usage.principals} 身份 · {tenant.usage.connectors} 通道 · {tenant.usage.projects} 项目
                      </span>
                      <small className="cell-sub">数量不设上限，仅作运行观测</small>
                    </td>
                    <td>
                      <span className={`exec-led ${tenant.usage.in_flight_tasks >= tenant.quota.max_concurrent_tasks ? 'wait' : 'ok'}`}>
                        <i aria-hidden="true" />
                        <strong className="font-mono">{tenant.usage.in_flight_tasks}</strong>
                      </span>
                      <small className="cell-sub font-mono">并发保护 {tenant.quota.max_concurrent_tasks}</small>
                      <div
                        className={`usage-bar ${tenant.usage.in_flight_tasks / Math.max(1, tenant.quota.max_concurrent_tasks) > 0.8 ? 'warn' : ''}`}
                        aria-hidden="true"
                      >
                        <i style={{ width: `${Math.min(100, (tenant.usage.in_flight_tasks / Math.max(1, tenant.quota.max_concurrent_tasks)) * 100)}%` }} />
                      </div>
                    </td>
                    <td>{tenant.usage.administrators}</td>
                    <td>{new Date(tenant.created_at).toLocaleDateString('zh-CN')}</td>
                    <td>
                      <button className="quiet-button" disabled={busy === 'detail'} onClick={() => openTenant(tenant.slug)}>
                        管理
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {selected && (
        <div className="modal-layer" role="presentation">
          <button className="modal-scrim" aria-label="关闭租户详情" onClick={() => setSelected(null)} />
          <section className="tenant-detail-dialog" role="dialog" aria-modal="true" aria-labelledby="tenant-detail-title">
            <header className="tenant-detail-header">
              <div>
                <span className="eyebrow">
                  <Building2 size={15} />
                  租户边界
                </span>
                <h2 id="tenant-detail-title">{selected.name}</h2>
                <p>
                  <code>{selected.slug}</code> · 创建于 {new Date(selected.created_at).toLocaleString('zh-CN')}
                </p>
              </div>
              <button className="icon-button" aria-label="关闭" onClick={() => setSelected(null)}>
                <X size={19} />
              </button>
            </header>
            <div className="tenant-detail-body">
              <section>
                <div className="section-heading">
                  <div>
                    <h3>成员</h3>
                    <p>本租户的全部登录用户。租户管理员是子管理员，可管理本租户项目、资源、策略与 Agent；项目成员仅参与被分配的项目。</p>
                  </div>
                  <span className="project-user-result-count">{members.length} 人</span>
                </div>
                <div className="tenant-admin-list">
                  {members.map((member) => (
                    <div key={member.id}>
                      <span className={`status-dot ${member.status === 'ACTIVE' ? 'active' : ''}`} />
                      <div>
                        <strong>
                          {member.display_name || member.name}
                          {member.is_builtin_admin && <span className="account-self-tag">超级管理员</span>}
                        </strong>
                        <small>
                          {member.auth_source === 'oidc' ? 'SSO 登录' : `${member.name}@${selected.slug}`}
                          {member.memberships.length > 0
                            ? ` · ${member.memberships.map((mp) => mp.project_name).join('、')}`
                            : ' · 未分配项目'}
                        </small>
                      </div>
                      {member.is_builtin_admin ? (
                        <span className="badge badge-info">超管</span>
                      ) : (
                        <select
                          className="account-role-select"
                          value={member.role === 'tenant_admin' ? 'tenant_admin' : 'user'}
                          disabled={busy !== '' || selected.status !== 'ACTIVE'}
                          aria-label={`修改 ${member.display_name || member.name} 的角色`}
                          onChange={(event) => changeMemberRole(member, event.target.value)}
                        >
                          <option value="user">项目成员</option>
                          <option value="tenant_admin">租户管理员</option>
                        </select>
                      )}
                    </div>
                  ))}
                  {members.length === 0 && <div className="empty-inline">本租户暂无登录用户。</div>}
                </div>
                {selected.status === 'ACTIVE' && (
                  <form className="tenant-admin-form form-grid" onSubmit={addMember}>
                    <label>
                      <span>成员名称</span>
                      <input
                        required
                        maxLength={255}
                        value={memberForm.name}
                        onChange={(event) => setMemberForm({ ...memberForm, name: event.target.value })}
                        placeholder="例如 zhangsan"
                      />
                    </label>
                    <label>
                      <span>初始密码</span>
                      <input
                        required
                        type="password"
                        minLength={8}
                        maxLength={72}
                        autoComplete="new-password"
                        value={memberForm.password}
                        onChange={(event) => setMemberForm({ ...memberForm, password: event.target.value })}
                      />
                    </label>
                    <label>
                      <span>角色</span>
                      <select
                        className="account-role-select"
                        value={memberForm.role}
                        onChange={(event) => setMemberForm({ ...memberForm, role: event.target.value })}
                      >
                        <option value="user">项目成员</option>
                        <option value="tenant_admin">租户管理员</option>
                      </select>
                    </label>
                    <button className="secondary-button" disabled={busy === 'member'}>
                      <Plus size={15} />
                      添加成员
                    </button>
                  </form>
                )}
              </section>
              <section>
                <div className="section-heading">
                  <div>
                    <h3>并发保护</h3>
                    <p>保护共享控制面的安全阀，不是商业配额；身份与通道数量不设上限。</p>
                  </div>
                  <button
                    className="primary-button compact"
                    disabled={busy === 'quota' || selected.status !== 'ACTIVE'}
                    onClick={saveQuota}
                  >
                    {busy === 'quota' ? <Loader2 size={15} className="spin" /> : null}保存阈值
                  </button>
                </div>
                <QuotaFields value={quota} onChange={setQuota} disabled={selected.status !== 'ACTIVE'} />
              </section>
              <section>
                <div className="section-heading">
                  <div>
                    <h3>SSO 邮箱域名</h3>
                    <p>新 SSO 用户的 IdP 邮箱属于这些域名时，自动归入本租户；未命中则进默认租户。仅信任 IdP 签发的邮箱。</p>
                  </div>
                  <button
                    className="primary-button compact"
                    disabled={busy === 'domains' || selected.status !== 'ACTIVE'}
                    onClick={saveDomains}
                  >
                    {busy === 'domains' ? <Loader2 size={15} className="spin" /> : null}保存域名
                  </button>
                </div>
                <div className="form-grid">
                  <label>
                    <span>邮箱域名（逗号分隔）</span>
                    <input
                      type="text"
                      disabled={selected.status !== 'ACTIVE'}
                      value={emailDomains}
                      onChange={(event) => setEmailDomains(event.target.value)}
                      placeholder="acme.com, acme.cn"
                    />
                  </label>
                </div>
              </section>
              <section className="tenant-danger-zone">
                <div>
                  <h3>租户状态</h3>
                  <p>停用后，租户管理员不能登录，Connector 也不能继续执行任务。</p>
                </div>
                {selected.status === 'ACTIVE' ? (
                  <button className="danger-button" disabled={busy === 'disable' || selected.slug === 'platform'} onClick={disable}>
                    停用租户
                  </button>
                ) : (
                  <span className="badge badge-neutral">已停用</span>
                )}
              </section>
            </div>
          </section>
        </div>
      )}
    </div>
  )
}
