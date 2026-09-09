import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { History, RefreshCw } from 'lucide-react'
import { Panel, Badge, ErrorState } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { CommandButton } from '@/components/CommandButton'
import { api } from '@/lib/api'
import { commandArgsError } from '@/lib/command-schema'
import { cmdMeta, cmdStatusMeta, fmtTime, fmtDateTime } from '@/lib/format'
import type { ReactNode } from 'react'
import type { CommandAction } from '@/lib/descriptor'

function failureInfo(result?: string): { message: string; next: string } {
  const text = result?.trim()
  if (!text) return { message: '设备没有完成操作', next: '请稍后重试' }
  if (/timeout|timed out|超时/i.test(text)) return { message: '设备响应超时', next: '确认设备在线且空闲后重试' }
  if (/busy|queue full|忙/i.test(text)) return { message: '设备正忙', next: '等待设备空闲后重试' }
  if (/offline|离线/i.test(text)) return { message: '设备当前离线', next: '确认设备恢复在线后重试' }
  if (/permission|forbidden|unauthorized|权限/i.test(text)) return { message: '当前账号没有操作权限', next: '请联系管理员授权后重试' }
  if (/unsupported|not supported|invalid|参数无效/i.test(text)) return { message: '设备不支持此操作或参数无效', next: '检查参数后重试' }
  if (/^[\u3400-\u9fff\s，。！？、；：（）\-—]+$/.test(text)) return { message: text, next: '请根据提示检查后重试' }
  return { message: '设备没有完成操作', next: '请稍后重试；如果持续失败，请查看技术详情' }
}

function isFailure(status: string): boolean {
  return status === 'failed' || status === 'timeout'
}

function isInProgress(status: string): boolean {
  return status === 'pending' || status === 'sent'
}

function progressCopy(status: string): string {
  return status === 'pending' ? '正在发送到设备' : '已发送，正在等待设备确认'
}

function controlPath(deviceId: string): string {
  const [edgeId = '', devId = ''] = deviceId.split('/')
  return `/devices/${encodeURIComponent(edgeId)}/${encodeURIComponent(devId)}?tab=controls`
}

/** 操作记录：REST 轮询该设备的操作与执行结果（含超时/失败原因）。
 *  普通用户只看人话结果；状态码、原始返回与参数只放在「技术详情」里。 */
export function CommandHistory({ deviceId, actions, limit, footer }: {
  deviceId: string; actions?: CommandAction[];
  /** 展示上限（概览首屏用：右栏不该拉到 20 行把左栏踢出空洞）；缺省全显 */
  limit?: number;
  /** 被截断时的出口（如「到控制页看全部」） */
  footer?: ReactNode
}) {
  const { data, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: ['device-commands', deviceId],
    queryFn: () => api.commands({ device: deviceId, limit: 20 }),
    refetchInterval: 5000,
  })
  const rows = data?.commands ?? []
  const shown = limit != null ? rows.slice(0, limit) : rows

  return (
    <Panel
      title={<span className="flex items-center gap-1.5"><History size={14} />操作记录</span>}
      right={!isLoading && !error ? (
        <span className="flex items-center gap-2 text-[12px] text-ink-3">
          <span className="num">{shown.length} 条</span>
          <button type="button" className="btn btn-ghost btn-sm h-7 px-2" disabled={isFetching}
            aria-label="刷新操作记录" onClick={() => { void refetch() }}>
            <RefreshCw size={12} className={isFetching ? 'animate-spin' : undefined} />
            刷新
          </button>
        </span>
      ) : undefined}
    >
      {isLoading ? (
        <RowSkeleton rows={3} />
      ) : error ? (
        <ErrorState compact title="加载失败"
          hint="暂时拿不到这台设备的操作记录，可能是网络或服务异常。"
          onRetry={() => { void refetch() }} retrying={isFetching} />
      ) : rows.length === 0 ? (
        <p className="py-4 text-center text-sm text-ink-3">这台设备还没有操作记录</p>
      ) : (
        <>
        <ul className="divide-y divide-hairline">
          {shown.map((c) => {
            const st = cmdStatusMeta(c.status)
            const statusLabel = st.label === c.status ? '状态未知' : st.label
            const meta = cmdMeta(c.cmd, actions)
            const action = actions?.find((a) => a.cmd === c.cmd)
            const failure = isFailure(c.status)
            const progress = isInProgress(c.status)
            const unknown = !failure && !progress && statusLabel === '状态未知'
            const info = failure ? failureInfo(c.result) : null
            const retryError = action ? commandArgsError(c.args ?? '', action.inputSchema, action.inputMaxLength) : undefined
            return (
              <li key={c.id} className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 py-3">
                <Badge tone={st.tone} className="shrink-0">{statusLabel}</Badge>
                <span className="min-w-0 truncate text-xs font-medium" title={meta.hint || meta.label}>
                  {meta.label}
                </span>
                <time className="num shrink-0 font-mono text-[11px] text-ink-3"
                  title={c.acked_at ? `执行结果 ${fmtDateTime(c.acked_at)}` : fmtDateTime(c.created_at)}>
                  {fmtTime(c.created_at)}
                </time>

                {progress && (
                  <p className="col-span-3 min-w-0 text-[12px] text-ink-3">{progressCopy(c.status)}，请勿重复操作。</p>
                )}

                {failure && info && (
                  <div className="col-span-3 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-2 rounded-lg bg-bad/8 px-2.5 py-2 text-[12px]">
                    <span className="min-w-0 flex-1 text-ink-2">
                      <span className="font-medium text-bad">{info.message}</span>
                      <span className="text-ink-3"> · {info.next}</span>
                    </span>
                    {action && !retryError ? (
                      <CommandButton deviceId={deviceId} action={action} args={c.args ?? ''}
                        buttonLabel="重试" buttonAriaLabel={`重试${meta.label}`} className="btn-sm shrink-0" />
                    ) : (
                      <Link to={controlPath(deviceId)} className="link shrink-0">去设备操作中重试</Link>
                    )}
                  </div>
                )}

                {unknown && (
                  <p className="col-span-3 min-w-0 text-[12px] text-ink-3">
                    暂时无法判断结果，请刷新后再看；如果持续异常，请查看技术详情。
                  </p>
                )}

                <details className="col-span-3 min-w-0 text-[12px]">
                  <summary className="cursor-pointer select-none text-ink-3">技术详情</summary>
                  <dl className="mt-1.5 grid gap-1 rounded-lg bg-ink-3/5 p-2 text-ink-2 sm:grid-cols-2">
                    <div className="min-w-0"><dt className="inline text-ink-3">状态码：</dt><dd className="inline break-all font-mono">{c.status}</dd></div>
                    <div className="min-w-0"><dt className="inline text-ink-3">记录编号：</dt><dd className="num inline font-mono">{c.id}</dd></div>
                    <div className="min-w-0 sm:col-span-2"><dt className="inline text-ink-3">参数：</dt><dd className="inline break-all font-mono">{c.args || '—'}</dd></div>
                    <div className="min-w-0 sm:col-span-2"><dt className="inline text-ink-3">原始结果：</dt><dd className="inline break-words font-mono">{c.result || '—'}</dd></div>
                  </dl>
                </details>
              </li>
            )
          })}
        </ul>
        {footer && rows.length > shown.length && <div className="mt-2 border-t border-hairline pt-2">{footer}</div>}
        </>
      )}
    </Panel>
  )
}
