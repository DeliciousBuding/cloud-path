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
    toast.ok('令牌已创建', `${created.name || '（未命名）'} · 明文只显示一次`)
  }

  return (
    <Panel
      title={<span className="flex items-center gap-1.5"><KeyRound size={14} />服务令牌</span>}
      right={(
        <Button
          variant={creating ? 'ghost' : 'primary'}
          aria-expanded={creating}
          onClick={() => setCreating((v) => !v)}
        >
          {!creating && <Plus size={14} />}{creating ? '收起表单' : '新建令牌'}
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
        <p className="py-6 text-center text-sm text-ink-3">本租户还没有服务令牌</p>
      ) : (
        <ul className="divide-y divide-hairline" aria-label="服务令牌列表">
          {tokens.map((t) => <TokenRow key={t.id} token={t} />)}
        </ul>
      )}

      <p className="mt-4 border-t border-hairline pt-3 text-[12px] leading-relaxed text-ink-3 break-words">
        令牌格式为 cp_ 前缀 + 至少 32 字节随机数据；服务端只保存 SHA-256 哈希与短前缀，
        因此列表永远看不到明文，忘记保存只能吊销后重建。旧的 CLOUDPATH_TOKEN 仍等价 default 租户 admin，但已标记为 legacy。
      </p>
    </Panel>
  )
}