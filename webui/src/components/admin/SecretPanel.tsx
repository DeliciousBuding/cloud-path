// 一次性令牌明文面板（docs/api.md §3.3）：明文只从父组件的 state 传进来，
// 关闭即被父组件清空 → DOM 里再也找不到；服务端只存哈希与短前缀，无法二次取回。
// 本组件刻意不做任何持久化：不写 localStorage/sessionStorage、不进 URL、不打 console、不进 toast 文本。
import { useEffect, useId, useState } from 'react'
import { Check, Copy, ShieldAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge, KeyValue } from '@/components/ui'
import { fmtDateTime } from '@/lib/format'
import type { CreatedToken } from '@/lib/types'

export function SecretPanel({ secret, onClose }: { secret: CreatedToken; onClose: () => void }) {
  const { t } = useTranslation('admin')
  const [copied, setCopied] = useState(false)
  const [copyFailed, setCopyFailed] = useState(false)
  const id = useId()
  const scopeLabel = (scope: string) => t(`secret.scopes.${scope}`, {
    defaultValue: t('secret.scopes.other'),
  })

  // Esc 关闭：模态语义的最低要求（焦点管理与真机行为见 README 的待验证项）
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => { if (ev.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(secret.token)
      setCopied(true)
      setCopyFailed(false)
    } catch {
      // 剪贴板不可用（非安全上下文/权限被拒）：明文仍在下方只读框内，可手动全选复制
      setCopied(false)
      setCopyFailed(true)
    }
  }

  return (
    <section
      role="dialog"
      aria-modal="true"
      aria-labelledby={`${id}-title`}
      aria-describedby={`${id}-warn`}
      className="card mb-4 border-bad/30 p-5 fade-up"
    >
      <h3 id={`${id}-title`} className="flex items-center gap-1.5 text-[15px] font-semibold tracking-[-0.01em] text-bad">
        <ShieldAlert size={15} className="shrink-0" />
        <span className="min-w-0 break-words">{t('secret.title')}</span>
      </h3>
      <p id={`${id}-warn`} className="mt-2 text-xs leading-relaxed text-ink-2 break-words">
        {t('secret.warning')}
      </p>

      <label htmlFor={`${id}-secret`} className="mt-4 mb-1.5 block text-[13px] font-medium text-ink-2">
        {t('secret.label')}
      </label>
      <div className="flex gap-2">
        {/* 只读输入框（不是文本节点）：可全选复制，也不会被当成正文重复朗读 */}
        <input
          id={`${id}-secret`}
          readOnly
          value={secret.token}
          spellCheck={false}
          autoComplete="off"
          aria-describedby={`${id}-status`}
          className="num min-w-0 flex-1 rounded-lg border border-hairline bg-surface-2 px-3 py-2 font-mono text-xs break-all outline-none focus:border-accent"
        />
        <button
          type="button"
          autoFocus
          onClick={() => void copy()}
          aria-label={t('secret.copyAria')}
          className="btn btn-primary shrink-0"
        >
          {copied ? <Check size={14} /> : <Copy size={14} />}{copied ? t('secret.copied') : t('secret.copy')}
        </button>
      </div>
      <p id={`${id}-status`} role="status" aria-live="polite" className="mt-2 text-xs leading-relaxed text-ink-3 break-words">
        {copied ? t('secret.status.copied') : copyFailed ? t('secret.status.failed') : t('secret.status.idle')}
      </p>

      <dl className="mt-4 space-y-2 border-t border-hairline pt-4">
        <KeyValue k={t('secret.fields.name')} v={secret.name || t('secret.none')} />
        <KeyValue k={t('secret.fields.prefix')} v={`${secret.prefix}…`} mono />
        <KeyValue k={t('secret.fields.scopes')} v={(secret.scopes ?? []).map(scopeLabel).join(t('secret.scopeSeparator')) || t('secret.none')} />
        <KeyValue k={t('secret.fields.expires')} v={secret.expires_at ? fmtDateTime(secret.expires_at) : t('secret.neverExpires')} />
      </dl>

      <div className="mt-4 flex flex-wrap items-center gap-2">
        <button type="button" className="btn btn-ghost" onClick={onClose} aria-label={t('secret.closeAria')}>
          {t('secret.close')}
        </button>
        <Badge tone="warn">{t('secret.badges.hidden')}</Badge>
        <Badge tone="idle">{t('secret.badges.keep')}</Badge>
      </div>
    </section>
  )
}
