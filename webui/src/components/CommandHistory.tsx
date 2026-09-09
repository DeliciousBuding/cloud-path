import { useQuery } from '@tanstack/react-query'
import { History } from 'lucide-react'
import { Panel, Badge, ErrorState } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { api } from '@/lib/api'
import { cmdMeta, cmdStatusMeta, fmtTime, fmtDateTime } from '@/lib/format'
import type { ReactNode } from 'react'
import type { CommandAction } from '@/lib/descriptor'

function failureCopy(result?: string): string {
  const text = result?.trim()
  if (!text) return '操作失败，请稍后重试'
  if (/timeout|timed out|超时/i.test(text)) return '设备响应超时，请重试'
  if (/busy|queue full|忙/i.test(text)) return '设备正忙，请稍后重试'
  if (/offline|离线/i.test(text)) return '设备离线，操作未完成'
  if (/unsupported|not supported|invalid|参数无效/i.test(text)) return '设备不支持此操作，或参数无效'
  if (/^[\u3400-\u9fff\s，。！？、；：（）\-—]+$/.test(text)) return text
  return '设备返回失败，请稍后重试'
}

/** 操作记录：REST 轮询该设备的操作与执行结果（含超时/失败原因）。
 *  操作展示名来自上层传入的声明命令集（actions），未声明则 humanize(cmd)。 */
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
      right={!isLoading && !error ? <span className="text-[12px] text-ink-3">{shown.length} 条</span> : undefined}
    >
      {isLoading ? (
        <RowSkeleton rows={3} />
      ) : error ? (
        <ErrorState compact title="加载失败"
          hint="暂时拿不到这台设备的操作记录，可能是网络或服务异常。"
          onRetry={() => { void refetch() }} retrying={isFetching} />
      ) : rows.length === 0 ? (
        <p className="py-4 text-center text-sm text-ink-3">还没有执行过操作</p>
      ) : (
        <>
        <ul className="divide-y divide-hairline">
          {shown.map((c) => {
            const st = cmdStatusMeta(c.status)
            const statusLabel = st.label === c.status ? '状态未知' : st.label
            const meta = cmdMeta(c.cmd, actions)
            return (
              // 390px：可换行 + 各段 truncate（nowrap 行的 min-content 会把外层网格轨道撑宽）
              <li key={c.id} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 py-2">
                <Badge tone={st.tone} className="shrink-0">{statusLabel}</Badge>
                <span className="min-w-0 truncate text-xs font-medium"
                  title={meta.hint || meta.label}>
                  {meta.label}
                </span>
                {/* 回执原文是机器噪音：失败原文收进 title，行内只给人话；成功结果可展开查看 */}
                {st.tone !== 'ok' && (
                  <span className="min-w-0 truncate text-[12px] text-bad" title={c.result}>
                    {failureCopy(c.result)}
                  </span>
                )}
                <span className="num ml-auto shrink-0 font-mono text-[11px] text-ink-3"
                  title={c.acked_at ? `执行结果 ${fmtDateTime(c.acked_at)}` : fmtDateTime(c.created_at)}>
                  {fmtTime(c.created_at)}
                </span>
                {c.result && st.tone === 'ok' && (
                  <details className="basis-full min-w-0 pl-0 text-[12px]">
                    <summary className="cursor-pointer select-none text-ink-3">查看结果</summary>
                    <p className="mt-1 break-words text-ink-2">{c.result}</p>
                  </details>
                )}
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
