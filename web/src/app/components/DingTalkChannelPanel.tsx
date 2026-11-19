import { useState } from 'react'
import { Bot, CheckCircle2, ChevronDown, CircleAlert, Info, Radio, Send, ShieldAlert, UserRoundCheck } from 'lucide-react'
import type { ApiError, ApiNotificationChannel } from '../../shared/api'
import { getDingTalkChannel, testDingTalkChannel, updateDingTalkChannel } from '../../shared/api'
import { useApp } from './Toast'
import { useApiQuery } from '../lib/useApiQuery'

const EVENTS = [
  { value: 'access_request.created', label: '新审批申请', hint: '审批进入待处理状态时通知' },
  { value: 'access_request.approved', label: '审批通过', hint: '审批通过并进入执行队列时通知' },
  { value: 'access_request.rejected', label: '审批拒绝', hint: '审批被管理员拒绝时通知' },
  { value: 'access_request.expired', label: '审批过期', hint: '审批超过有效期未处理时通知' },
  { value: 'access_request.cancelled', label: '申请取消', hint: '申请方取消待审批执行时同步更新卡片' },
]

export function DingTalkChannelPanel() {
  const channelQuery = useApiQuery(() => getDingTalkChannel(), [])
  // Local edit draft over the query data; cleared on save/reload.
  const [draft, setDraft] = useState<ApiNotificationChannel | null>(null)
  const channel = draft ?? channelQuery.data
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testSentAt, setTestSentAt] = useState<Date | null>(null)
  const loading = channelQuery.loading
  const error = channelQuery.error
  const { notify, auth } = useApp()
  const isPlatformTenant = (auth.user?.tenant?.slug ?? 'platform') === 'platform'

  const load = () => {
    setDraft(null)
    channelQuery.reload()
  }

  const setEvent = (event: string, checked: boolean) => {
    if (!channel) return
    const events = checked ? [...new Set([...channel.events, event])] : channel.events.filter((item) => item !== event)
    setDraft({ ...channel, events })
  }

  const save = () => {
    if (!channel) return
    if (!channel.name.trim()) return notify('请填写渠道名称', 'error')
    if (channel.events.length === 0) return notify('至少选择一个通知事件', 'error')
    if (channel.enabled && (!channel.card_configured || !channel.stream_configured)) return notify('请先完成互动卡片和 Stream 回调配置', 'error')
    const conversationID = (channel.open_conversation_id ?? '').trim()
    if (channel.enabled && !conversationID && !isPlatformTenant) return notify('启用前请先绑定本组织的审批群 ID', 'error')
    setSaving(true)
    updateDingTalkChannel({ name: channel.name.trim(), enabled: channel.enabled, events: channel.events, open_conversation_id: conversationID })
      .then(() => {
        setDraft(null)
        void channelQuery.reload(false)
        notify('钉钉审批通知配置已保存')
      })
      .catch((e: ApiError) => notify(e.message || '保存失败', 'error'))
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
      .catch((e: ApiError) => notify(e.message || '渠道测试失败', 'error'))
      .finally(() => setTesting(false))
  }

  if (loading)
    return (
      <section className="panel">
        <p className="empty-hint">正在加载钉钉通知渠道…</p>
      </section>
    )
  if (error || !channel)
    return (
      <section className="panel">
        <div className="px-5 py-4">
          <div className="credential-note error-note">
            <ShieldAlert size={16} />
            <span>{error || '渠道配置不可用'}</span>
          </div>
          <button className="quiet-button" onClick={load}>
            重试
          </button>
        </div>
      </section>
    )

  const workflowReady = channel.card_configured && channel.stream_configured
  const statusLabel = !workflowReady ? '配置不完整' : channel.enabled ? '运行中' : '已就绪 · 未启用'
  const statusTone = workflowReady && channel.enabled ? 'badge-success' : workflowReady ? 'badge-info' : 'badge-warning'

  return (
    <section className="panel">
      <div className="panel-header">
        <div>
          <h2>钉钉审批</h2>
          <p className="panel-desc">审批人在群内互动卡片上直接同意或拒绝</p>
        </div>
        <span className={`badge ${statusTone}`}>
          {workflowReady ? <CheckCircle2 size={12} /> : <CircleAlert size={12} />}
          {statusLabel}
        </span>
      </div>

      <div className="dingtalk-pipeline" aria-label="钉钉审批链路状态">
        <div
          className={channel.card_configured ? 'ready' : 'missing'}
          title={channel.card_configured ? '机器人可向审批群发送卡片' : '缺少机器人或卡片模板配置'}
        >
          <span>
            <Bot size={15} />
          </span>
          <div>
            <strong>互动卡片</strong>
            <b>{channel.card_configured ? '已配置' : '待配置'}</b>
          </div>
        </div>
        <i />
        <div
          className={channel.stream_configured ? 'ready' : 'missing'}
          title={channel.stream_configured ? '可接收同意和拒绝按钮事件' : 'Stream 回调尚未就绪'}
        >
          <span>
            <Radio size={15} />
          </span>
          <div>
            <strong>Stream 回调</strong>
            <b>{channel.stream_configured ? '已配置' : '待配置'}</b>
          </div>
        </div>
        <i />
        <div
          className={channel.stream_configured ? 'ready' : 'missing'}
          title={channel.stream_configured ? '优先查询处理人姓名，失败时记录 User ID' : '回调就绪后自动记录处理人'}
        >
          <span>
            <UserRoundCheck size={15} />
          </span>
          <div>
            <strong>处理人识别</strong>
            <b>{channel.stream_configured ? '自动' : '待配置'}</b>
          </div>
        </div>
      </div>

      {!workflowReady && (
        <div className="px-5 py-4">
          <div className="credential-note" role="alert">
            <ShieldAlert size={16} />
            <span>完成互动卡片和 Stream 回调配置后即可启用钉钉审批。部署所需 Secret 可在下方「部署提示」中查看。</span>
          </div>
        </div>
      )}

      <div className="dingtalk-form">
        <label htmlFor="dingtalk-channel-name" className="dingtalk-field">
          <span>渠道名称</span>
          <input
            id="dingtalk-channel-name"
            value={channel.name}
            maxLength={100}
            onChange={(e) => setDraft({ ...channel, name: e.target.value })}
          />
        </label>
        <label htmlFor="dingtalk-channel-conversation" className="dingtalk-field">
          <span>本组织审批群 ID（openConversationId）</span>
          <input
            id="dingtalk-channel-conversation"
            value={channel.open_conversation_id ?? ''}
            maxLength={512}
            placeholder={isPlatformTenant ? '留空则使用部署级审批群配置' : '例如 cidXXXXXXXX=='}
            onChange={(e) => setDraft({ ...channel, open_conversation_id: e.target.value })}
          />
        </label>
        <label
          className="dingtalk-toggle"
          htmlFor="dingtalk-channel-enabled"
          title={channel.enabled ? '' : '完成互动卡片和 Stream 回调配置后可启用'}
        >
          <span>
            <b>启用审批通知</b>
            <small>开启后向审批群发送互动卡片，审批人可在卡片内直接操作</small>
          </span>
          <input
            id="dingtalk-channel-enabled"
            type="checkbox"
            checked={channel.enabled}
            disabled={!workflowReady && !channel.enabled}
            onChange={(e) => setDraft({ ...channel, enabled: e.target.checked })}
          />
        </label>
      </div>

      <div className="notification-events">
        <strong>通知范围</strong>
        <div className="event-chips">
          {EVENTS.map((event) => (
            <label key={event.value} className="event-chip" title={event.hint}>
              <input
                type="checkbox"
                checked={channel.events.includes(event.value)}
                onChange={(e) => setEvent(event.value, e.target.checked)}
              />
              {event.label}
            </label>
          ))}
        </div>
      </div>

      <div className="form-actions notification-actions">
        <button
          className="quiet-button"
          disabled={!channel.card_configured || testing}
          title={channel.card_configured ? '' : '请先配置互动卡片'}
          onClick={test}
        >
          <Send size={15} />
          {testing ? '发送中…' : '发送测试卡片'}
        </button>
        <button className="primary-button" disabled={saving} onClick={save}>
          {saving ? '保存中…' : '保存配置'}
        </button>
      </div>

      {testSentAt && (
        <div className="dingtalk-test-result" role="status" aria-live="polite">
          <CheckCircle2 size={17} />
          <div>
            <strong>测试卡片已送达</strong>
            <span>请在钉钉群内点击“同意”或“拒绝”。卡片状态随即更新，表示 Stream 回调链路正常。</span>
            <small>发送于 {testSentAt.toLocaleTimeString('zh-CN', { hour12: false })}</small>
          </div>
        </div>
      )}

      <details className="dingtalk-advanced">
        <summary>
          <span className="summary-text">
            <Info size={14} />
            部署提示
          </span>
          <ChevronDown size={15} />
        </summary>
        <div className="dingtalk-advanced-body">
          <p className="adv-intro">启用前请确认以下条件，修改后需重启 control 服务生效。</p>
          <div className="adv-row">
            <span className="adv-label">Secret 文件</span>
            <div className="adv-codes">
              <code>WARDEN_DINGTALK_CARD_TEMPLATE_ID_FILE</code>
              <code>WARDEN_DINGTALK_OPEN_CONVERSATION_ID_FILE</code>
              <code>WARDEN_DINGTALK_ROBOT_CODE_FILE</code>
            </div>
          </div>
          <div className="adv-row">
            <span className="adv-label">应用权限</span>
            <p>
              企业内部应用需开通「成员信息读权限」（<code>qyapi_get_member</code>
              ），根据回调 User ID
              查询处理人姓名；查询失败时记录 User ID，不阻断审批。
            </p>
          </div>
          <div className="adv-row">
            <span className="adv-label">兼容说明</span>
            <p>
              复用企业内部应用的 Client ID / Client
              Secret，修改 Secret 文件后需重启 control 服务。部署级群配置（
              <code>WARDEN_DINGTALK_OPEN_CONVERSATION_ID</code>）仅作用于平台管理域；业务组织必须在上方绑定自己的审批群，审批数据与卡片严格按组织隔离。Webhook{' '}
              {channel.webhook_configured ? '已配置' : '未配置'}
              ，仅平台管理域启用时同步发送兼容文本通知；旧酷应用接口仅保留兼容，不参与当前卡片审批主流程。
            </p>
          </div>
        </div>
      </details>
    </section>
  )
}
