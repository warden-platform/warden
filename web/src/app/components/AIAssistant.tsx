import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import {
  Activity,
  ArrowUp,
  Bot,
  FileSearch,
  MessageSquareText,
  Plus,
  ScrollText,
  ShieldCheck,
  Sparkles,
  X,
  type LucideIcon,
} from 'lucide-react'
import { useLocation } from '../../shared/router'
import { API_BASE, getObservedTenant } from '../../shared/api/core'
import assistantAvatarUrl from '../../assets/warden-ai-avatar.svg'

type AssistantMode = 'agent' | 'chat'
type Message = { id: number; role: 'user' | 'assistant'; text: string }

const pageContext: Record<string, { title: string; prompts: string[] }> = {
  '/overview': { title: '运行总览', prompts: ['总结当前环境的运行状态', '有哪些需要优先关注的风险？'] },
  '/resources': { title: '资源目录', prompts: ['帮我查找生产环境资源', '哪些资源最近发生了异常？'] },
  '/executions': { title: '执行记录', prompts: ['分析最近失败的执行', '汇总今天的高风险操作'] },
  '/approvals': { title: '访问审批', prompts: ['总结当前待审批申请', '这些申请有哪些风险信号？'] },
  '/audit': { title: '审计追踪', prompts: ['调查最近的敏感操作', '生成今天的审计摘要'] },
  '/policies': { title: '策略中心', prompts: ['检查当前策略是否过宽', '解释最近一次的拒绝原因'] },
}

const capabilities: { label: string; prompt: string; icon: LucideIcon }[] = [
  { label: '异常诊断', prompt: '帮我诊断当前环境中需要优先处理的异常', icon: Activity },
  { label: '资源查询', prompt: '帮我查询当前组织中的资源', icon: FileSearch },
  { label: '执行分析', prompt: '分析最近的执行结果和失败原因', icon: Sparkles },
  { label: '审批解读', prompt: '解释当前待审批申请的风险与授权边界', icon: ShieldCheck },
  { label: '策略检查', prompt: '检查当前策略配置中的潜在风险', icon: ScrollText },
  { label: '审计调查', prompt: '调查最近的敏感操作并整理时间线', icon: MessageSquareText },
]

function resolveContext(pathname: string) {
  if (pageContext[pathname]) return pageContext[pathname]
  if (pathname.startsWith('/resources')) return pageContext['/resources']
  if (pathname.startsWith('/integrations')) return { title: '中间件详情', prompts: ['总结这个集成的健康状态', '最近有哪些失败调用？'] }
  if (pathname.startsWith('/connectors') || pathname.startsWith('/targets')) {
    return { title: '执行通道详情', prompts: ['检查 Connector 连接状态', '分析最近的通道异常'] }
  }
  return { title: 'Warden 控制台', prompts: ['当前有哪些需要关注的风险？', '帮我找到最近的失败执行'] }
}

