// 服务令牌面板（docs/api.md §3.3）：列表（只有 prefix/元数据）+ 创建 + 吊销。
//
// 明文的唯一落点是下面的 `secret` 组件 state：
//   - 创建响应到达 → setSecret(created) → SecretPanel 一次性展示
//   - 关闭面板 → setSecret(null) → DOM 里再无任何明文
//   - 组件卸载（切页/登出）→ state 随之消失
// 刻意不走 useMutation：mutationCache 会保留结果对象，超出「组件内存」的范围。
// 也刻意不写 localStorage/sessionStorage/URL/console/toast —— admin-tokens 测试对此做反向断言。
import { useState } from 'react'
import { KeyRound, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'
import { Button, Panel } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { CreateTokenForm } from './CreateTokenForm'
import { ErrorNote } from './ErrorNote'
import { SecretPanel } from './SecretPanel'
import { TokenRow } from './TokenRow'
import { ADMIN_TOKENS_KEY, useAdminTokens } from '@/hooks/useAdmin'
import { adminErrorMessage } from '@/lib/admin'
import { toast } from '@/store/toast'
import type { CreatedToken } from '@/lib/types'

export function TokenManager() {
  const { t } = useTranslation('admin')
  const qc = useQueryClient()
  const { data, isPending, isError, error, refetch } = useAdminTokens()
  const tokens = data?.tokens ?? []
  const [creating, setCreating] = useState(false)
  const [secret, setSecret] = useState<CreatedToken | null>(null)

  const onCreated = (created: CreatedToken) => {
    setSecret(created)
    setCreating(false)
    void qc.invalidateQueries({ queryKey: ADMIN_TOKENS_KEY })
    // toast 只带名称：明文绝不进提示文本（提示会挂在 DOM 上好几秒，也会被截图）
    toast.ok(t('tokenManager.toast.createdTitle'), t('tokenManager.toast.createdMessage', {
      name: created.name || t('tokenManager.unnamed'),
    }))
  }

  return (
    <Panel
      title={<span className="flex items-center gap-1.5"><KeyRound size={14} />{t('tokenManager.title')}</span>}
      right={(
        <Button
          variant={creating ? 'ghost' : 'primary'}
          aria-expanded={creating}
          onClick={() => setCreating((v) => !v)}
        >
          {!creating && <Plus size={14} />}{creating ? t('tokenManager.collapse') : t('tokenManager.create')}
        </Button>
      )}
    >
      {secret && <SecretPanel secret={secret} onClose={() => setSecret(null)} />}

      {creating && (
        <CreateTokenForm onCreated={onCreated} onCancel={() => setCreating(false)} />
      )}

      {isError ? (
        <ErrorNote message={adminErrorMessage(error)} onRetry={() => void refetch()} />
      ) : isPending ? (
        <RowSkeleton rows={2} />
      ) : tokens.length === 0 ? (
        <p className="py-6 text-center text-body text-ink-3">{t('tokenManager.empty')}</p>
      ) : (
        <ul className="divide-y divide-hairline" aria-label={t('tokenManager.listAria')}>
          {tokens.map((tok) => <TokenRow key={tok.id} token={tok} />)}
        </ul>
      )}

      <div className="mt-4 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3">
        <p>{t('tokenManager.hint')}</p>
        <details className="mt-1.5">
          <summary className="flex min-h-touch cursor-pointer items-center">{t('tokenManager.details')}</summary>
          <p className="mt-1 break-words">{t('tokenManager.detailsHint')}</p>
        </details>
      </div>
    </Panel>
  )
}
