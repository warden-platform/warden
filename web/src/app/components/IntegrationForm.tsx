import { useEffect, useState, type ReactNode } from 'react'
import { AlertTriangle, Check, Copy, ExternalLink, KeyRound, Loader2 } from 'lucide-react'
import type { ApiConnector, ApiError, ApiProject, Integration } from '../../shared/api'
import { createIntegration, listProjects, updateIntegration } from '../../shared/api'

// Reusable connection-info form for protocol integrations.
// Used both by the create page (which injects a connector selector via
// `extraContent`) and the integration detail settings tab (edit mode via
// `existing`). The `protocol` prop drives field visibility and copy.
export function IntegrationForm({
  connector,
  existing,
  protocol = 'jenkins',
  onCancel,
  onSaved,
  extraContent,
}: {
  connector: ApiConnector
  existing?: Integration | null
  protocol?: string
  onCancel: () => void
  onSaved: (message: string) => void
  extraContent?: ReactNode
}) {
  const isGrafana = protocol === 'grafana'
  const isDatabase = protocol === 'postgresql' || protocol === 'mysql'
  const isHuaweiCloud = protocol === 'huaweicloud'
  const isAliyunSLS = protocol === 'aliyun_sls'
  const isH3C = protocol === 'h3c_comware_ssh'
  const isHuawei = isHuaweiCloud || isAliyunSLS
  const tokenLabel = isGrafana
    ? 'Grafana Service Account Token'
    : isDatabase
      ? '数据库密码'
      : isHuaweiCloud
        ? '华为云 AK/SK'
        : isAliyunSLS
          ? '阿里云 AccessKey/Secret'
          : isH3C
            ? 'H3C SSH 私钥'
            : 'Jenkins API Token'
  const scopeHint = isGrafana
    ? 'Dashboard'
    : isDatabase
      ? 'Schema'
      : isHuaweiCloud
        ? '云服务'
        : isAliyunSLS
          ? '可访问 Project'
          : isH3C
            ? '网络设备'
            : 'Job'
  const linux = connector.connector_type === 'linux'
  const network = connector.connector_type === 'network'
  const connectorNamespace = (connector.config.connectorNamespace as string | undefined) ?? 'warden-system'
  const [name, setName] = useState(existing?.name ?? '')
  const [nameTouched, setNameTouched] = useState(!!existing?.name)
  const [endpoint, setEndpoint] = useState(existing?.endpoint ?? '')
  const [username, setUsername] = useState(existing?.username ?? '')
  const [credentialRef, setCredentialRef] = useState(existing?.credential_ref ?? '')
  const [saving, setSaving] = useState(false)
  const [allowAll, setAllowAll] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [projects, setProjects] = useState<ApiProject[]>([])
  const [projectId, setProjectId] = useState(existing?.project_id ?? '')
  const [environment, setEnvironment] = useState(existing?.environment ?? '')
  const [deviceRole, setDeviceRole] = useState((existing?.scope?.deviceRole as string | undefined) ?? '')
  const [deviceName, setDeviceName] = useState((existing?.scope?.deviceName as string | undefined) ?? '')
  const [hostKeySHA256, setHostKeySHA256] = useState((existing?.scope?.hostKeySHA256 as string | undefined) ?? '')
  const [legacyRsa, setLegacyRsa] = useState(Boolean(existing?.scope?.legacyRsa))
  const [allowInterfaceChanges, setAllowInterfaceChanges] = useState(Boolean(existing?.scope?.allowInterfaceChanges))
  const [protectedInterfaces, setProtectedInterfaces] = useState(
    Array.isArray(existing?.scope?.protectedInterfaces) ? existing.scope.protectedInterfaces.map(String).join('\n') : '',
  )
  useEffect(() => {
    listProjects()
      .then((items) => {
        const next = items ?? []
        setProjects(next)
        setProjectId((current) => current || next.find((project) => project.is_default)?.id || '')
      })
      .catch(() => setProjects([]))
  }, [])
  const ref = credentialRef.trim() || '<credential-ref>'
  const credentialCommand = isHuawei
    ? linux
      ? `printf '%s' '<AccessKey>:<SecretKey>' | sudo install -o root -g warden-linux -m 0640 \\\n  /dev/stdin /etc/warden-linux/credentials/${ref}`
      : network
        ? `printf '%s' '<AccessKey>:<SecretKey>' | sudo install -o root -g warden-network -m 0640 \\\n  /dev/stdin /var/lib/warden-network/credentials/${ref}`
        : `kubectl create secret generic ${ref} -n ${connectorNamespace} \\\n  --from-literal=token='<AccessKey>:<SecretKey>'`
    : linux
      ? `printf '%s' '<你的 ${tokenLabel}>' | sudo install -o root -g warden-linux -m 0640 \\\n  /dev/stdin /etc/warden-linux/credentials/${ref}`
      : network
        ? `printf '%s' '<你的 ${tokenLabel}>' | sudo install -o root -g warden-network -m 0640 \\\n  /dev/stdin /var/lib/warden-network/credentials/${ref}`
        : `kubectl create secret generic ${ref} -n ${connectorNamespace} \\\n  --from-literal=token='<你的 ${tokenLabel}>'`
  const credentialLocation = linux
    ? `/etc/warden-linux/credentials/${ref}`
    : network
      ? `/var/lib/warden-network/credentials/${ref}`
      : `Secret ${ref} / key: token (Namespace: ${connectorNamespace})`
  const interfaceChangesSupported = ['switch', 'core', 'aggregation', 'poe'].includes(deviceRole)
  const protectedInterfaceList = [
    ...new Set(
      protectedInterfaces
        .split(/[\n,]/)
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  ]
  const missingFields = [
    !name.trim() && '名称',
    !endpoint.trim() && (isHuaweiCloud || isAliyunSLS ? '种子区域' : `${protocolNameFor(protocol)} Endpoint`),
    !isGrafana && !isHuawei && !username.trim() && (isDatabase ? '数据库用户名' : '服务账号'),
    !credentialRef.trim() && 'Credential Reference',
    isH3C && !deviceRole && '设备角色',
    isH3C && !hostKeySHA256.trim() && 'Host Key SHA256 指纹',
    !projectId && '默认项目',
    !existing && !allowAll && `确认创建阶段允许全部 ${scopeHint}`,
  ].filter(Boolean) as string[]

  const submit = () => {
    if (missingFields.length > 0) {
      setError(`请先完成：${missingFields.join('、')}`)
      return
    }
    setSaving(true)
    setError('')
    const payload = {
      protocol,
      name: name.trim(),
      endpoint: endpoint.trim(),
      username: username.trim(),
      credentialRef: credentialRef.trim(),
      projectId,
      environment,
      scope: isH3C
        ? {
            ...(existing?.scope ?? {}),
            ...(!existing ? { allowAll } : {}),
            deviceRole,
            deviceName: deviceName.trim(),
            hostKeySHA256: hostKeySHA256.trim(),
            legacyRsa,
            allowInterfaceChanges: interfaceChangesSupported && allowInterfaceChanges,
            protectedInterfaces: interfaceChangesSupported && allowInterfaceChanges ? protectedInterfaceList : [],
          }
        : (existing?.scope ??
          (isHuaweiCloud || isAliyunSLS ? { allowAll, regionPolicy: { mode: 'all', regions: [], deniedRegions: [] } } : { allowAll })),
    }
    const call = existing ? updateIntegration(connector.id, existing.id, payload) : createIntegration(connector.id, payload)
    call
      .then(() => onSaved(existing ? 'Integration 已更新，Connector 正在同步' : 'Integration 已创建，Connector 正在调谐并同步资源'))
      .catch((e: ApiError) => setError(e.message))
      .finally(() => setSaving(false))
  }
  const onEndpointChange = (value: string) => {
    setEndpoint(value)
    if (!nameTouched) {
      if (isHuaweiCloud || isAliyunSLS) {
        setName(`${isHuaweiCloud ? 'huaweicloud' : 'aliyun-sls'}-${value}`)
        return
      }
      try {
        setName(new URL(value).hostname)
      } catch {
        /* incomplete URL, keep previous */
      }
    }
  }
  const copyCommand = () => {
    navigator.clipboard.writeText(credentialCommand).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }
  const protocolName = protocolNameFor(protocol)
  const endpointPlaceholder =
    protocol === 'postgresql'
      ? 'postgresql://db.example.com:5432/app?sslmode=require'
      : protocol === 'mysql'
        ? 'mysql://db.example.com:3306/app?tls=true'
        : isGrafana
          ? 'https://grafana.example.com'
          : isHuaweiCloud
            ? 'cn-north-4'
            : isAliyunSLS
              ? 'cn-hangzhou'
              : isH3C
                ? 'ssh://192.0.2.10:22'
                : 'https://jenkins.example.com'
  const namePlaceholder =
    protocol === 'postgresql'
      ? 'prod-postgresql'
      : protocol === 'mysql'
        ? 'prod-mysql'
        : isGrafana
          ? 'prod-grafana'
          : isHuaweiCloud
            ? 'prod-huaweicloud'
            : isAliyunSLS
              ? 'prod-aliyun-sls'
              : isH3C
                ? 'core-01'
                : 'prod-jenkins'
  const refPlaceholder = isDatabase
    ? 'database-password'
    : isGrafana
      ? 'grafana-sa-token'
      : isHuaweiCloud
        ? 'huawei-ak-sk'
        : isAliyunSLS
          ? 'aliyun-sls-ak-sk'
          : isH3C
            ? 'h3c-core-01-key'
            : 'jenkins-api-token'
  return (
    <div className="form-grid compact-grid integration-form">
      <label>
        <span>名称</span>
        <input
          value={name}
          onChange={(e) => {
            setNameTouched(true)
            setName(e.target.value)
          }}
          placeholder={namePlaceholder}
        />
      </label>
      <label>
        <span>{isHuaweiCloud ? '种子 Region' : isAliyunSLS ? '种子 Region' : `${protocolName} Endpoint`}</span>
        <input value={endpoint} onChange={(e) => onEndpointChange(e.target.value)} placeholder={endpointPlaceholder} />
        {isAliyunSLS && (
          <small>
            用于首次探测；选择“全部可访问 Region”后，Warden 会并发发现该 RAM 账号在其他区域有权访问的 Project。公网 Endpoint 将自动生成。
          </small>
        )}
      </label>
      {!isGrafana && (
        <label>
          <span>
            {isDatabase
              ? '数据库用户名'
              : isHuaweiCloud
                ? 'IAM 用户标识（建议填写）'
                : isAliyunSLS
                  ? 'RAM 用户标识（建议填写）'
                  : '服务账号'}
          </span>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder={isDatabase ? 'warden_reader' : isHuawei || isAliyunSLS ? 'warden-readonly' : 'warden-connector'}
          />
          {(isHuaweiCloud || isAliyunSLS) && <small>仅用于标识这对 AK/SK 属于哪个云账号用户，不参与认证，也不会展示 AK/SK。</small>}
        </label>
      )}
      {isH3C && (
        <>
          <label>
            <span>设备角色</span>
            <select value={deviceRole} onChange={(e) => setDeviceRole(e.target.value)}>
              <option value="">选择角色</option>
              <option value="switch">交换机</option>
              <option value="core">核心交换机</option>
              <option value="aggregation">汇聚交换机</option>
              <option value="poe">PoE 交换机</option>
              <option value="router">路由器</option>
              <option value="ac">无线 AC</option>
            </select>
          </label>
          <label>
            <span>设备名称（可选）</span>
            <input value={deviceName} onChange={(e) => setDeviceName(e.target.value)} placeholder="留空使用 Integration 名称" />
          </label>
          <label>
            <span>SSH Host Key SHA256</span>
            <input value={hostKeySHA256} onChange={(e) => setHostKeySHA256(e.target.value)} placeholder="SHA256:..." />
            <small>必须预先核对设备指纹；Connector 会拒绝指纹不匹配的连接。</small>
          </label>
          <label>
            <span>
              <input type="checkbox" checked={legacyRsa} onChange={(e) => setLegacyRsa(e.target.checked)} /> 允许旧式 RSA Host
              Key（仅兼容老设备）
            </span>
          </label>
          {interfaceChangesSupported && (
            <>
              <label className="check-field">
                <input type="checkbox" checked={allowInterfaceChanges} onChange={(e) => setAllowInterfaceChanges(e.target.checked)} />
                <span>允许受控接口变更</span>
                <small>默认关闭。开启后仅开放固定的描述、Access VLAN 和 Trunk VLAN 动作；所有写操作仍需预检、审批和写后复核。</small>
              </label>
              {allowInterfaceChanges && (
                <label>
                  <span>保护接口</span>
                  <textarea
                    rows={4}
                    value={protectedInterfaces}
                    onChange={(e) => setProtectedInterfaces(e.target.value)}
                    placeholder={'Bridge-Aggregation1\nTen-GigabitEthernet1/0/49'}
                  />
                  <small>每行一个接口。聚合口及描述含 uplink、upstream、core 的接口也会自动按保护接口处理。</small>
                </label>
              )}
            </>
          )}
        </>
      )}
      <label>
        <span>Credential Reference</span>
        <input
          value={credentialRef}
          onChange={(e) => setCredentialRef(e.target.value.toLowerCase())}
          placeholder={refPlaceholder}
          pattern="[a-z0-9](?:(?:[a-z0-9.]|-)*[a-z0-9])?"
        />
      </label>
      <label>
        <span>默认项目</span>
        <select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
          <option value="" disabled>
            选择中间件默认项目
          </option>
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
              {p.is_default ? '（系统默认）' : ''}
            </option>
          ))}
        </select>
        <small>中间件项目独立于执行通道；执行通道只决定任务在哪里执行。单资源仍可覆盖。</small>
      </label>
      <label>
        <span>默认环境</span>
        <select value={environment} onChange={(e) => setEnvironment(e.target.value as typeof environment)}>
          <option value="">继承执行通道</option>
          <option value="development">开发环境</option>
          <option value="testing">测试环境</option>
          <option value="production">生产环境</option>
        </select>
        <small>该 Integration 发现的服务默认使用此环境，单资源仍可覆盖。</small>
      </label>
      <div className="credential-guide">
        <div className="credential-guide-head">
          <KeyRound size={16} />
          <span>{tokenLabel} 不经过 Warden。请先在目标侧放置凭据，再保存 Integration：</span>
        </div>
        {isHuaweiCloud && (
          <div className="credential-guide-deeplink">
            <ol className="credential-guide-steps">
              <li>登录华为云控制台，创建（或选用）一个只读 IAM 用户（建议授予 ReadOnlyAccess 策略）</li>
              <li>在「我的凭证 → 访问密钥」中新增访问密钥，得到 AccessKey / SecretKey</li>
              <li>
                回到本页，用下方命令把 <code>AK:SK</code> 放置到 Connector 所在目标侧
              </li>
            </ol>
            <div className="credential-guide-links">
              <button
                type="button"
                className="secondary-button"
                onClick={() => window.open('https://console.huaweicloud.com/iam/?locale=zh-cn#/mine/accessKey', '_blank', 'noopener')}
              >
                <ExternalLink size={14} />
                前往华为云创建 AK/SK
              </button>
              <button
                type="button"
                className="quiet-button"
                onClick={() => window.open('https://console.huaweicloud.com/iam/?locale=zh-cn#/iam/users', '_blank', 'noopener')}
              >
                <ExternalLink size={14} />
                管理 IAM 用户
              </button>
            </div>
          </div>
        )}
        {isAliyunSLS && (
          <small className="credential-guide-hint">
            建议使用只读 RAM 用户；Warden 只展示该身份通过 ListProject/ListLogStores 实际有权访问的 Project 和 Logstore。
          </small>
        )}
        {isH3C && (
          <small className="credential-guide-hint">
            请放置仅用于该设备的 SSH 私钥（PEM）。Network Connector
            在内网主动连接设备；发现与只读审计默认开放，接口变更需显式开启，并经过预检、审批和写后复核。
          </small>
        )}
        <div className="credential-guide-location">
          目标侧凭据位置：<code>{credentialLocation}</code>
        </div>
        <div className="credential-guide-command">
          <pre>
            <code>{credentialCommand}</code>
          </pre>
          <button type="button" className="quiet-button" onClick={copyCommand}>
            {copied ? <Check size={14} /> : <Copy size={14} />}
            {copied ? '已复制' : '复制'}
          </button>
        </div>
        {!existing && (
          <small className="credential-guide-hint">
            先放置凭据并确认下面的临时全量范围。Connector 发现资源后，可在中间件详情页缩小范围。
          </small>
        )}
      </div>
      {!existing && (
        <label>
          <span>初始资源范围</span>
          <span>
            <input type="checkbox" checked={allowAll} onChange={(e) => setAllowAll(e.target.checked)} /> 创建阶段允许全部 {scopeHint}
          </span>
          <small>
            {isAliyunSLS
              ? '首次同步会自动发现 RAM 有权访问的 Region、Project 和 Logstore；随后可按 Region 或 Project 缩小范围。'
              : '首次连接前尚无资源可选；创建并同步后可改为精确范围。'}
          </small>
        </label>
      )}
      {extraContent}
      {missingFields.length > 0 && !error && (
        <div className="credential-note">
          <AlertTriangle size={16} />
          <span>还需完成：{missingFields.join('、')}</span>
        </div>
      )}
      {error && (
        <div className="credential-note error-note">
          <AlertTriangle size={16} />
          <span>{error}</span>
        </div>
      )}
      <div className="form-actions">
        <button type="button" className="secondary-button" onClick={onCancel}>
          取消
        </button>
        <button type="button" className="primary-button" disabled={saving} onClick={submit}>
          {saving ? <Loader2 size={16} className="spin" /> : <Check size={16} />}
          {existing ? '保存修改' : '创建 Integration'}
        </button>
      </div>
    </div>
  )
}

function protocolNameFor(protocol: string) {
  if (protocol === 'postgresql') return 'PostgreSQL'
  if (protocol === 'mysql') return 'MySQL'
  if (protocol === 'grafana') return 'Grafana'
  if (protocol === 'huaweicloud') return '华为云'
  if (protocol === 'aliyun_sls') return '阿里云 SLS'
  if (protocol === 'h3c_comware_ssh') return 'H3C 网络设备'
  return 'Jenkins'
}
