import { X, CheckCircle2, XCircle, Info } from 'lucide-react'
import { useToasts, type ToastTone } from '@/store/toast'
import { cn } from '@/lib/cn'

const TONE_ICON: Record<ToastTone, { icon: typeof Info; cls: string }> = {
  ok: { icon: CheckCircle2, cls: 'text-ok' },
  bad: { icon: XCircle, cls: 'text-bad' },
  info: { icon: Info, cls: 'text-accent' },
}

/**
 * 轻提示视口（命令回执 / 错误反馈）。
 * 无障碍：整块是 role=status + aria-live=polite 的礼貌播报区；关闭是独立按钮并有可读名称
 * （旧实现把整张卡片做成一个按钮，读屏会把提示正文当成按钮名，且容易误触关闭）。
 * 390px：宽度上限跟随视口（max-w-[calc(100vw-3rem)]），不产生横向溢出。
 */
export function ToastViewport() {
  const items = useToasts((s) => s.items)
  const dismiss = useToasts((s) => s.dismiss)
  return (
    <div
      role="status"
      aria-live="polite"
      aria-label="通知"
      className="pointer-events-none fixed bottom-6 right-6 z-50 flex w-80 max-w-[calc(100vw-3rem)] flex-col gap-2"
    >
      {items.map((t) => {
        const meta = TONE_ICON[t.tone]
        const Icon = meta.icon
        return (
          <div
            key={t.id}
            className="glass pointer-events-auto flex items-start gap-3 rounded-2xl border border-hairline px-4 py-3 text-left shadow-lift fade-up"
          >
            <Icon size={17} className={cn('mt-0.5 shrink-0', meta.cls)} strokeWidth={2} aria-hidden />
            <span className="min-w-0 flex-1">
              <span className="block text-[13px] font-semibold leading-snug">{t.title}</span>
              {t.detail && <span className="mt-0.5 block truncate text-xs text-ink-2">{t.detail}</span>}
            </span>
            <button
              type="button"
              onClick={() => dismiss(t.id)}
              aria-label={`关闭提示：${t.title}`}
              className="-mr-1 shrink-0 rounded-full p-1 text-ink-3 transition-colors hover:text-ink"
            >
              <X size={14} aria-hidden />
            </button>
          </div>
        )
      })}
    </div>
  )
}