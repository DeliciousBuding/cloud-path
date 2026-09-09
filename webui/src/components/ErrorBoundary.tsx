import { Component, type ErrorInfo, type ReactNode } from 'react'
import { AlertTriangle, RefreshCw } from 'lucide-react'

interface Props { children: ReactNode }
interface State { error: Error | null }

/**
 * 全局错误边界：任何渲染期异常都收敛成一张可读的卡片，而不是白屏。
 * 数据层错误（fetch 失败）由各页面的 Query 错误态处理，这里只兜渲染崩溃。
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // 生产环境无采集端；打到控制台便于本地排查
    console.error('[CloudPath] render error', error, info.componentStack)
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children
    return (
      <div className="flex min-h-screen items-center justify-center px-6">
        <div className="card w-full max-w-md p-8 text-center">
          <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-pill bg-bad/10 text-bad">
            <AlertTriangle size={22} />
          </div>
          <h1 className="mt-5 text-section font-semibold tracking-[-0.01em]">界面出现异常</h1>
          <p className="mt-1.5 text-body text-ink-2">
            页面暂时没有加载出来。重新加载后再试一次；如果仍然失败，再尝试继续。
          </p>
          <div className="mt-5 flex justify-center gap-2">
            <button type="button" className="btn btn-primary" onClick={() => location.reload()}>
              <RefreshCw size={14} /> 重新加载
            </button>
            <button type="button" className="btn btn-ghost" onClick={() => this.setState({ error: null })}>
              尝试继续
            </button>
          </div>
        </div>
      </div>
    )
  }
}
