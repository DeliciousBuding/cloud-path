import { Link } from 'react-router'
import type { EventView } from '@/lib/types'
import { Badge } from './ui'
import { useCapabilityIndex } from '@/hooks/useDescriptor'
import { eventLabel, eventTone, fmtDateTime, fmtTime, payloadLabel } from '@/lib/format'

/**
 * 事件流（新→旧）。来源可为 WS 实时环形缓冲、REST 历史，或两者合并结果。
 * 事件类型属于 Capability/Application 命名空间：标签优先取后端给的 label，
 * 其次 Capability 声明的 title，最后 humanize(类型名)——前端不维护事件枚举。
 * 首行带进入动画；时间显示时刻，悬停显示完整日期时间。
 */
export function EventFeed({ events, showDevice = true, limit = 30 }: {
  events: EventView[]
  showDevice?: boolean
  limit?: number
}) {
  const index = useCapabilityIndex()

  if (!events.length) {
    return <p className="py-6 text-center text-sm text-ink-3">暂无事件</p>
  }
  return (
    <ul className="divide-y divide-hairline">
      {events.slice(0, limit).map((e, i) => {
        const label = eventLabel(e.type, index, payloadLabel(e.payload))
        const tone = eventTone(e.type, index)
        const [edgeId, devId] = e.device_id.split('/')
        return (
          <li key={`${e.id}-${i}`} className={i === 0 ? 'fade-up' : undefined}>
            <div className="flex items-center gap-3 py-2.5">
              <Badge tone={tone} className="max-w-[9rem] truncate">{label}</Badge>
              {showDevice && (
                <Link
                  to={`/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`}
                  className="num truncate font-mono text-[11px] text-ink-3 transition-colors hover:text-accent"
                  title={e.device_id}
                >
                  {devId}
                </Link>
              )}
              <span className="num ml-auto shrink-0 text-[11px] text-ink-3" title={`${fmtDateTime(e.ts)} · ${e.type}`}>
                {fmtTime(e.ts)}
              </span>
            </div>
          </li>
        )
      })}
    </ul>
  )
}