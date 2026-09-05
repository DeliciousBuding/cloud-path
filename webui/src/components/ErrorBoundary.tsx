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
    console.error('[cloudpath] render error', error, info.componentStack)
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children
    return (
      <div className="flex min-h-screen items-center justify-center px-6">
        <div className="card w-full max-w-md p-8 text-center fade-up">
          <div className="mx-auto flex h-14 w-14 items-center justify-center rounded-full bg-bad/10 text-bad">
            <AlertTriangle size={24} />
          </div>
          <h1 className="mt-5 text-[22px] font-semibold tracking-[-0.01em]">界面出现异常</h1>
          <p className="mt-1.5 text-sm text-ink-2">
            渲染过程中发生错误。数据与服务本身仍在运行，重新加载通常即可恢复。
          </p>
          <pre className="mt-4 max-h-32 overflow-auto rounded-lg bg-surface-2 p-3 text-left font-mono text-[11px] leading-relaxed text-ink-3">
            {error.message || String(error)}
          </pre>
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
