import { useMemo } from 'react'
import { Link } from 'react-router'
import { ChevronRight } from 'lucide-react'
import { Badge } from './ui'
import { useDeviceDescriptor } from '@/hooks/useDescriptor'
import { metricTiles, statusMeta } from '@/lib/descriptor'
import { fmtDateTime } from '@/lib/format'
import type { DeviceView } from '@/lib/types'

/**
 * 设备列表行：Name / Edge / 关键读数 / Status / Last Seen。
 * 1440px 视口起显示关键读数列（声明主观测至多两条）；窄屏把读数与最近上报收在同一行，
 * 避免「状态点 + 状态文字」和「读数标题」重复占用扫读空间。
 *
 * 桌面（lg）是紧凑表格行；窄屏自动堆叠并补上列名，因此 390px 下每一列都仍可读、可点。
 * 读数一律由 Descriptor 声明推导（primaryObservation + presentation），前端不维护字段清单；
 * Descriptor 缺席时明确说「等待声明」，不猜也不编。
 */
export function DeviceRow({ d }: { d: DeviceView }) {
  const [edgeId, devId] = d.id.split('/')
  const { descriptor, capabilities } = useDeviceDescriptor(d.id, edgeId ?? '', devId ?? '', { device: d })

  // 舰队行的「关键读数」：声明主观测至多两条（metricTiles 与概览 KPI 同一推导，无设备特例）
  const metrics = useMemo(() => (descriptor ? metricTiles(descriptor, capabilities, 2) : []), [descriptor, capabilities])

  const st = descriptor ? statusMeta(descriptor.status) : null
  const name = d.name || devId || d.id
  const seen = d.online ? d.updated_at : d.last_seen
  return (
    <li className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 border-b border-hairline px-4 py-3 last:border-b-0 lg:grid-cols-[minmax(0,1.5fr)_minmax(0,0.9fr)_5.5rem_9.5rem] lg:items-center xl:grid-cols-[minmax(0,1.35fr)_minmax(0,0.8fr)_minmax(0,1.2fr)_5.5rem_9.5rem]">
      {/* Name（窄屏把状态徽标放在同一行，不重复状态点） */}
      <div className="col-span-2 flex min-w-0 items-center gap-2 lg:col-span-1">
        <Link
          to={`/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`}
          className="flex min-w-0 items-center gap-1 text-[13px] font-medium no-underline hover:text-accent"
          title={`${name} · ${d.id}`}
        >
          <span className="min-w-0 truncate">{name}</span>
          <ChevronRight size={11} className="shrink-0 text-ink-3" />
        </Link>
        <span className="num hidden shrink-0 truncate font-mono text-[11px] text-ink-3 2xl:inline" title={d.id}>
          {devId}
        </span>
        <span className="ml-auto shrink-0 lg:hidden">
          {st
            ? <Badge tone={st.tone}>{st.label}</Badge>
            : <Badge tone={d.online ? 'ok' : 'idle'}>{d.online ? '在线' : '离线'}</Badge>}
        </span>
      </div>

      {/* Edge */}
      <div className="num col-span-2 min-w-0 truncate text-xs text-ink-2 lg:col-span-1" title={`${d.edge_id}${d.adapter ? ` · 设备类型 ${d.adapter}` : ''}${d.port ? ` · ${d.port}` : ''}`}>
        <span className="text-ink-3 lg:hidden">网关 </span>
        {d.edge_id || '—'}
        {d.adapter && <span className="text-ink-3"> · {d.adapter}</span>}
      </div>

      {/* 关键读数（声明主观测；能力全量事实面在详情页「能力」tab） */}
      <div className="col-span-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 lg:hidden xl:col-span-1 xl:flex">
        {!descriptor ? (
          <span className="text-[12px] text-ink-3" title="设备尚未同步信息，读数未知">读数等待同步</span>
        ) : metrics.length === 0 ? (
          <span className="text-[12px] text-ink-3" title="设备信息里没有可显示的主读数">暂无可读数值</span>
        ) : (
          metrics.map((m, i) => (
            <span key={`${m.label}-${i}`} className="num flex min-w-0 items-baseline gap-1 text-[12px]" title={m.title}>
              <span className="min-w-0 truncate text-ink-3">{m.label}</span>
              <span className="min-w-0 truncate font-medium text-ink-2">
                {m.text}{m.unit ? ` ${m.unit}` : ''}
              </span>
            </span>
          ))
        )}
        <span className="num ml-auto shrink-0 font-mono text-[11px] text-ink-3 lg:hidden"
          title={seen ? fmtDateTime(seen) : '从未更新'}>
          {d.online ? '更新于 ' : '最后在线 '}{seen ? fmtDateTime(seen) : '从未更新'}
        </span>
      </div>

      {/* Online / Offline（桌面列；窄屏已在名称行呈现） */}
      <div className="hidden lg:block">
        {st
          ? <Badge tone={st.tone}>{st.label}</Badge>
          : <Badge tone={d.online ? 'ok' : 'idle'}>{d.online ? '在线' : '离线'}</Badge>}
      </div>

      {/* Last Seen（桌面列；窄屏已并入读数行） */}
      <div className="num hidden min-w-0 truncate text-right font-mono text-[11px] text-ink-3 lg:block" title={seen ? fmtDateTime(seen) : '从未更新'}>
        {seen ? fmtDateTime(seen) : '从未更新'}
      </div>
    </li>
  )
}

/** 列表表头（仅桌面；窄屏每行自带列名，故隐藏） */
export function DeviceRowHead() {
  return (
    <li
      aria-hidden
      className="hidden gap-x-4 px-4 pb-2 text-[12px] font-medium text-ink-3 lg:grid lg:grid-cols-[minmax(0,1.5fr)_minmax(0,0.9fr)_5.5rem_9.5rem] xl:grid-cols-[minmax(0,1.35fr)_minmax(0,0.8fr)_minmax(0,1.2fr)_5.5rem_9.5rem]"
    >
      <span>设备</span><span>网关</span><span className="hidden xl:inline">关键数据</span><span>状态</span><span className="text-right">最近上报</span>
    </li>
  )
}
