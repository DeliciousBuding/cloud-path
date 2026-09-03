import { useQuery } from '@tanstack/react-query'
import { History } from 'lucide-react'
import { Panel, Badge } from '@/components/ui'
import { RowSkeleton } from '@/components/Skeleton'
import { api } from '@/lib/api'
import { cmdMeta, cmdStatusMeta, fmtTime, fmtDateTime } from '@/lib/format'

/** 命令历史：REST 轮询该设备的命令与回执状态（含超时/失败原因） */
export function CommandHistory({ deviceId }: { deviceId: string }) {
  const { data, isLoading } = useQuery({
    queryKey: ['device-commands', deviceId],
    queryFn: () => api.commands({ device: deviceId, limit: 20 }),
    refetchInterval: 5000,
  })
  const rows = data?.commands ?? []

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
        <ul className="divide-y divide-hairline">
          {rows.map((c) => {
            const st = cmdStatusMeta(c.status)
            const meta = cmdMeta(c.cmd)
            return (
              <li key={c.id} className="flex items-center gap-3 py-2.5">
                <Badge tone={st.tone}>{st.label}</Badge>
                <span className="text-xs font-medium" title={meta.hint || c.cmd}>{meta.label}</span>
                {c.args && (
                  <span className="num truncate font-mono text-[11px] text-ink-3" title={`args: ${c.args}`}>
                    {c.args}
                  </span>
                )}
                {c.result && (
                  <span className="truncate text-[11px] text-bad" title={c.result}>{c.result}</span>
                )}
                <span className="num ml-auto shrink-0 text-[11px] text-ink-3"
                  title={c.acked_at ? `回执 ${fmtDateTime(c.acked_at)}` : fmtDateTime(c.created_at)}>
                  {fmtTime(c.created_at)}
                </span>
              </li>
            )
          })}
        </ul>
      )}
    </Panel>
  )
}
