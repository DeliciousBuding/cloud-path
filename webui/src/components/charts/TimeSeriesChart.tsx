import {
  Area, AreaChart, Bar, BarChart, Brush, CartesianGrid, Line, LineChart, ReferenceLine,
  ResponsiveContainer, Tooltip, XAxis, YAxis,
} from 'recharts'
import { useTranslation } from 'react-i18next'
import '@/i18n'
import { cn } from '@/lib/cn'
import type { ChartKind, ChartPoint, ChartTimeFormatter } from './types'

interface TooltipEntry {
  value?: number
  payload?: ChartPoint
}

function ChartTooltip({ active, payload, unit, timeFormatter }: {
  active?: boolean
  payload?: TooltipEntry[]
  unit?: string
  timeFormatter: ChartTimeFormatter
}) {
  const point = payload?.[0]
  if (!active || !point || point.value === undefined) return null
  return (
    <div className="card num px-2 py-1 text-meta text-ink-2 shadow-lg">
      {point.payload?.t !== undefined && <span className="text-ink-3">{timeFormatter(point.payload.t)} · </span>}
      <span className="font-medium text-ink">{point.value}</span>
      {unit && <span className="text-ink-3"> {unit}</span>}
    </div>
  )
}

export interface TimeSeriesChartProps {
  points: readonly ChartPoint[]
  kind?: ChartKind
  height?: number
  unit?: string
  zeroBase?: boolean
  hideY?: boolean
  xTick?: ChartTimeFormatter
  tooltipTime?: ChartTimeFormatter
  zoomable?: boolean
  emptyLabel?: string
  ariaLabel?: string
  className?: string
}

/**
 * 通用单序列时间图。组件只认识 {t,v} 和展示选项，不认识设备、字段或数据获取；
 * 数据来源由调用方负责。面积/折线/柱状共用同一套坐标、Tooltip 与空态。
 *
 * 设计约束：
 *   - 动画恒关，避免实时刷新造成视觉噪声；
 *   - zoomable 使用 Recharts Brush，仅放大视窗，不改数据；
 *   - 计数/频次图由调用方传 zeroBase，避免悬空基线夸大差异。
 */
export function TimeSeriesChart({
  points, kind = 'area', height = 112, unit, zeroBase = false, hideY = false,
  xTick, tooltipTime, zoomable = false, emptyLabel, ariaLabel, className,
}: TimeSeriesChartProps) {
  const { i18n } = useTranslation()
  const defaultTick: ChartTimeFormatter = (t) => new Date(t * 1000).toLocaleTimeString(i18n.resolvedLanguage ?? i18n.language, { hour12: false })
  const tickFormatter = xTick ?? defaultTick
  const tooltipFormatter = tooltipTime ?? tickFormatter

  if (points.length < 2) {
    return (
      <div className={cn('flex w-full items-center justify-center text-center', className)} style={{ height }}>
        <p className="text-meta text-ink-3">{emptyLabel}</p>
      </div>
    )
  }

  const data = points.map((point) => ({ t: point.t, v: point.v }))
  const values = data.map((point) => point.v)
  const lo = Math.min(...values)
  const hi = Math.max(...values)
  const pad = zeroBase ? Math.max(1, hi * 0.12) : (hi - lo) * 0.15 || Math.max(1, Math.abs(hi) * 0.1)
  const domain: [number, number] = [zeroBase || lo >= 0 ? 0 : lo - pad, hi + pad]
  const margin = { top: 6, right: 4, bottom: zoomable ? 2 : 0, left: hideY ? 0 : -14 }

  const xAxis = (
    <XAxis
      dataKey="t"
      tick={{ fontSize: 11, fill: 'var(--color-ink-3)', fontFamily: 'var(--font-mono)' }}
      tickFormatter={tickFormatter}
      minTickGap={48}
      axisLine={false}
      tickLine={false}
      height={18}
    />
  )
  const yAxis = hideY ? null : (
    <YAxis
      dataKey="v"
      width={38}
      tick={{ fontSize: 11, fill: 'var(--color-ink-3)', fontFamily: 'var(--font-mono)' }}
      tickFormatter={(value: number) => (zeroBase ? String(Math.round(value)) : Number.isInteger(value) ? String(value) : value.toFixed(1))}
      domain={domain}
      axisLine={false}
      tickLine={false}
      allowDecimals={!zeroBase}
    />
  )
  const grid = hideY ? null : <CartesianGrid stroke="var(--color-hairline)" strokeDasharray="2 4" vertical={false} />
  const tip = <Tooltip content={<ChartTooltip unit={unit} timeFormatter={tooltipFormatter} />} isAnimationActive={false} />
  const zeroLine = !zeroBase && lo < 0 && hi > 0 ? <ReferenceLine y={0} stroke="var(--color-hairline)" /> : null
  const brush = zoomable && data.length >= 8 ? (
    <Brush
      dataKey="t"
      height={24}
      travellerWidth={8}
      stroke="var(--color-ink-3)"
      fill="var(--color-hairline)"
      fillOpacity={0.35}
      tickFormatter={tickFormatter}
      ariaLabel={ariaLabel}
    />
  ) : null

  return (
    <div
      className={cn('relative w-full', className)}
      style={{ height }}
      role={ariaLabel ? 'img' : undefined}
      aria-label={ariaLabel}
    >
      {unit && !hideY && (
        <span className="pointer-events-none absolute right-1 top-0 z-local text-meta text-ink-3">{unit}</span>
      )}
      <ResponsiveContainer>
        {kind === 'line' ? (
          <LineChart data={data} margin={margin}>
            {xAxis}{yAxis}{grid}{tip}{zeroLine}
            <Line
              type="monotone" dataKey="v" stroke="var(--color-accent)" strokeWidth={1.8}
              dot={false} activeDot={{ r: 3, fill: 'var(--color-accent)', stroke: 'var(--color-surface)' }}
              isAnimationActive={false}
            />
            {brush}
          </LineChart>
        ) : kind === 'bar' ? (
          <BarChart data={data} margin={margin} barCategoryGap="25%">
            {xAxis}{yAxis}{grid}{tip}{zeroLine}
            <Bar dataKey="v" fill="var(--color-accent)" radius={[2, 2, 0, 0]} isAnimationActive={false} />
            {brush}
          </BarChart>
        ) : (
          <AreaChart data={data} margin={margin}>
            {xAxis}{yAxis}{grid}{tip}{zeroLine}
            <Area
              type="monotone" dataKey="v" stroke="var(--color-accent)" strokeWidth={1.8}
              fill="var(--color-accent)" fillOpacity={0.1} dot={false}
              activeDot={{ r: 3, fill: 'var(--color-accent)', stroke: 'var(--color-surface)' }}
              isAnimationActive={false}
            />
            {brush}
          </AreaChart>
        )}
      </ResponsiveContainer>
    </div>
  )
}
