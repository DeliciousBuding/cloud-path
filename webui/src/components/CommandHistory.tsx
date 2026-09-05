import { useQuery } from '@tanstack/react-query'
import { History } from 'lucide-react'
import { Panel, Badge } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { api } from '@/lib/api'
import { cmdMeta, cmdStatusMeta, fmtTime, fmtDateTime } from '@/lib/format'
import type { ReactNode } from 'react'
import type { CommandAction } from '@/lib/descriptor'

/** 命令历史：REST 轮询该设备的命令与回执状态（含超时/失败原因）。
 *  命令展示名来自上层传入的声明命令集（actions），未声明则 humanize(cmd)。 */
export function CommandHistory({ deviceId, actions, limit, footer }: {
  deviceId: string; actions?: CommandAction[];
  /** 展示上限（概览首屏用：右栏不该拉到 20 行把左栏踢出空洞）；缺省全显 */
  limit?: number;
  /** 被截断时的出口（如「到控制页看全部」） */
  footer?: ReactNode
}) {
  const { data, isLoading } = useQuery({
    queryKey: ['device-commands', deviceId],
    queryFn: () => api.commands({ device: deviceId, limit: 20 }),
    refetchInterval: 5000,
  })
  const rows = data?.commands ?? []
  const shown = limit ? rows.slice(0, limit) : rows

  return (
    <Panel
      title={<span className="flex items-center gap-1.5"><History size={14} />命令历史</span>}
      right={<span className="text-[11px] text-ink-3">最近 {rows.length} 条</span>}
    >
      {isLoading ? (
        <RowSkeleton rows={3} />
      ) : rows.length === 0 ? (
        <p className="py-4 text-center text-sm text-ink-3">还没有下发过命令</p>
      ) : (
        <>
        <ul className="divide-y divide-hairline">
          {shown.map((c) => {
            const st = cmdStatusMeta(c.status)
            const meta = cmdMeta(c.cmd, actions)
            return (
              // 390px：可换行 + 各段 truncate（nowrap 行的 min-content 会把外层网格轨道撑宽）
              <li key={c.id} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 py-2">
                <Badge tone={st.tone} className="shrink-0">{st.label}</Badge>
                <span className="min-w-0 truncate text-xs font-medium"
                  title={`${meta.hint || c.cmd}${c.args ? ` · args: ${c.args}` : ''}${c.result && st.tone === 'ok' ? ` · 回执: ${c.result}` : ''}`}>
                  {meta.label}
                </span>
                {/* 回执原文是机器噪音：成功收进 title；失败原因才是人话信息，行内红字呈现 */}
                {c.result && st.tone !== 'ok' && (
                  <span className="min-w-0 truncate text-[11px] text-bad" title={c.result}>
                    {c.result}
                  </span>
                )}
                <span className="num ml-auto shrink-0 text-[11px] text-ink-3"
                  title={c.acked_at ? `回执 ${fmtDateTime(c.acked_at)}` : fmtDateTime(c.created_at)}>
                  {fmtTime(c.created_at)}
                </span>
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
