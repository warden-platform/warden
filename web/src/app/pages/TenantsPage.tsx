import { useEffect, useMemo, useState } from 'react'
import { Building2, Gauge, Loader2, Plus, RefreshCw, ShieldCheck, Users, X } from 'lucide-react'
import {
  ApiError, bootstrapTenant, createTenantAdmin, disableTenant, getTenant, listTenantAdmins, listTenants,
  updateTenantQuota, type ApiPrincipal, type ApiTenant, type BootstrapTenantInput, type TenantQuotaInput,
} from '../../shared/api'
import { ErrorState, LoadingState } from '../components/States'
import { Kpi } from '../components/Kpi'
import { useApp } from '../components/Toast'

const DEFAULT_QUOTA: TenantQuotaInput = {
  maxPrincipals: 1000,
  maxConnectors: 100,
  maxConcurrentTasks: 100,
  maxRequestsPerMinute: 600,
  auditRetentionDays: 365,
}

const EMPTY_TENANT: BootstrapTenantInput = {
  slug: '', name: '', adminName: '', adminPassword: '', quota: DEFAULT_QUOTA,
}

const quotaFromTenant = (tenant: ApiTenant): TenantQuotaInput => ({
  maxPrincipals: tenant.quota.max_principals,
  maxConnectors: tenant.quota.max_connectors,
  maxConcurrentTasks: tenant.quota.max_concurrent_tasks,
  maxRequestsPerMinute: tenant.quota.max_requests_per_minute,
  auditRetentionDays: tenant.quota.audit_retention_days,
})

function QuotaFields({ value, onChange, disabled = false }: { value: TenantQuotaInput; onChange: (value: TenantQuotaInput) => void; disabled?: boolean }) {
  const field = (key: keyof TenantQuotaInput, label: string) => <label><span>{label}</span><input type="number" min={1} disabled={disabled} value={value[key]} onChange={(event) => onChange({ ...value, [key]: Number(event.target.value) })} /></label>
  return <div className="form-grid tenant-quota-grid">
    {field('maxPrincipals', 'Principal 上限')}
    {field('maxConnectors', 'Connector 上限')}
    {field('maxConcurrentTasks', '并发任务上限')}
    {field('maxRequestsPerMinute', '每分钟请求上限')}
    {field('auditRetentionDays', '审计保留天数')}
  </div>
}

