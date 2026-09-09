import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorNote } from './ErrorNote'
import { useRevokeToken } from '@/hooks/useAdmin'
import { adminErrorMessage } from '@/lib/admin'
import { toast } from '@/store/toast'
import type { TokenView } from '@/lib/types'

export function TokenActions({ token }: { token: TokenView }) {
  const { t } = useTranslation('admin')
  const revoke = useRevokeToken()
  const [confirming, setConfirming] = useState(false)
  const revoked = Boolean(token.revoked_at)

  const doRevoke = () => {
    revoke.mutate(token.id, {
      onSuccess: () => {
        toast.ok(t('tokenRow.toast.revokedTitle'), t('tokenRow.toast.revokedMessage', { name: token.name }))
        setConfirming(false)
      },
    })
  }

  if (revoked) return <span className="text-meta text-ink-3">—</span>

  return (
    <>
      <Button
        variant="danger-ghost"
        size="sm"
        className="whitespace-nowrap"
        onClick={() => setConfirming(true)}
        aria-label={t('tokenRow.actions.revokeAria', { name: token.name })}
      >
        {t('tokenRow.actions.revoke')}
      </Button>
      <ConfirmDialog
        open={confirming}
        title={t('tokenRow.confirmTitle', { name: token.name })}
        body={t('tokenRow.confirmBody')}
        confirmLabel={revoke.isPending ? t('tokenRow.actions.confirming') : t('tokenRow.actions.confirm')}
        busy={revoke.isPending}
        extra={revoke.isError ? <ErrorNote message={adminErrorMessage(revoke.error)} /> : undefined}
        onCancel={() => setConfirming(false)}
        onConfirm={doRevoke}
      />
    </>
  )
}
