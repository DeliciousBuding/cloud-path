// 一次性令牌明文面板（docs/api.md §3.3）：明文只从父组件的 state 传进来，
// 关闭即被父组件清空 → DOM 里再也找不到；服务端只存哈希与短前缀，无法二次取回。
// 本组件刻意不做任何持久化：不写 localStorage/sessionStorage、不进 URL、不打 console、不进 toast 文本。
import { useId, useState } from 'react'
import { Check, Copy, ShieldAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge, Button, Input, KeyValue } from '@/components/ui'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
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
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="border-bad/30" showClose={false}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-1.5 text-bad">
            <ShieldAlert size={15} className="shrink-0" />
            <span className="min-w-0 break-words">{t('secret.title')}</span>
          </DialogTitle>
          <DialogDescription className="break-words">{t('secret.warning')}</DialogDescription>
        </DialogHeader>

        <label htmlFor={`${id}-secret`} className="mt-4 mb-1.5 block text-compact font-medium text-ink-2">
          {t('secret.label')}
        </label>
        <div className="flex gap-2">
          {/* 只读输入框（不是文本节点）：可全选复制，也不会被当成正文重复朗读 */}
          <Input
            id={`${id}-secret`}
            readOnly
            value={secret.token}
            spellCheck={false}
            autoComplete="off"
            aria-describedby={`${id}-status`}
            compact
            className="num min-w-0 flex-1 font-mono break-all"
          />
          <Button
            autoFocus
            onClick={() => void copy()}
            aria-label={t('secret.copyAria')}
            className="shrink-0"
          >
            {copied ? <Check size={14} /> : <Copy size={14} />}{copied ? t('secret.copied') : t('secret.copy')}
          </Button>
        </div>
        <p id={`${id}-status`} role="status" aria-live="polite" className="mt-2 text-meta leading-relaxed text-ink-3 break-words">
          {copied ? t('secret.status.copied') : copyFailed ? t('secret.status.failed') : t('secret.status.idle')}
        </p>

        <dl className="mt-4 space-y-2 border-t border-hairline pt-4">
          <KeyValue k={t('secret.fields.name')} v={secret.name || t('secret.none')} />
          <KeyValue k={t('secret.fields.prefix')} v={`${secret.prefix}…`} mono />
          <KeyValue k={t('secret.fields.scopes')} v={(secret.scopes ?? []).map(scopeLabel).join(t('secret.scopeSeparator')) || t('secret.none')} />
          <KeyValue k={t('secret.fields.expires')} v={secret.expires_at ? fmtDateTime(secret.expires_at) : t('secret.neverExpires')} />
        </dl>

        <div className="mt-4 flex flex-wrap items-center gap-2">
          <Button variant="ghost" onClick={onClose} aria-label={t('secret.closeAria')}>
            {t('secret.close')}
          </Button>
          <Badge tone="warn">{t('secret.badges.hidden')}</Badge>
          <Badge tone="idle">{t('secret.badges.keep')}</Badge>
        </div>
      </DialogContent>
    </Dialog>
  )
}
