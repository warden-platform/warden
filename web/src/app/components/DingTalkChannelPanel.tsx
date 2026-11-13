import { useEffect, useState } from 'react'
import { BellRing, Bot, CheckCircle2, ChevronDown, CircleAlert, Radio, Send, ShieldAlert, UserRoundCheck } from 'lucide-react'
import type { ApiError, ApiNotificationChannel } from '../../shared/api'
import { getDingTalkChannel, testDingTalkChannel, updateDingTalkChannel } from '../../shared/api'
import { useApp } from './Toast'

const EVENTS = [
  { value: 'access_request.created', label: '新审批申请', hint: '审批进入待处理状态时通知' },
  { value: 'access_request.approved', label: '审批通过', hint: '审批通过并进入执行队列时通知' },
  { value: 'access_request.rejected', label: '审批拒绝', hint: '审批被管理员拒绝时通知' },
  { value: 'access_request.expired', label: '审批过期', hint: '审批超过有效期未处理时通知' },
  { value: 'access_request.cancelled', label: '申请取消', hint: '申请方取消待审批执行时同步更新卡片' },
]

export function DingTalkChannelPanel() {
  const [channel, setChannel] = useState<ApiNotificationChannel | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testSentAt, setTestSentAt] = useState<Date | null>(null)
  const [error, setError] = useState('')
  const { notify } = useApp()

  const load = () => {
    setLoading(true)
    setError('')
    getDingTalkChannel()
      .then(setChannel)
      .catch((e: ApiError) => setError(e.message || '加载钉钉渠道失败'))
      .finally(() => setLoading(false))
  }
  useEffect(load, [])

  const setEvent = (event: string, checked: boolean) => {
    if (!channel) return
    const events = checked ? [...new Set([...channel.events, event])] : channel.events.filter((item) => item !== event)
    setChannel({ ...channel, events })
  }

  const save = () => {
    if (!channel) return
    if (!channel.name.trim()) return notify('请填写渠道名称')
    if (channel.events.length === 0) return notify('至少选择一个通知事件')
    if (channel.enabled && (!channel.card_configured || !channel.stream_configured)) return notify('请先完成互动卡片和 Stream 回调配置')
    setSaving(true)
    updateDingTalkChannel({ name: channel.name.trim(), enabled: channel.enabled, events: channel.events })
      .then((updated) => { setChannel(updated); notify('钉钉审批通知配置已保存') })
      .catch((e: ApiError) => notify(e.message || '保存失败'))
      .finally(() => setSaving(false))
  }

  const test = () => {
    setTesting(true)
    setTestSentAt(null)
    testDingTalkChannel()
      .then(() => {
        setTestSentAt(new Date())
        notify('测试卡片已发送，请在钉钉群内点击按钮验证回调')
      })
      .catch((e: ApiError) => notify(e.message || '渠道测试失败'))
      .finally(() => setTesting(false))
  }

  if (loading) return <section className="panel"><p className="empty-hint">正在加载钉钉通知渠道…</p></section>
  if (error || !channel) return <section className="panel"><div className="credential-note error-note"><ShieldAlert size={16} /><span>{error || '渠道配置不可用'}</span></div><button className="quiet-button" onClick={load}>重试</button></section>

  const workflowReady = channel.card_configured && channel.stream_configured
  const statusLabel = !workflowReady ? '配置不完整' : channel.enabled ? '运行中' : '已就绪 · 未启用'
  const statusTone = workflowReady && channel.enabled ? 'badge-success' : workflowReady ? 'badge-info' : 'badge-warning'

  return (
    <section className="panel">
      <div className="panel-header">
        <div>
          <span className="eyebrow"><BellRing size={15} />审批通知渠道</span>
          <h2>钉钉审批</h2>
          <p className="panel-desc">应用机器人向群聊发送互动卡片，审批人可在卡片内直接同意或拒绝；Stream 通道负责安全接收按钮事件。</p>
        </div>
        <span className={`badge ${statusTone}`}>
          {workflowReady ? <CheckCircle2 size={12} /> : <CircleAlert size={12} />}{statusLabel}
        </span>
      </div>

      <div className="dingtalk-workflow" aria-label="钉钉审批链路状态">
        <div className={channel.card_configured ? 'ready' : 'missing'}>
          <span><Bot size={17} /></span>
          <div><strong>互动卡片</strong><small>{channel.card_configured ? '机器人可向审批群发送卡片' : '缺少机器人或卡片模板配置'}</small></div>
          <b>{channel.card_configured ? '可用' : '待配置'}</b>
        </div>
        <div className={channel.stream_configured ? 'ready' : 'missing'}>
          <span><Radio size={17} /></span>
          <div><strong>Stream 回调</strong><small>{channel.stream_configured ? '可接收同意和拒绝按钮事件' : 'Stream 回调尚未就绪'}</small></div>
          <b>{channel.stream_configured ? '可用' : '待配置'}</b>
        </div>
        <div className={channel.stream_configured ? 'ready' : 'missing'}>
          <span><UserRoundCheck size={17} /></span>
          <div><strong>处理人识别</strong><small>{channel.stream_configured ? '优先查询姓名，失败时记录 User ID' : '回调就绪后自动记录处理人'}</small></div>
          <b>{channel.stream_configured ? '自动' : '待配置'}</b>
        </div>
      </div>

      {!workflowReady && <div className="credential-note" role="alert"><ShieldAlert size={16} /><span>完成互动卡片和 Stream 回调配置后即可启用钉钉审批。部署所需 Secret 可在下方“部署与兼容配置”中查看。</span></div>}

      <div className="notification-channel-grid">
        <label htmlFor="dingtalk-channel-name"><span>渠道名称</span><input id="dingtalk-channel-name" value={channel.name} maxLength={100} onChange={(e) => setChannel({ ...channel, name: e.target.value })} /></label>
        <label className="channel-enable" htmlFor="dingtalk-channel-enabled"><span>渠道状态</span><span><input id="dingtalk-channel-enabled" type="checkbox" checked={channel.enabled} disabled={!workflowReady && !channel.enabled} onChange={(e) => setChannel({ ...channel, enabled: e.target.checked })} />启用互动卡片审批通知</span></label>
      </div>

      <div className="notification-events">
        <strong>通知范围</strong>
        <div>
          {EVENTS.map((event) => (
            <label key={event.value}>
              <input type="checkbox" checked={channel.events.includes(event.value)} onChange={(e) => setEvent(event.value, e.target.checked)} />
              <span><b>{event.label}</b><small>{event.hint}</small></span>
            </label>
          ))}
        </div>
      </div>

      <div className="form-actions notification-actions">
        <button className="quiet-button" disabled={!channel.card_configured || testing} title={channel.card_configured ? '' : '请先配置互动卡片'} onClick={test}><Send size={15} />{testing ? '发送中…' : '发送测试卡片'}</button>
        <button className="primary-button" disabled={saving} onClick={save}>{saving ? '保存中…' : '保存配置'}</button>
      </div>

      {testSentAt && (
        <div className="dingtalk-test-result" role="status" aria-live="polite">
          <CheckCircle2 size={17} />
          <div><strong>测试卡片已送达</strong><span>请在钉钉群内点击“同意”或“拒绝”。卡片状态随即更新，表示 Stream 回调链路正常。</span><small>发送于 {testSentAt.toLocaleTimeString('zh-CN', { hour12: false })}</small></div>
        </div>
      )}

      <details className="dingtalk-advanced">
        <summary><span>部署与兼容配置</span><ChevronDown size={15} /></summary>
        <div>
          <p>互动卡片需要以下 Secret 文件，并复用企业内部应用的 Client ID 和 Client Secret。修改后需重启 control 服务。</p>
          <ul>
            <li><code>PAM_DINGTALK_CARD_TEMPLATE_ID_FILE</code></li>
            <li><code>PAM_DINGTALK_OPEN_CONVERSATION_ID_FILE</code></li>
            <li><code>PAM_DINGTALK_ROBOT_CODE_FILE</code></li>
          </ul>
          <p>企业内部应用需要开通“成员信息读权限”（<code>qyapi_get_member</code>），用于根据回调 User ID 查询处理人姓名；查询失败时会记录 User ID，不会阻断审批。</p>
          <p>Webhook {channel.webhook_configured ? '已配置' : '未配置'}，启用时会同步发送兼容文本通知。旧酷应用接口仅保留兼容，不参与当前卡片审批主流程。</p>
        </div>
      </details>
    </section>
  )
}