export function TenantsPage() {
  const { notify } = useApp()
  const [tenants, setTenants] = useState<ApiTenant[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)
  const [createForm, setCreateForm] = useState<BootstrapTenantInput>(EMPTY_TENANT)
  const [selected, setSelected] = useState<ApiTenant | null>(null)
  const [admins, setAdmins] = useState<ApiPrincipal[]>([])
  const [quota, setQuota] = useState<TenantQuotaInput>(DEFAULT_QUOTA)
  const [adminForm, setAdminForm] = useState({ name: '', password: '' })
  const [busy, setBusy] = useState('')

  const load = (spinner = true) => {
    if (spinner) setLoading(true)
    setError('')
    listTenants().then((items) => setTenants(items ?? [])).catch((e: ApiError) => setError(e.message || '加载租户失败')).finally(() => setLoading(false))
  }
  useEffect(() => load(), [])

  const active = tenants.filter((tenant) => tenant.status === 'ACTIVE').length
  const connectorCount = useMemo(() => tenants.reduce((total, tenant) => total + tenant.usage.connectors, 0), [tenants])
  const principalCount = useMemo(() => tenants.reduce((total, tenant) => total + tenant.usage.principals, 0), [tenants])

  const openTenant = (slug: string) => {
    setBusy('detail')
    Promise.all([getTenant(slug), listTenantAdmins(slug)])
      .then(([tenant, items]) => { setSelected(tenant); setQuota(quotaFromTenant(tenant)); setAdmins(items ?? []) })
      .catch((e: ApiError) => notify(e.message || '加载租户详情失败'))
      .finally(() => setBusy(''))
  }

  const create = (event: React.FormEvent) => {
    event.preventDefault()
    setBusy('create')
    bootstrapTenant({ ...createForm, slug: createForm.slug.trim().toLowerCase(), name: createForm.name.trim(), adminName: createForm.adminName.trim() })
      .then(({ tenant }) => { notify(`租户「${tenant.name}」已创建`); setCreating(false); setCreateForm(EMPTY_TENANT); load(false) })
      .catch((e: ApiError) => notify(e.message || '创建租户失败'))
      .finally(() => setBusy(''))
  }

  const saveQuota = () => {
    if (!selected) return
    setBusy('quota')
    updateTenantQuota(selected.slug, quota)
      .then(() => { notify('租户配额已更新'); return openTenant(selected.slug) })
      .then(() => load(false)).catch((e: ApiError) => notify(e.message || '更新配额失败')).finally(() => setBusy(''))
  }

  const addAdmin = (event: React.FormEvent) => {
    event.preventDefault()
    if (!selected) return
    setBusy('admin')
    createTenantAdmin(selected.slug, { name: adminForm.name.trim(), password: adminForm.password })
      .then(() => { notify('租户管理员已创建'); setAdminForm({ name: '', password: '' }); return openTenant(selected.slug) })
      .then(() => load(false)).catch((e: ApiError) => notify(e.message || '创建管理员失败')).finally(() => setBusy(''))
  }

  const disable = () => {
    if (!selected || !window.confirm(`确认停用租户「${selected.name}」？该租户的管理员和 Connector 将同时停用。`)) return
    setBusy('disable')
    disableTenant(selected.slug).then(() => { notify('租户已停用'); setSelected(null); load(false) })
      .catch((e: ApiError) => notify(e.message || '停用租户失败')).finally(() => setBusy(''))
  }

  if (loading) return <LoadingState label="正在加载租户" />
  if (error) return <ErrorState message={error} onRetry={() => load()} />

  return <div className="view-stack">
    <section className="kpi-grid">
      <Kpi icon={Building2} tone="blue" label="租户总数" value={tenants.length} detail={`${active} 个正在使用`} />
      <Kpi icon={ShieldCheck} tone="green" label="活跃租户" value={active} detail={`${tenants.length - active} 个已停用`} />
      <Kpi icon={Users} tone="amber" label="有效 Principal" value={principalCount} detail="所有租户合计" />
      <Kpi icon={Gauge} tone="red" label="有效 Connector" value={connectorCount} detail="未归档且未停用" />
    </section>

    <section className="panel">
      <div className="panel-header"><div><h2>共享控制面租户</h2><p>租户之间的身份、资源、策略、执行和审计相互隔离。</p></div><div className="header-actions"><button className="icon-button" aria-label="刷新租户" title="刷新" onClick={() => load()}><RefreshCw size={16} /></button><button className="primary-button compact" onClick={() => setCreating((value) => !value)}><Plus size={16} />新建租户</button></div></div>
      {creating && <form className="tenant-create-form" onSubmit={create}>
        <div className="form-grid tenant-identity-grid">
          <label><span>租户名称</span><input required maxLength={255} value={createForm.name} onChange={(event) => setCreateForm({ ...createForm, name: event.target.value })} placeholder="例如 Acme Technology" /></label>
          <label><span>租户标识</span><input required maxLength={100} pattern="[a-z0-9](?:[a-z0-9-]*[a-z0-9])?" value={createForm.slug} onChange={(event) => setCreateForm({ ...createForm, slug: event.target.value.toLowerCase() })} placeholder="acme" /></label>
          <label><span>首个管理员</span><input required maxLength={255} value={createForm.adminName} onChange={(event) => setCreateForm({ ...createForm, adminName: event.target.value })} placeholder="acme-admin" /></label>
          <label><span>初始密码</span><input required type="password" minLength={8} maxLength={72} autoComplete="new-password" value={createForm.adminPassword} onChange={(event) => setCreateForm({ ...createForm, adminPassword: event.target.value })} /></label>
        </div>
        <QuotaFields value={createForm.quota} onChange={(value) => setCreateForm({ ...createForm, quota: value })} />
        <div className="form-actions"><button type="button" className="quiet-button" onClick={() => setCreating(false)}>取消</button><button className="primary-button" disabled={busy === 'create'}>{busy === 'create' ? <Loader2 size={16} className="spin" /> : <Plus size={16} />}创建租户</button></div>
      </form>}
      {tenants.length === 0 ? <div className="empty-inline">尚未创建租户。</div> : <div className="table-wrap"><table><thead><tr><th>租户</th><th>状态</th><th>使用量</th><th>并发任务</th><th>管理员</th><th>创建时间</th><th>操作</th></tr></thead><tbody>{tenants.map((tenant) => <tr key={tenant.id}>
        <td><strong>{tenant.name}</strong><code className="cell-sub">{tenant.slug}</code></td>
        <td><span className={`badge ${tenant.status === 'ACTIVE' ? 'badge-success' : 'badge-neutral'}`}>{tenant.status === 'ACTIVE' ? '使用中' : '已停用'}</span></td>
        <td><span>{tenant.usage.principals} / {tenant.quota.max_principals} Principal</span><small className="cell-sub">{tenant.usage.connectors} / {tenant.quota.max_connectors} Connector · {tenant.usage.projects} 项目</small></td>
        <td><strong>{tenant.usage.in_flight_tasks}</strong><small className="cell-sub">上限 {tenant.quota.max_concurrent_tasks}</small></td>
        <td>{tenant.usage.administrators}</td><td>{new Date(tenant.created_at).toLocaleDateString('zh-CN')}</td>
        <td><button className="quiet-button" disabled={busy === 'detail'} onClick={() => openTenant(tenant.slug)}>管理</button></td>
      </tr>)}</tbody></table></div>}
    </section>

    {selected && <div className="modal-layer" role="presentation"><button className="modal-scrim" aria-label="关闭租户详情" onClick={() => setSelected(null)} /><section className="tenant-detail-dialog" role="dialog" aria-modal="true" aria-labelledby="tenant-detail-title">
      <header className="tenant-detail-header"><div><span className="eyebrow"><Building2 size={15} />租户边界</span><h2 id="tenant-detail-title">{selected.name}</h2><p><code>{selected.slug}</code> · 创建于 {new Date(selected.created_at).toLocaleString('zh-CN')}</p></div><button className="icon-button" aria-label="关闭" onClick={() => setSelected(null)}><X size={19} /></button></header>
      <div className="tenant-detail-body">
        <section><div className="section-heading"><div><h3>管理员</h3><p>可管理当前租户的项目、资源、策略和 Agent 身份。</p></div></div>
          <div className="tenant-admin-list">{admins.map((admin) => <div key={admin.id}><span className={`status-dot ${admin.status === 'ACTIVE' ? 'active' : ''}`} /><div><strong>{admin.name}</strong><small>{admin.last_login_at ? `最近登录 ${new Date(admin.last_login_at).toLocaleString('zh-CN')}` : '尚未登录'}</small></div><span className={`badge ${admin.status === 'ACTIVE' ? 'badge-success' : 'badge-neutral'}`}>{admin.status === 'ACTIVE' ? '有效' : '停用'}</span></div>)}</div>
          {selected.status === 'ACTIVE' && <form className="tenant-admin-form form-grid" onSubmit={addAdmin}><label><span>管理员名称</span><input required maxLength={255} value={adminForm.name} onChange={(event) => setAdminForm({ ...adminForm, name: event.target.value })} /></label><label><span>初始密码</span><input required type="password" minLength={8} maxLength={72} autoComplete="new-password" value={adminForm.password} onChange={(event) => setAdminForm({ ...adminForm, password: event.target.value })} /></label><button className="secondary-button" disabled={busy === 'admin'}><Plus size={15} />添加管理员</button></form>}
        </section>
        <section><div className="section-heading"><div><h3>资源配额</h3><p>配额在租户边界内限制主体、执行通道和运行负载。</p></div><button className="primary-button compact" disabled={busy === 'quota' || selected.status !== 'ACTIVE'} onClick={saveQuota}>{busy === 'quota' ? <Loader2 size={15} className="spin" /> : null}保存配额</button></div><QuotaFields value={quota} onChange={setQuota} disabled={selected.status !== 'ACTIVE'} /></section>
        <section className="tenant-danger-zone"><div><h3>租户状态</h3><p>停用后，租户管理员不能登录，Connector 也不能继续执行任务。</p></div>{selected.status === 'ACTIVE' ? <button className="danger-button" disabled={busy === 'disable' || selected.slug === 'platform'} onClick={disable}>停用租户</button> : <span className="badge badge-neutral">已停用</span>}</section>
      </div>
    </section></div>}
  </div>
}
