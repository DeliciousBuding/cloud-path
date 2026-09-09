// 单个令牌行：只展示可读名称与元数据（服务端从不回明文），吊销走两步确认。
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge, Button, KeyValue } from '@/components/ui'
import type { Tone } from '@/components/ui'
import { ErrorNote } from './ErrorNote'
import { useRevokeToken } from '@/hooks/useAdmin'
import { adminErrorMessage } from '@/lib/admin'
import { fmtDateTime, timeAgo } from '@/lib/format'
import { toast } from '@/store/toast'
import type { TokenView } from '@/lib/types'

const SCOPE_TONES: Record<string, Tone> = {
  read: 'idle',
  write: 'ok',
  admin: 'warn',
  edge: 'warn',
}

export function TokenRow({ token: t }: { token: TokenView }) {
  const { t: translate } = useTranslation('admin')
  const revoke = useRevokeToken()
  const [confirming, setConfirming] = useState(false)
  const revoked = Boolean(t.revoked_at)
  const expired = !revoked && Boolean(t.expires_at) && (t.expires_at ?? 0) * 1000 <= Date.now()
  const tone: Tone = revoked ? 'bad' : expired ? 'warn' : 'ok'
  const stateLabel = translate(revoked ? 'tokenRow.states.revoked' : expired ? 'tokenRow.states.expired' : 'tokenRow.states.valid')
  const scopeLabel = (scope: string) => translate(`tokenRow.scopes.${scope}`, {
    defaultValue: translate('tokenRow.scopes.other'),
  })

  const doRevoke = () => {
    revoke.mutate(t.id, {
      onSuccess: () => {
        toast.ok(translate('tokenRow.toast.revokedTitle'), translate('tokenRow.toast.revokedMessage', { name: t.name }))
        setConfirming(false)
      },
    })
  }

  return (
    <li className="py-4 first:pt-0">
      <div className="flex min-w-0 items-start gap-2">
        <div className="min-w-0 flex-1">
          <p className="truncate text-body font-semibold" title={t.name}>{t.name || translate('tokenRow.unnamed')}</p>
        </div>
        <span className="shrink-0"><Badge tone={tone}>{stateLabel}</Badge></span>
      </div>

      <div className="mt-3">
        <p className="mb-1.5 text-micro font-medium text-ink-3">{translate('tokenRow.scopeLabel')}</p>
        {(t.scopes ?? []).length > 0 ? (
          <div className="flex flex-wrap gap-1.5">
            {(t.scopes ?? []).map((s) => (
              <Badge key={s} tone={SCOPE_TONES[s] ?? 'idle'}>
                {scopeLabel(s)}
              </Badge>
            ))}
          </div>
        ) : (
          <p className="text-meta text-ink-3">{translate('tokenRow.noScopes')}</p>
        )}
      </div>

      <dl className="mt-3 space-y-2">
        <KeyValue k={translate('tokenRow.fields.createdAt')} v={<span className="font-mono">{fmtDateTime(t.created_at)}</span>} />
        <KeyValue k={translate('tokenRow.fields.lastUsed')} v={t.last_used_at ? timeAgo(t.last_used_at) : translate('tokenRow.neverUsed')} />
        <KeyValue k={translate('tokenRow.fields.expires')} v={t.expires_at ? <span className="font-mono">{fmtDateTime(t.expires_at)}</span> : translate('tokenRow.neverExpires')} />
        {revoked && <KeyValue k={translate('tokenRow.fields.revokedAt')} v={<span className="font-mono">{fmtDateTime(t.revoked_at ?? 0)}</span>} />}
      </dl>

      <details className="mt-3 text-meta text-ink-2">
        <summary className="flex min-h-touch cursor-pointer items-center">{translate('tokenRow.details')}</summary>
        <dl className="mt-2 space-y-2">
          <KeyValue k={translate('tokenRow.technicalFields.id')} v={<span className="font-mono">{t.id}</span>} />
          <KeyValue k={translate('tokenRow.technicalFields.prefix')} v={t.prefix} mono />
          <KeyValue k={translate('tokenRow.technicalFields.codes')} v={(t.scopes ?? []).join(', ') || '—'} mono />
        </dl>
      </details>

      {!revoked && !confirming && (
        <div className="mt-3 border-t border-hairline pt-3">
          <Button variant="ghost" onClick={() => setConfirming(true)}
            aria-label={translate('tokenRow.actions.revokeAria', { name: t.name })}>
            {translate('tokenRow.actions.revoke')}
          </Button>
        </div>
      )}

      {!revoked && confirming && (
        <div className="mt-3 space-y-3 border-t border-hairline pt-3">
          <p className="text-meta leading-relaxed text-warn break-words">
            {translate('tokenRow.confirm', { name: t.name })}
          </p>
          {revoke.isError && <ErrorNote message={adminErrorMessage(revoke.error)} />}
          <div className="flex flex-wrap gap-2">
            <button type="button" disabled={revoke.isPending} onClick={doRevoke}
              className="btn btn-danger" aria-label={translate('tokenRow.actions.confirmAria', { name: t.name })}>
              {revoke.isPending ? translate('tokenRow.actions.confirming') : translate('tokenRow.actions.confirm')}
            </button>
            <Button type="button" variant="ghost" onClick={() => setConfirming(false)}
              aria-label={translate('tokenRow.actions.cancelAria', { name: t.name })}>
              {translate('actions.cancel')}
            </Button>
          </div>
        </div>
      )}

      {revoked && (
        <p className="mt-3 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3 break-words">
          {translate('tokenRow.revokedHint')}
        </p>
      )}
    </li>
  )
}
