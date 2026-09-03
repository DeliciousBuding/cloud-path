import { Link } from 'react-router'
import type { DeviceView } from '@/lib/types'
import { Badge, StatusDot } from './ui'
import { StatusBadge, SummaryChips, SummaryGroups } from './SchemaRenderer'
import { summarizeDescriptor, summarizeRaw } from '@/lib/descriptor'
import { useDeviceDescriptor } from '@/hooks/useDescriptor'
import { useReducedMotion } from '@/hooks/useReducedMotion'
import { timeAgo } from '@/lib/format'
import { cn } from '@/lib/cn'

/**
 * 设备卡（Schema 驱动）：主值 / 胶囊 / 分组全部由 Descriptor 的 entities[].observations
 * 与 Capability presentation 推导；Descriptor 缺席时按上报字段做通用回落（summarizeRaw）。
 * 卡片本身不认识任何设备字段名。
 */
export function DeviceCard({ d }: { d: DeviceView }) {
  const [edgeId, devId] = d.id.split('/')
  const reduced = useReducedMotion()
  const { descriptor, capabilities } = useDeviceDescriptor(d.id, edgeId ?? '', devId ?? '', { device: d })

  const summary = descriptor ? summarizeDescriptor(descriptor, capabilities) : summarizeRaw(d.state)
  const alert = !reduced && (
    summary.primary?.tone === 'bad' || summary.chips.some((c) => c.tone === 'bad')
  )

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
        {/* 适配器名由后端注册，长度不可控：允许收缩并截断，避免 390px 撑宽卡片 */}
        <span className="ml-auto min-w-0 shrink">
          <Badge tone={d.online ? 'accent' : 'idle'} className="max-w-full truncate">{d.adapter || '未知适配器'}</Badge>
        </span>
      </div>

      <div className="mt-4 flex items-end justify-between gap-2">
        <div className={cn('num min-w-0 truncate text-[40px] font-semibold leading-none tracking-tight',
          !d.online && 'text-ink-3', alert && 'remind')}
          title={summary.primary?.title}>
          {summary.primary?.text ?? '--'}
          {summary.primary?.unit && (
            <span className="ml-1 text-base font-normal text-ink-3">{summary.primary.unit}</span>
          )}
        </div>
        <div className="shrink-0 pb-1 text-right">
          {descriptor
            ? <StatusBadge status={descriptor.status} />
            : <Badge tone={d.online ? 'ok' : 'idle'}>{d.online ? '在线' : '离线'}</Badge>}
          <p className="mt-1 max-w-[9rem] truncate text-[11px] text-ink-3"
            title={descriptor ? 'Schema 驱动' : '通用回落视图（尚无 Descriptor）'}>
            {summary.primary?.label ?? (descriptor ? 'Schema 驱动' : '等待上报')}
          </p>
        </div>
      </div>

      <div className="mt-3 flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <SummaryChips chips={summary.chips} />
        </div>
        <span className="num shrink-0 pt-0.5 text-[11px] text-ink-3" title={`设备键 ${d.id}`}>
          {d.online ? timeAgo(d.updated_at) : `最后见 ${timeAgo(d.last_seen)}`}
        </span>
      </div>

      {summary.groups.length > 0 && (
        <div className="mt-3 border-t border-hairline pt-3">
          <SummaryGroups groups={summary.groups} />
        </div>
      )}
    </Link>
  )
}