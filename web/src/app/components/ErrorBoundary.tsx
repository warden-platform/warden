import { Component, type ErrorInfo, type ReactNode } from 'react'
import { AlertTriangle, RefreshCw } from 'lucide-react'

type Props = { children: ReactNode }
type State = { error: Error | null }

// Lazy route chunks fail to load after a deploy invalidates the old bundle
// (the browser still references hashed filenames that no longer exist). The
// only reliable recovery is a full reload, which fetches the new index.html.
function isChunkLoadError(error: Error): boolean {
  return /dynamically imported module|Importing a module script failed|Loading chunk|ChunkLoadError/i.test(`${error.name} ${error.message}`)
}

// Keeps a crashed page from taking down the whole console. Mount it with a
// location-derived key so navigating to another route resets the boundary.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('[warden] error boundary caught:', error, info.componentStack)
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children
    const chunkFailed = isChunkLoadError(error)
    return (
      <div className="empty-state" role="alert">
        <div>
          <AlertTriangle size={26} />
        </div>
        <h2>{chunkFailed ? '页面模块加载失败' : '页面渲染出现异常'}</h2>
        <p>
          {chunkFailed
            ? '可能刚发布了新版本，当前会话引用的旧模块已失效，刷新后即可加载新版本。'
            : '当前页面遇到未预期的错误，其他页面仍可正常使用，可尝试重试或刷新。'}
        </p>
        <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
          {!chunkFailed && (
            <button className="quiet-button" onClick={() => this.setState({ error: null })}>
              重试
            </button>
          )}
          <button className="primary-button" onClick={() => window.location.reload()}>
            <RefreshCw size={16} />
            刷新页面
          </button>
        </div>
      </div>
    )
  }
}
