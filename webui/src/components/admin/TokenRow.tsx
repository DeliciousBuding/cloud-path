// 单个令牌行：只展示 prefix 与元数据（服务端从不回明文），吊销走两步确认。
import { useState } from 'react'
import { Badge, Button, KeyValue } from '@/components/ui'
import type { Tone } from '@/components/ui'
import { ErrorNote } from './ErrorNote'
import { useRevokeToken } from '@/hooks/useAdmin'
import { adminErrorMessage } from '@/lib/admin'
import { fmtDateTime, timeAgo } from '@/lib/format'
import { toast } from '@/store/toast'
import type { TokenView } from '@/lib/types'

export function TokenRow({ token: t }: { token: TokenView }) {
  const revoke = useRevokeToken()
  const [confirming, setConfirming] = useState(false)
  const revoked = Boolean(t.revoked_at)
  const expired = !revoked && Boolean(t.expires_at) && (t.expires_at ?? 0) * 1000 <= Date.now()
  const tone: Tone = revoked ? 'bad' : expired ? 'warn' : 'ok'
  const stateLabel = revoked ? '已吊销' : expired ? '已过期' : '有效'

  const doRevoke = () => {
    revoke.mutate(t.id, {
      onSuccess: () => { toast.ok('令牌已吊销', `${t.name} 立即失效`); setConfirming(false) },
    })
  }

  return (
    <li className="rounded-2xl border border-hairline bg-surface-2 p-4">
      <div className="flex min-w-0 items-start gap-2">
        {/* 名称/前缀由管理员与服务端给定，长度不可控：必须各自截断 */}
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-semibold" title={t.name}>{t.name || '（未命名）'}</p>
          <p className="num mt-0.5 truncate font-mono text-xs text-ink-2" title={t.prefix}>{t.prefix}</p>
        </div>
        <span className="shrink-0"><Badge tone={tone}>{stateLabel}</Badge></span>
      </div>

      <div className="mt-3 flex flex-wrap gap-1.5">
        {(t.scopes ?? []).map((s) => (
          <Badge key={s} tone={s === 'admin' || s === 'edge' ? 'warn' : 'idle'}>{s}</Badge>
        ))}
      </div>

      <dl className="mt-3 space-y-2">
        <KeyValue k="创建于" v={fmtDateTime(t.created_at)} />
        <KeyValue k="最后使用" v={t.last_used_at ? timeAgo(t.last_used_at) : '从未使用'} />
        <KeyValue k="过期时间" v={t.expires_at ? fmtDateTime(t.expires_at) : '永不过期'} />
        {revoked && <KeyValue k="吊销于" v={fmtDateTime(t.revoked_at ?? 0)} />}
      </dl>

      {!revoked && !confirming && (
        <div className="mt-3 border-t border-hairline pt-3">
          <Button variant="ghost" onClick={() => setConfirming(true)} aria-label={`吊销令牌 ${t.name}`}>吊销</Button>
        </div>
      )}

      {!revoked && confirming && (
        <div className="mt-3 space-y-3 border-t border-hairline pt-3">
          <p className="text-xs leading-relaxed text-warn break-words">
            确认吊销「{t.name}」？该令牌会立即失效且无法恢复，正在使用它的边缘代理或脚本会开始收到 401。
          </p>
          {revoke.isError && <ErrorNote message={adminErrorMessage(revoke.error)} />}
          <div className="flex flex-wrap gap-2">
            <Button type="button" disabled={revoke.isPending} onClick={doRevoke}
              aria-label={`确认吊销令牌 ${t.name}`}>
              {revoke.isPending ? '吊销中…' : '确认吊销'}
            </Button>
            <Button type="button" variant="ghost" onClick={() => setConfirming(false)}
              aria-label={`取消吊销 ${t.name}`}>取消</Button>
          </div>
        </div>
      )}

      {revoked && (
        <p className="mt-3 border-t border-hairline pt-3 text-[11px] leading-relaxed text-ink-3 break-words">
          已吊销的令牌保留元数据用于审计，不能恢复；需要时请新建一个。
        </p>
      )}
    </li>
  )
}