export function AIAssistant() {
  const location = useLocation()
  const context = resolveContext(location.pathname)
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<AssistantMode>('agent')
  const [input, setInput] = useState('')
  const [messages, setMessages] = useState<Message[]>([])
  const [conversationId, setConversationId] = useState('')
  const [sending, setSending] = useState(false)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const launcherRef = useRef<HTMLButtonElement>(null)
  const messageEndRef = useRef<HTMLDivElement>(null)
  const nextMessageId = useRef(1)
  const requestRef = useRef<AbortController | null>(null)

  useEffect(() => {
    if (open) inputRef.current?.focus()
  }, [open])

  useEffect(() => {
    messageEndRef.current?.scrollIntoView({ block: 'nearest' })
  }, [messages])

  useEffect(() => {
    if (!open) return
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false)
        launcherRef.current?.focus()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [open])

  const choosePrompt = (prompt: string) => {
    setInput(prompt)
    inputRef.current?.focus()
  }

  const submit = async (event?: FormEvent) => {
    event?.preventDefault()
    const text = input.trim()
    if (!text || sending) return
    const userMessage: Message = { id: nextMessageId.current++, role: 'user', text }
    const assistantId = nextMessageId.current++
    const assistantMessage: Message = { id: assistantId, role: 'assistant', text: '' }
    setMessages((current) => [...current, userMessage, assistantMessage])
    setInput('')
    setSending(true)
    const controller = new AbortController()
    requestRef.current = controller
    try {
      const headers: Record<string, string> = { 'Content-Type': 'application/json', Accept: 'text/event-stream' }
      const observed = getObservedTenant()
      if (observed) headers['X-Warden-Tenant'] = observed
      const response = await fetch(`${API_BASE}/ai/chat`, {
        method: 'POST', headers, credentials: 'include', signal: controller.signal,
        body: JSON.stringify({ conversationId: conversationId || undefined, message: text }),
      })
      if (!response.ok) {
        const data = await response.json().catch(() => null) as { message?: string } | null
        if (response.status === 401) window.location.href = '/login'
        throw new Error(data?.message || `请求失败 (${response.status})`)
      }
      if (!response.body) throw new Error('浏览器未收到流式响应')
      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      let streamError = ''
      const processEvent = (block: string) => {
        let eventName = 'message'
        const data: string[] = []
        for (const line of block.split('\n')) {
          if (line.startsWith('event:')) eventName = line.slice(6).trim()
          if (line.startsWith('data:')) data.push(line.slice(5).trimStart())
        }
        if (!data.length) return
        const payload = JSON.parse(data.join('\n')) as { content?: string; conversationId?: string; message?: string }
        if (eventName === 'meta' && payload.conversationId) setConversationId(payload.conversationId)
        if (eventName === 'delta') {
          setMessages((current) => current.map((message) => message.id === assistantId
            ? { ...message, text: message.text + (payload.content || '') }
            : message))
        }
        if (eventName === 'error') streamError = payload.message || 'AI 请求失败'
      }
      while (true) {
        const { done, value } = await reader.read()
        buffer += decoder.decode(value, { stream: !done })
        const blocks = buffer.split(/\r?\n\r?\n/)
        buffer = blocks.pop() || ''
        blocks.forEach(processEvent)
        if (done) break
      }
      if (buffer.trim()) processEvent(buffer)
      if (streamError) throw new Error(streamError)
    } catch (error) {
      if (controller.signal.aborted) return
      const message = error instanceof Error ? error.message : 'AI 请求失败，请稍后重试'
      setMessages((current) => current.map((item) => item.id === assistantId ? { ...item, text: message } : item))
    } finally {
      if (requestRef.current === controller) requestRef.current = null
      setSending(false)
    }
  }

  const onInputKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault()
      submit()
    }
  }

  const reset = () => {
    requestRef.current?.abort()
    requestRef.current = null
    setSending(false)
    setConversationId('')
    setMessages([])
    setInput('')
    inputRef.current?.focus()
  }

  return (
    <>
      <button
        ref={launcherRef}
        className={`ai-assistant-launcher${open ? ' active' : ''}`}
        onClick={() => setOpen((current) => !current)}
        aria-label={open ? '收起 Warden AI 助手' : '打开 Warden AI 助手'}
        aria-expanded={open}
        title="Warden AI 助手"
      >
        <Bot size={21} />
        <span>AI</span>
        <i aria-hidden="true" />
      </button>
      {open && <button className="ai-assistant-mobile-scrim" aria-label="关闭 AI 助手" onClick={() => setOpen(false)} />}
      {open && (
        <aside className="ai-assistant-panel" aria-label="Warden AI 助手">
          <header className="ai-assistant-header">
            <div className="ai-assistant-title">
              <span className="ai-assistant-logo" aria-hidden="true">
                <img src={assistantAvatarUrl} alt="" />
              </span>
              <div>
                <h2>Warden AI</h2>
                <span>受控智能助手</span>
              </div>
            </div>
            <div className="ai-assistant-header-actions">
              <button onClick={reset} aria-label="开始新会话" title="新会话">
                <Plus size={17} />
              </button>
              <button
                onClick={() => {
                  setOpen(false)
                  launcherRef.current?.focus()
                }}
                aria-label="关闭 AI 助手"
                title="关闭"
              >
                <X size={18} />
              </button>
            </div>
          </header>

          <div className="ai-assistant-conversation" aria-live="polite">
            {messages.length === 0 ? (
              <div className="ai-assistant-welcome">
                <div className="ai-assistant-hero">
                  <span className="ai-assistant-avatar" aria-hidden="true">
                    <img src={assistantAvatarUrl} alt="" />
                  </span>
                  <div>
                    <span>你好，欢迎使用</span>
                    <h3>Warden AI 助手</h3>
                  </div>
                </div>
                <p className="ai-assistant-welcome-copy">我可以协助你分析当前环境</p>
                <div className="ai-capability-grid">
                  {capabilities.map(({ label, prompt, icon: Icon }) => (
                    <button key={label} onClick={() => choosePrompt(prompt)}>
                      <Icon size={18} />
                      <span>{label}</span>
                    </button>
                  ))}
                </div>
                <div className="ai-prompt-list">
                  {context.prompts.map((prompt) => (
                    <button key={prompt} onClick={() => choosePrompt(prompt)}>
                      <MessageSquareText size={13} />
                      <span>{prompt}</span>
                    </button>
                  ))}
                </div>
              </div>
            ) : (
              <div className="ai-message-list">
                {messages.map((message) => (
                  <div key={message.id} className={`ai-message ${message.role}`}>
                    {message.role === 'assistant' && (
                      <span className="ai-message-avatar" aria-hidden="true">
                        <img src={assistantAvatarUrl} alt="" />
                      </span>
                    )}
                    <p>{message.text || (sending ? '正在思考…' : '')}</p>
                  </div>
                ))}
                <div ref={messageEndRef} />
              </div>
            )}
          </div>

          <div className="ai-assistant-bottom">
            <div className="ai-context-chip">
              <span className="ai-context-pulse" aria-hidden="true" />
              <span>当前上下文</span>
              <strong>{context.title}</strong>
            </div>
            <form className="ai-assistant-composer" onSubmit={submit}>
              <label className="sr-only" htmlFor="warden-ai-input">
                向 Warden AI 提问
              </label>
              <textarea
                ref={inputRef}
                id="warden-ai-input"
                value={input}
                onChange={(event) => setInput(event.target.value)}
                onKeyDown={onInputKeyDown}
                placeholder={mode === 'agent' ? '描述你想完成的运维任务…' : '向 Warden AI 提问…'}
                rows={3}
                maxLength={2000}
                disabled={sending}
              />
              <div className="ai-composer-toolbar">
                <div className="ai-mode-switch" role="group" aria-label="助手模式">
                  <button type="button" className={mode === 'agent' ? 'selected' : ''} onClick={() => setMode('agent')}>
                    <Sparkles size={13} />
                    Agent
                  </button>
                  <button type="button" className={mode === 'chat' ? 'selected' : ''} onClick={() => setMode('chat')}>
                    <MessageSquareText size={13} />
                    Chat
                  </button>
                </div>
                <button className="ai-send-button" type="submit" disabled={!input.trim() || sending} aria-label="发送消息" title="发送">
                  <ArrowUp size={17} />
                </button>
              </div>
            </form>
            <p className="ai-assistant-disclaimer">AI 建议仅供参考，变更操作仍需经过 Warden 授权与审批</p>
          </div>
        </aside>
      )}
    </>
  )
}
