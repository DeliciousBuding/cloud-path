import { Link } from 'react-router'
import { Clock3 } from 'lucide-react'
import type { DeviceView } from '@/lib/types'
import { Badge, StatusDot, type Tone } from './ui'
import { SlotChips } from './SlotChips'
import { fmtClock, fmtDrift, driftTone, timeAgo } from '@/lib/format'
import { cn } from '@/lib/cn'

function stateTone(raw: number | undefined): Tone {
  if (raw === 1) return 'warn'
  if (raw === 2) return 'bad'
  return 'idle'
}

const DRIFT_CLS: Record<Tone, string> = {
  ok: 'text-ok', warn: 'text-warn', bad: 'text-bad', accent: 'text-accent', idle: 'text-ink-3',
}

/** 设备卡：大时钟 + 漂移 + 状态 + 槽位，整卡可点进详情 */
export function DeviceCard({ d }: { d: DeviceView }) {
  const [edgeId, devId] = d.id.split('/')
  const raw = d.state ?? {}
  const drift = typeof raw.drift_min === 'number' ? raw.drift_min : null
  const slots = Array.isArray(raw.slots) ? raw.slots : undefined
  const tone = driftTone(drift)

  return (
    <Link
      to={`/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`}
      className="card card-lift block p-5 fade-up"
      aria-label={`设备 ${d.name || devId} 详情`}
    >
      <div className="flex items-center gap-2">
        <StatusDot online={d.online} />
        <span className="truncate text-[15px] font-semibold tracking-tight">
          {d.name || devId}
        </span>
        <span className="num truncate font-mono text-[11px] text-ink-3" title={d.id}>{devId}</span>
        <span className="ml-auto shrink-0">
          <Badge tone={d.online ? 'accent' : 'idle'}>{d.adapter || '未知适配器'}</Badge>
        </span>
      </div>

      <div className="mt-4 flex items-end justify-between gap-2">
        <div className={cn('num text-[40px] font-semibold leading-none tracking-tight',
          !d.online && 'text-ink-3', raw.state === 1 && 'remind')}>
          {d.online || raw.clock ? fmtClock(raw.clock as string | undefined) : '--:--'}
        </div>
        <div className="pb-1 text-right">
          <div className={cn('num text-sm font-medium', DRIFT_CLS[tone])}
            title="设备时钟与参考时间的偏差">
            {fmtDrift(drift)}
          </div>
          <div className="text-[11px] text-ink-3">漂移</div>
        </div>
      </div>

      <div className="mt-3 flex items-center gap-2">
        <Badge tone={stateTone(raw.state as number | undefined)}>
          {raw.state === 1 && <Clock3 size={11} />}
          {(raw.state_label as string) ?? (d.online ? '等待转储' : '离线')}
        </Badge>
        <span className="num ml-auto text-[11px] text-ink-3" title={`设备键 ${d.id}`}>
          {d.online ? timeAgo(d.updated_at) : `最后见 ${timeAgo(d.last_seen)}`}
        </span>
      </div>

      {slots && <div className="mt-4"><SlotChips slots={slots} compact /></div>}
    </Link>
  )
}
