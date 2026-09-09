// 单个令牌行：只展示可读名称与元数据（服务端从不回明文），吊销走两步确认。
import { useState } from 'react'
import { Badge, Button, KeyValue } from '@/components/ui'
import type { Tone } from '@/components/ui'
import { ErrorNote } from './ErrorNote'
import { useRevokeToken } from '@/hooks/useAdmin'
import { adminErrorMessage } from '@/lib/admin'
import { fmtDateTime, timeAgo } from '@/lib/format'
import { toast } from '@/store/toast'
import type { TokenView } from '@/lib/types'

const SCOPE_LABELS: Record<string, string> = {
  read: '查看设备与记录',
  write: '执行设备操作',
  admin: '管理成员与令牌',
  edge: '网关接入',
}

const SCOPE_TONES: Record<string, Tone> = {
  read: 'idle',
  write: 'ok',
  admin: 'warn',
  edge: 'warn',
}

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
    <li className="py-4 first:pt-0">
      <div className="flex min-w-0 items-start gap-2">
        <div className="min-w-0 flex-1">
          <p className="truncate text-body font-semibold" title={t.name}>{t.name || '（未命名）'}</p>
        </div>
        <span className="shrink-0"><Badge tone={tone}>{stateLabel}</Badge></span>
      </div>

      <div className="mt-3">
        <p className="mb-1.5 text-micro font-medium text-ink-3">权限范围</p>
        {(t.scopes ?? []).length > 0 ? (
          <div className="flex flex-wrap gap-1.5">
            {(t.scopes ?? []).map((s) => (
              <Badge key={s} tone={SCOPE_TONES[s] ?? 'idle'}>
                {SCOPE_LABELS[s] ?? '其他权限'}
              </Badge>
            ))}
          </div>
        ) : (
          <p className="text-meta text-ink-3">未设置权限范围</p>
        )}
      </div>

      <dl className="mt-3 space-y-2">
        <KeyValue k="创建时间" v={<span className="font-mono">{fmtDateTime(t.created_at)}</span>} />
        <KeyValue k="最近使用" v={t.last_used_at ? timeAgo(t.last_used_at) : '从未使用'} />
        <KeyValue k="有效期" v={t.expires_at ? <span className="font-mono">{fmtDateTime(t.expires_at)}</span> : '不自动失效'} />
        {revoked && <KeyValue k="吊销于" v={<span className="font-mono">{fmtDateTime(t.revoked_at ?? 0)}</span>} />}
      </dl>

      <details className="mt-3 text-meta text-ink-2">
        <summary className="flex min-h-touch cursor-pointer items-center">技术详情</summary>
        <dl className="mt-2 space-y-2">
          <KeyValue k="令牌 ID" v={<span className="font-mono">{t.id}</span>} />
          <KeyValue k="识别前缀" v={t.prefix} mono />
          <KeyValue k="权限代码" v={(t.scopes ?? []).join(', ') || '—'} mono />
        </dl>
      </details>

      {!revoked && !confirming && (
        <div className="mt-3 border-t border-hairline pt-3">
          <Button variant="ghost" onClick={() => setConfirming(true)} aria-label={`吊销令牌 ${t.name}`}>吊销</Button>
        </div>
      )}

      {!revoked && confirming && (
        <div className="mt-3 space-y-3 border-t border-hairline pt-3">
          <p className="text-meta leading-relaxed text-warn break-words">
            确认吊销「{t.name}」？该令牌会立即失效且无法恢复，正在使用它的网关或自动化工具将无法继续访问。
          </p>
          {revoke.isError && <ErrorNote message={adminErrorMessage(revoke.error)} />}
          <div className="flex flex-wrap gap-2">
            <button type="button" disabled={revoke.isPending} onClick={doRevoke}
              className="btn btn-danger" aria-label={`确认吊销令牌 ${t.name}`}>
              {revoke.isPending ? '吊销中…' : '确认吊销'}
            </button>
            <Button type="button" variant="ghost" onClick={() => setConfirming(false)}
              aria-label={`取消吊销 ${t.name}`}>取消</Button>
          </div>
        </div>
      )}

      {revoked && (
        <p className="mt-3 border-t border-hairline pt-3 text-meta leading-relaxed text-ink-3 break-words">
          已吊销的令牌会保留基本信息，方便以后核对；不能恢复，需要时请新建一个。
        </p>
      )}
    </li>
  )
}
