import { X, CheckCircle2, XCircle, Info } from 'lucide-react'
import { useToasts, type ToastTone } from '@/store/toast'
import { cn } from '@/lib/cn'

const TONE_ICON: Record<ToastTone, { icon: typeof Info; cls: string }> = {
  ok: { icon: CheckCircle2, cls: 'text-ok' },
  bad: { icon: XCircle, cls: 'text-bad' },
  info: { icon: Info, cls: 'text-accent' },
}

export function ToastViewport() {
  const items = useToasts((s) => s.items)
  const dismiss = useToasts((s) => s.dismiss)
  return (
    <div className="pointer-events-none fixed bottom-6 right-6 z-50 flex w-80 flex-col gap-2">
      {items.map((t) => {
        const meta = TONE_ICON[t.tone]
        const Icon = meta.icon
        return (
          <button
            key={t.id}
            type="button"
            onClick={() => dismiss(t.id)}
            className="glass pointer-events-auto flex items-start gap-3 rounded-2xl border border-hairline px-4 py-3 text-left shadow-lift fade-up"
          >
            <Icon size={17} className={cn('mt-0.5 shrink-0', meta.cls)} strokeWidth={2} />
            <span className="min-w-0 flex-1">
              <span className="block text-[13px] font-semibold leading-snug">{t.title}</span>
              {t.detail && <span className="mt-0.5 block truncate text-xs text-ink-2">{t.detail}</span>}
            </span>
            <X size={14} className="mt-0.5 shrink-0 text-ink-3" />
          </button>
        )
      })}
    </div>
  )
}