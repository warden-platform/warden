import { useEffect, useRef, useState } from 'react'
import { Check, Clock3, FileClock, Loader2, RefreshCw, XCircle } from 'lucide-react'
import type { ApiError, DingTalkCoolAppApproval } from '../../shared/api'
import { authenticateDingTalkCoolApp, decideDingTalkCoolAppApproval, getDingTalkCoolAppConfig, listDingTalkCoolAppApprovals } from '../../shared/api'

type DingTalkJS = {
  ready: (callback: () => void) => void
  error?: (callback: (error: unknown) => void) => void
  requestAuthCode?: (options: { corpId: string; clientId: string }) => Promise<{ code: string }>
}

async function loadDingTalkJS(): Promise<DingTalkJS> {
  const sdk = await import('dingtalk-jsapi')
  return ((sdk as { default?: unknown }).default ?? sdk) as unknown as DingTalkJS
}

function dingTalkContext(params: URLSearchParams) {
  const corpId = [...params.getAll('corpId'), ...params.getAll('corpid')]
    .map((value) => value.trim().replace(/^\$+|\$+$/g, ''))
    .find((value) => /^ding[a-z0-9]+$/i.test(value)) ?? ''
  const openConversationId = params.getAll('openConversationId')
    .map((value) => value.trim().replace(/^\$+|\$+$/g, ''))
    .reverse()
    .find((value) => value !== '' && !/^(ENCCID|ENCID|OPENCONVERSATIONID)$/i.test(value)) ?? ''
  return { corpId, openConversationId }
}

function requestDingTalkAuthCode(dd: DingTalkJS, corpId: string, clientId: string): Promise<string> {
  return new Promise((resolve, reject) => {
    dd.ready(() => {
      const requestAuthCode = dd.requestAuthCode
	  if (!requestAuthCode) return reject(new Error('当前钉钉客户端不支持免登 JSAPI'))
      requestAuthCode({ corpId, clientId }).then((result) => resolve(result.code)).catch(reject)
    })
    dd.error?.(reject)
  })
}

export function DingTalkApprovalsPage() {
  const started = useRef(false)
  const [approvals, setApprovals] = useState<DingTalkCoolAppApproval[]>([])
  const [phase, setPhase] = useState<'loading' | 'ready' | 'error'>('loading')
  const [message, setMessage] = useState('正在连接 PAM 审批服务…')
  const [acting, setActing] = useState('')

  const loadApprovals = () => listDingTalkCoolAppApprovals().then((items) => {
    setApprovals(items ?? []); setPhase('ready'); setMessage('')
  })

  useEffect(() => {
    if (started.current) return
    started.current = true
    const params = new URLSearchParams(window.location.search)
    const { corpId, openConversationId } = dingTalkContext(params)

    loadApprovals().catch(async () => {
      if (!corpId) { setPhase('error'); setMessage('缺少 corpId，请从钉钉群快捷入口打开本页面'); return }
      try {
        setMessage('正在进行钉钉免登认证…')
		const config = await getDingTalkCoolAppConfig()
        const dd = await loadDingTalkJS()
		const authCode = await requestDingTalkAuthCode(dd, corpId, config.client_id)
        await authenticateDingTalkCoolApp({ auth_code: authCode, corp_id: corpId, open_conversation_id: openConversationId })
        await loadApprovals()
      } catch (error) {
        const apiError = error as ApiError
        const clientError = error as { errorCode?: string | number; errorMessage?: string }
        const clientDetail = clientError.errorMessage
          ? `钉钉 JSAPI 失败${clientError.errorCode ? ` (${clientError.errorCode})` : ''}：${clientError.errorMessage}`
          : ''
        setPhase('error'); setMessage(apiError.message || clientDetail || '钉钉免登失败，请关闭后从群快捷入口重新打开')
      }
    })
  }, [])

  const decide = (approval: DingTalkCoolAppApproval, approve: boolean) => {
    setActing(approval.id)
    decideDingTalkCoolAppApproval(approval.id, approve)
      .then(() => setApprovals((items) => items.filter((item) => item.id !== approval.id)))
      .catch((error: ApiError) => setMessage(error.message || '审批处理失败'))
      .finally(() => setActing(''))
  }

  if (phase === 'loading') return <main className="coolapp-shell"><div className="coolapp-state"><Loader2 className="spin" size={28} /><strong>{message}</strong></div></main>
  if (phase === 'error') return <main className="coolapp-shell"><div className="coolapp-state error"><XCircle size={32} /><h1>无法打开审批</h1><p>{message}</p></div></main>

  return <main className="coolapp-shell">
    <header className="coolapp-header"><div><span>PAM · 钉钉酷应用</span><h1>访问审批</h1></div><button onClick={() => { setMessage(''); loadApprovals().catch((e: ApiError) => setMessage(e.message)) }}><RefreshCw size={16} />刷新</button></header>
    {message && <div className="coolapp-alert">{message}</div>}
    {!approvals.length ? <div className="coolapp-empty"><FileClock size={36} /><strong>没有待处理的审批</strong><p>新的授权申请会显示在这里。</p></div> : <section className="coolapp-list">{approvals.map((approval) => <article key={approval.id} className="coolapp-approval">
      <div className="coolapp-approval-title"><span>{approval.risk_level || 'high'} · {approval.environment || 'production'}</span><small><Clock3 size={13} />{approval.expires_at ? new Date(approval.expires_at).toLocaleString() : '无固定期限'}</small></div>
      <h2>{approval.action_name || '未知动作'}</h2><h3>{approval.resource_type} / {approval.resource_name}</h3>
      <p>{approval.reason || '未填写申请原因'}</p>
      <dl><div><dt>申请方</dt><dd>{approval.applicant || '未知'}</dd></div><div><dt>执行参数</dt><dd><code>{JSON.stringify(approval.parameters)}</code></dd></div></dl>
      <div className="coolapp-actions"><button className="reject" disabled={acting === approval.id} onClick={() => decide(approval, false)}><XCircle size={16} />拒绝</button><button className="approve" disabled={acting === approval.id} onClick={() => decide(approval, true)}>{acting === approval.id ? <Loader2 className="spin" size={16} /> : <Check size={16} />}同意并执行</button></div>
    </article>)}</section>}
  </main>
}
