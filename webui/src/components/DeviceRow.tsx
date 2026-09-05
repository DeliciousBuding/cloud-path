import { useMemo } from 'react'
import { Link } from 'react-router'
import { Braces } from 'lucide-react'
import { Badge, StatusDot } from './ui'
import { useDeviceDescriptor } from '@/hooks/useDescriptor'
import { useLive } from '@/store/ws'
import { Sparkline } from './Sparkline'
import { capabilityLabel, statusMeta } from '@/lib/descriptor'
import { fmtDateTime } from '@/lib/format'
import type { DeviceView } from '@/lib/types'

/**
 * 设备列表行：Name / Edge / Capabilities / Trend / Status / Last Seen。
 * 能力列只在 2xl 出现：中宽屏（含概览页的窄面板）里芯片换行会把行撑到三倍高，
 * 而能力的完整事实面在设备详情「能力」页，列表行不需要重复承载。
 *
 * 桌面（lg）是紧凑表格行；窄屏自动堆叠并补上列名，因此 390px 下每一列都仍可读、可点。
 * Capabilities 一律来自 Descriptor 声明（entities[].capabilities），前端不维护能力清单；
 * Descriptor 缺席时明确说「未声明能力」，不猜也不编。
 */
export function DeviceRow({ d }: { d: DeviceView }) {
  const [edgeId, devId] = d.id.split('/')
  const { descriptor, capabilities } = useDeviceDescriptor(d.id, edgeId ?? '', devId ?? '', { device: d })

  const caps = useMemo(() => {
    if (!descriptor) return []
    const set = new Set<string>()
    for (const e of descriptor.entities) for (const c of e.capabilities) if (c) set.add(c)
    return [...set]
  }, [descriptor])

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

      {/* Capabilities（来自 Descriptor 声明） */}
      <div className="flex min-w-0 flex-wrap items-center gap-1 lg:hidden 2xl:flex">
        <span className="text-[11px] text-ink-3 lg:hidden">能力 </span>
        {caps.length === 0 ? (
          <span className="flex items-center gap-1 text-[11px] text-ink-3" title={descriptor ? '设备声明里未列出能力' : '设备尚未上报声明，能力未知'}>
            <Braces size={10} className="shrink-0" />
            {descriptor ? '未声明能力' : '能力未知（未上报声明）'}
          </span>
        ) : (
          <>
            {caps.slice(0, 4).map((ref) => (
              <span key={ref} className="badge max-w-full bg-ink-3/10 text-ink-2" title={ref}>
                <span className="min-w-0 truncate">{capabilityLabel(ref, capabilities)}</span>
              </span>
            ))}
            {caps.length > 4 && (
              <span className="badge shrink-0 bg-ink-3/10 text-ink-3" title={caps.slice(4).join('\n')}>
                +{caps.length - 4}
              </span>
            )}
          </>
        )}
      </div>

      {/* 会话趋势火花线（仅桌面；无序列时留空，不画假线） */}
      <div className="hidden min-w-0 lg:block">
        {spark && <Sparkline points={spark} height={18} />}
      </div>

      {/* Online / Offline（桌面列；窄屏已在名称行呈现） */}
      <div className="hidden lg:block">
        {st
          ? <Badge tone={st.tone}>{st.label}</Badge>
          : <Badge tone={d.online ? 'ok' : 'idle'}>{d.online ? '在线' : '离线'}</Badge>}
      </div>

      {/* Last Seen（绝对时间） */}
      <div className="num text-[11px] text-ink-3" title={seen ? fmtDateTime(seen) : '从未上报'}>
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
      className="hidden gap-x-4 px-4 pb-2 text-[11px] font-medium text-ink-3 lg:grid lg:grid-cols-[minmax(0,1.5fr)_minmax(0,0.9fr)_5.5rem_6.5rem_9.5rem] 2xl:grid-cols-[minmax(0,1.5fr)_minmax(0,0.9fr)_minmax(0,1.4fr)_5.5rem_6.5rem_9.5rem]"
    >
      <span>设备</span><span>边缘节点</span><span className="hidden 2xl:inline">能力</span><span>状态</span><span>趋势</span><span>最后见</span>
    </li>
  )
}