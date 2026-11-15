import type { LucideIcon } from 'lucide-react'
import { AlertTriangle, Loader2 } from 'lucide-react'

export function LoadingState({ label = '加载中' }: { label?: string }) {
  return (
    <div className="empty-state">
      <div>
        <Loader2 size={26} className="spin" />
      </div>
      <h2>{label}</h2>
      <p>正在从 pam-control 拉取数据</p>
    </div>
  )
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="empty-state">
      <div>
        <AlertTriangle size={26} />
      </div>
      <h2>加载失败</h2>
      <p>{message}</p>
      {onRetry && (
        <button className="primary-button" onClick={onRetry} style={{ marginTop: 14 }}>
          重试
        </button>
      )}
    </div>
  )
}

export function EmptyState({ icon: Icon, title, text }: { icon: LucideIcon; title: string; text: string }) {
  return (
    <div className="empty-state">
      <div>
        <Icon size={28} />
      </div>
      <h2>{title}</h2>
      <p>{text}</p>
    </div>
  )
}
