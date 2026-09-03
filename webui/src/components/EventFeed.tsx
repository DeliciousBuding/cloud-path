import { Link } from 'react-router'
import type { EventView } from '@/lib/types'
import { Badge } from './ui'
import { eventMeta, fmtDateTime, fmtTime } from '@/lib/format'

/**
 * 事件流（新→旧）。来源可为 WS 实时环形缓冲、REST 历史，或两者合并结果。
 * 首行带进入动画；时间显示时刻，悬停显示完整日期时间。
 */
export function EventFeed({ events, showDevice = true, limit = 30 }: {
  events: EventView[]
  showDevice?: boolean
  limit?: number
}) {
  if (!events.length) {
    return <p className="py-6 text-center text-sm text-ink-3">暂无事件</p>
  }
  return (
    <ul className="divide-y divide-hairline">
      {events.slice(0, limit).map((e, i) => {
        const meta = eventMeta(e.type)
        const [edgeId, devId] = e.device_id.split('/')
        return (
          <li key={`${e.id}-${i}`} className={i === 0 ? 'fade-up' : undefined}>
            <div className="flex items-center gap-3 py-2.5">
              <Badge tone={meta.tone} >{meta.label}</Badge>
              {showDevice && (
                <Link
                  to={`/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`}
                  className="num truncate font-mono text-[11px] text-ink-3 transition-colors hover:text-accent"
                  title={e.device_id}
                >
                  {devId}
                </Link>
              )}
              <span className="num ml-auto shrink-0 text-[11px] text-ink-3" title={fmtDateTime(e.ts)}>
                {fmtTime(e.ts)}
              </span>
            </div>
          </li>
        )
      })}
    </ul>
  )
}
