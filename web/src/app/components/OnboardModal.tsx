import { useEffect, useState } from 'react'
import {
  AlertTriangle,
  ArrowRight,
  Boxes,
  Check,
  CheckCircle2,
  ChevronDown,
  Copy,
  Loader2,
  Network,
  ServerCog,
  ShieldCheck,
  TerminalSquare,
  X,
} from 'lucide-react'
import type { ApiConnector, ApiError, ApiProject } from '../../shared/api'
import {
  confirmClusterPairing,
  confirmHostPairing,
  confirmLinuxPairing,
  getClusterPairingOptions,
  getHostPairingOptions,
  getInstallConfig,
  getLinuxPairingOptions,
  listConnectors,
  listProjects,
} from '../../shared/api'
import { useApp } from './Toast'
import { Badge } from './Badge'

// Target onboarding wizard. The Connector is created as the target-side
// execution agent; users start from the host/cluster they want to manage.
export function OnboardModal({
  onClose,
  onComplete,
  initialTargetType = 'kubernetes',
}: {
  onClose: () => void
  onComplete: (connectorId: string) => void
  initialTargetType?: 'kubernetes' | 'linux' | 'windows' | 'macos'
}) {
  const { notify } = useApp()
  const [targetType, setTargetType] = useState<'kubernetes' | 'linux' | 'windows' | 'macos'>(initialTargetType)
  const [step, setStep] = useState<1 | 2 | 3>(1)
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [environment, setEnvironment] = useState<'development' | 'testing' | 'production'>('testing')
  const [projects, setProjects] = useState<ApiProject[]>([])
  const [projectId, setProjectId] = useState('')
  const [namespaceOptions, setNamespaceOptions] = useState<string[]>([])
  const [selectedNamespaces, setSelectedNamespaces] = useState<string[]>([])
  const [containerOptions, setContainerOptions] = useState<string[]>([])
  const [selectedContainers, setSelectedContainers] = useState<string[]>([])
  const [commandOptions, setCommandOptions] = useState<string[]>([])
  const [selectedCommands, setSelectedCommands] = useState<string[]>([])
  const [machine, setMachine] = useState<Record<string, unknown>>({})
  const [optionsLoading, setOptionsLoading] = useState(false)
  const [imagePullSecret, setImagePullSecret] = useState('')
  const [connectorId, setConnectorId] = useState('')
  const [status, setStatus] = useState<ApiConnector['status'] | 'PAIRING'>('PAIRING')
  const [ready, setReady] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [installConfig, setInstallConfig] = useState<Awaited<ReturnType<typeof getInstallConfig>> | null>(null)
  const [installConfigError, setInstallConfigError] = useState('')
  const fallbackControlURL = `${window.location.protocol}//${window.location.hostname}:8080`
  const controlURL = installConfig?.publicControlUrl || fallbackControlURL
  const shellQuote = (value: string) => `'${value.replaceAll("'", `'"'"'`)}'`
  const chartReference = installConfig?.operatorChartReference.startsWith('/')
    ? `${controlURL}${installConfig.operatorChartReference}`
    : installConfig?.operatorChartReference
  const imagePullSecretArg = imagePullSecret.trim() ? ` --set imagePullSecret=${shellQuote(imagePullSecret.trim())}` : ''
  const installCommand =
    installConfig && chartReference
      ? `helm upgrade --install warden-operator ${shellQuote(chartReference)} --namespace warden-system --create-namespace --set controlPlaneURL=${shellQuote(controlURL)} --set image.repository=${shellQuote(installConfig.operatorImageRepository)} --set image.tag=${shellQuote(installConfig.operatorImageTag)} --set connectorImage=${shellQuote(installConfig.connectorImage)}${imagePullSecretArg}`
      : ''
  const linuxInstallCommand = installConfig
    ? `curl -fsSL ${shellQuote(`${controlURL}/downloads/install-linux.sh`)} | sudo bash -s -- --control-url ${shellQuote(controlURL)}`
    : ''
  const hostPlatform = targetType === 'windows' || targetType === 'macos' ? targetType : undefined
  const hostInstallCommand = hostPlatform
    ? `curl -fsSL ${shellQuote(`${controlURL}/downloads/install-host.sh`)} | ${hostPlatform === 'windows' ? 'bash' : 'sudo bash'}`
    : ''
  const activeInstallCommand = targetType === 'linux' ? linuxInstallCommand : hostPlatform ? hostInstallCommand : installCommand

  useEffect(() => {
    getInstallConfig()
      .then(setInstallConfig)
      .catch((e: ApiError) => setInstallConfigError(e.message || '读取当前发布配置失败'))
    listProjects()
      .then((items) => setProjects(items ?? []))
      .catch(() => setProjects([]))
  }, [])

  useEffect(() => {
    if (step !== 3 || !connectorId || ready) return
    const timer = window.setInterval(() => {
      listConnectors()
        .then((items) => {
          const connector = items.find((item) => item.id === connectorId)
          if (connector) {
            setStatus(connector.status)
            const heartbeatAt = connector.last_heartbeat ? new Date(connector.last_heartbeat).getTime() : 0
            setReady(connector.status === 'ACTIVE' && heartbeatAt > 0 && Date.now() - heartbeatAt < 60_000)
          }
        })
        .catch(() => undefined)
    }, 3000)
    return () => window.clearInterval(timer)
  }, [step, connectorId, ready])

  const copyInstall = () => {
    if (!activeInstallCommand) return
    navigator.clipboard?.writeText(activeInstallCommand).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1800)
    })
  }

  const loadOptions = () => {
    setError('')
    setOptionsLoading(true)
    const optionsRequest = hostPlatform
      ? getHostPairingOptions(code.trim().toUpperCase())
      : targetType === 'linux'
        ? getLinuxPairingOptions(code.trim().toUpperCase())
        : getClusterPairingOptions(code.trim().toUpperCase())
    optionsRequest
      .then((result) => {
        const options =
          'availableCommands' in result
            ? result.availableCommands
            : 'availableServices' in result
              ? result.availableServices
              : result.availableNamespaces
        setNamespaceOptions(options ?? [])
        setSelectedNamespaces(targetType === 'linux' ? [] : (options ?? []).includes('default') ? ['default'] : [])
        if ('machine' in result) {
          setContainerOptions('availableContainers' in result ? (result.availableContainers ?? []) : [])
          setSelectedContainers([])
          setMachine(result.machine)
          setName(String(result.machine.hostname ?? ''))
        }
        if ('availableCommands' in result) {
          setCommandOptions(result.availableCommands ?? [])
          setSelectedCommands([])
        }
      })
      .catch((e: ApiError) => {
        setNamespaceOptions([])
        setSelectedNamespaces([])
        setError(e.message || `读取${targetType === 'linux' ? '主机' : '集群'}信息失败`)
      })
      .finally(() => setOptionsLoading(false))
  }

  const toggleNamespace = (namespace: string) => {
    setSelectedNamespaces((current) =>
      current.includes(namespace) ? current.filter((item) => item !== namespace) : [...current, namespace],
    )
  }

  const confirm = (event: React.FormEvent) => {
    event.preventDefault()
    setError('')
    setSubmitting(true)
    const confirmation = hostPlatform
      ? confirmHostPairing({
          code: code.trim().toUpperCase(),
          platform: hostPlatform,
          name,
          environment,
          projectId,
          allowedCommands: selectedCommands,
        })
      : targetType === 'linux'
        ? confirmLinuxPairing({
            code: code.trim().toUpperCase(),
            name,
            environment,
            projectId,
            allowedServices: selectedNamespaces,
            allowedContainers: selectedContainers,
          })
        : confirmClusterPairing({
            code: code.trim().toUpperCase(),
            name,
            environment,
            projectId,
            allowedNamespaces: selectedNamespaces,
            allowedImagePrefixes: [],
          })
    confirmation
      .then((result) => {
        setConnectorId(result.connectorId)
        setStatus('PAIRING')
        setReady(false)
        setStep(3)
        notify(targetType === 'linux' ? '配对已确认，Linux Connector 正在启动' : '配对已确认，Operator 正在安装 Connector')
      })
      .catch((e: ApiError) => setError(e.message || '配对失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <>
      <button className="drawer-scrim" aria-label="关闭接入目标" onClick={onClose} />
      <aside className="drawer onboarding-drawer" aria-label="接入主机或集群">
        <div className="drawer-header">
          <div>
            <span className="eyebrow">
              <Network size={15} />
              Target Onboarding
            </span>
            <h2>
              接入
              {targetType === 'linux'
                ? ' Linux 主机'
                : hostPlatform
                  ? ` ${hostPlatform === 'windows' ? 'Windows' : 'macOS'} 主机`
                  : ' Kubernetes 集群'}
            </h2>
            <p>在目标侧安装执行代理，并通过短期配对码建立受控连接。</p>
          </div>
          <button className="icon-button" onClick={onClose} aria-label="关闭">
            <X size={20} />
          </button>
        </div>
        {step === 1 && (
          <div className="onboarding-target">
            <div className="segmented">
              <button className={targetType === 'kubernetes' ? 'selected' : ''} onClick={() => setTargetType('kubernetes')}>
                <Boxes size={15} />
                Kubernetes 集群
              </button>
              <button className={targetType === 'linux' ? 'selected' : ''} onClick={() => setTargetType('linux')}>
                <ServerCog size={15} />
                Linux 主机
              </button>
              <button className={targetType === 'windows' ? 'selected' : ''} onClick={() => setTargetType('windows')}>
                <ServerCog size={15} />
                Windows 主机
              </button>
              <button className={targetType === 'macos' ? 'selected' : ''} onClick={() => setTargetType('macos')}>
                <ServerCog size={15} />
                macOS 主机
              </button>
            </div>
          </div>
        )}
        <div className="onboarding-progress" aria-label="接入进度">
          {[targetType === 'linux' ? '安装 Connector' : '安装 Operator', '确认权限范围', '等待就绪'].map((label, index) => (
            <div className={step >= index + 1 ? 'active' : ''} key={label}>
              <span>{index + 1}</span>
              <strong>{label}</strong>
            </div>
          ))}
        </div>
        {step === 1 && (
          <div className="onboarding-body">
            <div className="onboarding-callout">
              <TerminalSquare size={20} />
              <div>
                <strong>
                  {targetType === 'linux'
                    ? '在目标 Linux 主机执行一次'
                    : hostPlatform
                      ? `在目标 ${hostPlatform === 'windows' ? 'Windows' : 'macOS'} 主机执行一次`
                      : '在目标集群执行一次'}
                </strong>
                <span>
                  {targetType === 'linux'
                    ? '安装器创建非 root systemd 服务，并从目标机发现可授权的服务。'
                    : hostPlatform
                      ? 'Connector 通过 HTTPS 主动连接控制面；配对后仅执行白名单程序。'
                      : 'Operator 会自动识别 k3s、生成短期配对码并管理 Connector 生命周期。'}
                </span>
              </div>
            </div>
            {installConfigError ? (
              <div className="credential-note error-note">
                <AlertTriangle size={17} />
                <span>{installConfigError}</span>
              </div>
            ) : (
              <div className="command-block">
                <code>{activeInstallCommand || '正在读取当前发布配置...'}</code>
                <button
                  className="icon-button"
                  onClick={copyInstall}
                  aria-label="复制安装命令"
                  title="复制安装命令"
                  disabled={!activeInstallCommand}
                >
                  {copied ? <Check size={16} /> : <Copy size={16} />}
                </button>
              </div>
            )}
            {installConfig && (
              <div className="onboarding-hint">
                <ShieldCheck size={17} />
                <span>当前发布版本：{installConfig.releaseVersion}</span>
              </div>
            )}
            {targetType === 'kubernetes' && (
              <label>
                <span>私有镜像拉取 Secret（公共镜像留空）</span>
                <input value={imagePullSecret} onChange={(e) => setImagePullSecret(e.target.value)} placeholder="例如 swr-secret" />
              </label>
            )}
            <div className="pairing-code-help">
              <strong>安装后获取配对码</strong>
              <span>
                日志中的{' '}
                <code>
                  {targetType === 'linux'
                    ? 'Warden Linux pairing code'
                    : hostPlatform
                      ? `Warden ${hostPlatform} host pairing code`
                      : 'Warden pairing code'}
                  : XXXX-XXXX
                </code>{' '}
                就是下一步需要输入的配对码。
              </span>
              <div className="command-block">
                <code>
                  {targetType === 'linux'
                    ? 'sudo journalctl -u warden-linux-connector -f'
                    : hostPlatform === 'windows'
                      ? 'type C:\\ProgramData\\warden-host\\connector.log'
                      : hostPlatform
                        ? 'sudo tail -f /var/log/warden-host-connector.log'
                        : 'kubectl logs -n warden-system deployment/warden-operator -f'}
                </code>
              </div>
            </div>
            <div className="onboarding-hint">
              <ShieldCheck size={17} />
              <span>
                {targetType === 'linux'
                  ? '不上传 SSH 凭据，不开放任意 Shell；配对码 10 分钟失效。'
                  : '不会上传 kubeconfig；配对码 10 分钟失效，Connector 使用独立加密任务 Key。'}
              </span>
            </div>
            <div className="drawer-actions">
              <button className="secondary-button" onClick={onClose}>
                取消
              </button>
              <button className="primary-button" onClick={() => setStep(2)} disabled={!installConfig}>
                <ArrowRight size={16} />
                我已安装
              </button>
            </div>
          </div>
        )}
        {step === 2 && (
          <form className="onboarding-body" onSubmit={confirm}>
            <div className="pairing-code-entry">
              <label className="pairing-code-field">
                <span>配对码</span>
                <input
                  autoFocus
                  value={code}
                  onChange={(e) => {
                    setCode(e.target.value.toUpperCase())
                    setNamespaceOptions([])
                    setSelectedNamespaces([])
                  }}
                  placeholder="ABCD-2345"
                  pattern="[A-Z2-9]{4}-[A-Z2-9]{4}"
                  required
                />
              </label>
              <button
                type="button"
                className="secondary-button"
                onClick={loadOptions}
                disabled={optionsLoading || !/^[A-Z2-9]{4}-[A-Z2-9]{4}$/.test(code)}
              >
                {optionsLoading ? <Loader2 size={16} className="spin" /> : <Network size={16} />}读取目标
              </button>
            </div>
            <div className="form-grid compact-grid">
              <label>
                <span>{targetType === 'linux' ? '主机名称' : '集群名称'}</span>
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={targetType === 'linux' ? '例如 api-prod-01' : '例如 k3s-shanghai-test'}
                  required
                />
              </label>
              <label>
                <span>环境</span>
                <div className="select-wrap full">
                  <select value={environment} onChange={(e) => setEnvironment(e.target.value as 'development' | 'testing' | 'production')}>
                    <option value="development">开发</option>
                    <option value="testing">测试</option>
                    <option value="production">生产</option>
                  </select>
                  <ChevronDown size={15} />
                </div>
              </label>
              <label>
                <span>归属项目</span>
                <div className="select-wrap full">
                  <select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
                    <option value="">{projects.find((p) => p.is_default)?.name ?? '默认项目'}</option>
                    {projects
                      .filter((p) => !p.is_default)
                      .map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.name}
                        </option>
                      ))}
                  </select>
                  <ChevronDown size={15} />
                </div>
              </label>
            </div>
            <div className="onboarding-hint">
              <ShieldCheck size={17} />
              <span>该主机或集群发现的原生资源会继承所选项目；之后可在目标设置或资源目录中调整。中间件继续保持独立项目归属。</span>
            </div>
            {targetType === 'linux' && Object.keys(machine).length > 0 && (
              <div className="machine-summary">
                <strong>{String(machine.hostname ?? name)}</strong>
                <span>
                  {String(machine.pretty_name ?? machine.os ?? 'Linux')} · {String(machine.arch ?? '-')}
                </span>
              </div>
            )}
            {namespaceOptions.length > 0 && (
              <fieldset className="namespace-picker">
                <legend>{targetType === 'linux' ? '允许访问的 systemd 服务' : '允许访问的 Namespace'}</legend>
                <div>
                  {namespaceOptions.map((namespace) => (
                    <label key={namespace}>
                      <input type="checkbox" checked={selectedNamespaces.includes(namespace)} onChange={() => toggleNamespace(namespace)} />
                      <span>{namespace}</span>
                    </label>
                  ))}
                </div>
                <small>
                  {targetType === 'linux'
                    ? '未勾选的服务不会进入资源目录，也不能被任务访问。主机只读信息始终可见。'
                    : '仅为勾选的 Namespace 创建执行和资源发现权限。'}
                </small>
              </fieldset>
            )}
            {targetType === 'linux' && containerOptions.length > 0 && (
              <fieldset className="namespace-picker">
                <legend>允许访问的 Docker 容器</legend>
                <div>
                  {containerOptions.map((container) => (
                    <label key={container}>
                      <input
                        type="checkbox"
                        checked={selectedContainers.includes(container)}
                        onChange={() =>
                          setSelectedContainers((current) =>
                            current.includes(container) ? current.filter((item) => item !== container) : [...current, container],
                          )
                        }
                      />
                      <span>{container.slice(0, 12)}</span>
                    </label>
                  ))}
                </div>
                <small>容器按发现时的稳定 ID 授权；同名重建的容器不会继承权限。</small>
              </fieldset>
            )}
            {hostPlatform && commandOptions.length > 0 && (
              <fieldset className="namespace-picker">
                <legend>允许执行的本机程序</legend>
                <div>
                  {commandOptions.map((command) => (
                    <label key={command}>
                      <input
                        type="checkbox"
                        checked={selectedCommands.includes(command)}
                        onChange={() =>
                          setSelectedCommands((current) =>
                            current.includes(command) ? current.filter((item) => item !== command) : [...current, command],
                          )
                        }
                      />
                      <span>{command}</span>
                    </label>
                  ))}
                </div>
                <small>仅允许直接执行已发现程序，参数仍受控制面策略和审批约束。</small>
              </fieldset>
            )}
            {!optionsLoading && code && namespaceOptions.length === 0 && !error && (
              <div className="onboarding-hint">
                <Network size={17} />
                <span>先点击“读取目标”，再选择允许访问的{targetType === 'linux' ? ' systemd 服务' : ' Namespace'}。</span>
              </div>
            )}
            {error && (
              <div className="credential-note error-note">
                <AlertTriangle size={17} />
                <span>{error}</span>
              </div>
            )}
            <div className="drawer-actions">
              <button type="button" className="secondary-button" onClick={() => setStep(1)}>
                上一步
              </button>
              <button
                className="primary-button"
                type="submit"
                disabled={submitting || (targetType === 'kubernetes' && selectedNamespaces.length === 0)}
              >
                {submitting ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}确认配对
              </button>
            </div>
          </form>
        )}
        {step === 3 && (
          <div className="onboarding-body">
            <div className={`pairing-status ${ready ? 'ready' : ''}`}>
              {ready ? <CheckCircle2 size={28} /> : <Loader2 size={28} className="spin" />}
              <div>
                <strong>{ready ? `${targetType === 'linux' ? 'Linux 主机' : 'Kubernetes 集群'}已接入` : '正在配置执行代理'}</strong>
                <span>
                  {ready
                    ? '目标与执行通道已关联，资源同步将自动持续运行。'
                    : targetType === 'linux'
                      ? 'Linux 执行代理正在领取独立凭据并启动。'
                      : 'Operator 正在创建最小权限 RBAC、凭证并等待执行代理首次心跳。'}
                </span>
              </div>
            </div>
            <div className="target-link-result">
              <strong>{name || (targetType === 'linux' ? 'Linux 主机' : 'Kubernetes 集群')}</strong>
              <span>目标</span>
              <ArrowRight size={18} />
              <strong>
                {targetType === 'linux' ? `warden-linux-connector-${connectorId.slice(0, 8)}` : `warden-connector-${connectorId.slice(0, 8)}`}
              </strong>
              <span>执行通道</span>
            </div>
            <div className="status-rows">
              <div>
                <span>执行代理 ID</span>
                <code>{connectorId}</code>
              </div>
              <div>
                <span>当前状态</span>
                <Badge tone={status === 'ACTIVE' ? 'success' : 'warning'}>{status}</Badge>
              </div>
            </div>
            <div className="drawer-actions">
              <button className="secondary-button" onClick={onClose}>
                关闭
              </button>
              <button className="primary-button" onClick={() => onComplete(connectorId)} disabled={!ready}>
                <Check size={16} />
                完成
              </button>
            </div>
          </div>
        )}
      </aside>
    </>
  )
}
