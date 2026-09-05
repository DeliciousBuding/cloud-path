import { useMemo } from 'react'
import { Link } from 'react-router'
import { Badge, StatusDot } from './ui'
import { useDeviceDescriptor } from '@/hooks/useDescriptor'
import { useLive } from '@/store/ws'
import { Sparkline } from './Sparkline'
import { metricTiles, statusMeta } from '@/lib/descriptor'
import { fmtDateTime } from '@/lib/format'
import type { DeviceView } from '@/lib/types'

/**
 * 设备列表行：Name / Edge / 关键读数 / Status / Trend / Last Seen。
 * 关键读数列只在 2xl 出现（声明主观测至多两条）：中宽屏里多值换行会把行撑高，
 * 而能力的完整事实面在设备详情「能力」页，列表行不重复承载芯片墙。
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
  // 会话数值序列里第一条够画的序列 → 行内火花线（纯形状，不标精度）
  const series = useLive((s) => s.series[d.id])
  const spark = useMemo(() => {
    for (const pts of Object.values(series ?? {})) if (pts.length >= 2) return pts
    return null
  }, [series])

  return (
    <li className="grid gap-x-4 gap-y-1.5 border-b border-hairline px-4 py-2.5 last:border-b-0 lg:grid-cols-[minmax(0,1.5fr)_minmax(0,0.9fr)_5.5rem_6.5rem_9.5rem] lg:items-center 2xl:grid-cols-[minmax(0,1.5fr)_minmax(0,0.9fr)_minmax(0,1.4fr)_5.5rem_6.5rem_9.5rem]">
      {/* Name（+ 窄屏时把状态徽标放到同一行，避免多占一行） */}
      <div className="flex min-w-0 items-center gap-2">
        <StatusDot online={d.online} />
        <Link
          to={`/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`}
          className="min-w-0 truncate text-[13px] font-medium no-underline hover:text-accent"
          title={`${name} · ${d.id}`}
        >
          {name}
        </Link>
        <span className="num hidden shrink-0 truncate font-mono text-[11px] text-ink-3 lg:inline" title={d.id}>
          {devId}
        </span>
        <span className="ml-auto shrink-0 lg:hidden">
          {st
            ? <Badge tone={st.tone}>{st.label}</Badge>
            : <Badge tone={d.online ? 'ok' : 'idle'}>{d.online ? '在线' : '离线'}</Badge>}
        </span>
      </div>

      {/* Edge */}
      <div className="num min-w-0 truncate text-xs text-ink-2" title={`${d.edge_id}${d.adapter ? ` · 适配器 ${d.adapter}` : ''}${d.port ? ` · ${d.port}` : ''}`}>
        <span className="text-ink-3 lg:hidden">边缘 </span>
        {d.edge_id || '—'}
        {d.adapter && <span className="text-ink-3"> · {d.adapter}</span>}
      </div>

      {/* 关键读数（声明主观测；能力全量事实面在详情页「能力」tab） */}
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 lg:hidden 2xl:flex">
        <span className="text-[12px] text-ink-3 lg:hidden">读数 </span>
        {!descriptor ? (
          <span className="text-[12px] text-ink-3" title="设备尚未上报声明，读数未知">等待声明</span>
        ) : metrics.length === 0 ? (
          <span className="text-[12px] text-ink-3" title="声明里没有标量主观测">暂无可读数值</span>
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
      </div>

      {/* Online / Offline（桌面列；窄屏已在名称行呈现） */}
      <div className="hidden lg:block">
        {st
          ? <Badge tone={st.tone}>{st.label}</Badge>
          : <Badge tone={d.online ? 'ok' : 'idle'}>{d.online ? '在线' : '离线'}</Badge>}
      </div>

      {/* 会话趋势火花线（仅桌面；无序列时留空，不画假线） */}
      <div className="hidden min-w-0 lg:block">
        {spark && <Sparkline points={spark} height={18} />}
      </div>

      {/* Last Seen（绝对时间） */}
      <div className="num min-w-0 truncate text-left font-mono text-[11px] text-ink-3 lg:text-right" title={seen ? fmtDateTime(seen) : '从未上报'}>
        <span className="lg:hidden">{d.online ? '更新于 ' : '最后见 '}</span>
        {seen ? fmtDateTime(seen) : '从未上报'}
      </div>
    </li>
  )
}

/** 列表表头（仅桌面；窄屏每行自带列名，故隐藏） */
export function DeviceRowHead() {
  return (
    <li
      aria-hidden
      className="hidden gap-x-4 px-4 pb-2 text-[12px] font-medium text-ink-3 lg:grid lg:grid-cols-[minmax(0,1.5fr)_minmax(0,0.9fr)_5.5rem_6.5rem_9.5rem] 2xl:grid-cols-[minmax(0,1.5fr)_minmax(0,0.9fr)_minmax(0,1.4fr)_5.5rem_6.5rem_9.5rem]"
    >
      <span>设备</span><span>边缘节点</span><span className="hidden 2xl:inline">关键读数</span><span>状态</span><span>趋势</span><span className="text-right">最后见</span>
    </li>
  )
}