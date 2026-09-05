import { useState } from 'react'
import { Link } from 'react-router'
import { ChevronRight } from 'lucide-react'
import type { EventView } from '@/lib/types'
import { Badge } from './ui'
import { useCapabilityIndex } from '@/hooks/useDescriptor'
import { eventLabel, eventTone, fmtDay, fmtDateTime, fmtTime, payloadLabel } from '@/lib/format'

/**
 * 事件流（新→旧）。来源可为 WS 实时环形缓冲、REST 历史，或两者合并结果。
 * 事件类型属于 Capability/Application 命名空间：标签优先取后端给的 label，
 * 其次 Capability 声明的 title，最后 humanize(类型名)——前端不维护事件枚举。
 * 单行高密度：类型 / 对象 / 载荷展开 / 时刻一行放下；原始载荷按需展开（取证面，不污染扫读）。
 */
export function EventFeed({ events, showDevice = true, limit = 30, fullTime = false }: {
  events: EventView[]
  showDevice?: boolean
  limit?: number
  /** true = 显示完整绝对日期时间（活动页跨天历史用）；false = 只显示时刻（详情页/概览紧凑列表） */
  fullTime?: boolean
}) {
  if (!events.length) {
    return <p className="py-6 text-center text-sm text-ink-3">暂无事件</p>
  }
  const shown = events.slice(0, limit)
  if (!fullTime) {
    return (
      <ul className="divide-y divide-hairline">
        {shown.map((e, i) => (
          <EventRow key={`${e.id}-${i}`} e={e} first={i === 0} showDevice={showDevice} fullTime={fullTime} />
        ))}
      </ul>
    )
  }
  // 跨天历史按天分组：组头是扫读锚点（今天/昨天/日期），组内仍是单行高密度时间线
  const groups: { day: string; items: EventView[] }[] = []
  for (const e of shown) {
    const day = fmtDay(e.ts)
    const last = groups[groups.length - 1]
    if (last && last.day === day) last.items.push(e)
    else groups.push({ day, items: [e] })
  }
  return (
    <div className="space-y-4">
      {groups.map((g, gi) => (
        <section key={`${g.day}-${gi}`}>
          <h4 className="mb-1 px-0.5 text-[11px] font-medium text-ink-3">{g.day}</h4>
          <ul className="divide-y divide-hairline">
            {g.items.map((e, i) => (
              <EventRow key={`${e.id}-${gi}-${i}`} e={e} first={gi === 0 && i === 0} showDevice={showDevice} fullTime={fullTime} />
            ))}
          </ul>
        </section>
      ))}
    </div>
  )
}

function EventRow({ e, first, showDevice, fullTime }: {
  e: EventView
  first: boolean
  showDevice: boolean
  fullTime: boolean
}) {
  const index = useCapabilityIndex()
  const [open, setOpen] = useState(false)
  const label = eventLabel(e.type, index, payloadLabel(e.payload))
  const tone = eventTone(e.type, index)
  const [edgeId, devId] = e.device_id.split('/')
  const hasPayload = !!e.payload && e.payload !== '{}'
  return (
    <li className={first ? 'fade-up' : undefined}>
      <div className="flex items-center gap-3 py-2">
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
        {hasPayload && (
          <button
            type="button"
            onClick={() => setOpen((v) => !v)}
            aria-expanded={open}
            className="flex shrink-0 items-center gap-0.5 text-[11px] text-ink-3 transition-colors hover:text-ink-2">
            <ChevronRight size={11} className={open ? 'rotate-90 transition-transform' : 'transition-transform'} />
            详情
          </button>
        )}
        <span className="num ml-auto shrink-0 text-[11px] text-ink-3" title={`${fmtDateTime(e.ts)} · ${e.type}`}>
          {fullTime ? fmtDateTime(e.ts) : fmtTime(e.ts)}
        </span>
      </div>
      {open && (
        <pre tabIndex={0} role="group" aria-label="事件原始载荷"
          className="num mb-2 max-h-40 overflow-auto rounded-lg bg-surface-2 p-2 font-mono text-[10px] leading-relaxed text-ink-2">
          {e.payload}
        </pre>
      )}
    </li>
  )
}
