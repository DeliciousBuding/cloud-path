import { useId } from 'react'
import {
  Area, AreaChart, CartesianGrid, Line, LineChart, ReferenceLine, ResponsiveContainer,
  Tooltip, XAxis, YAxis,
} from 'recharts'
import type { SeriesPoint } from '@/store/ws'

const timeTick = (t: number): string => new Date(t * 1000).toLocaleTimeString('zh-CN', { hour12: false })

function ChartTooltip({ active, payload, unit }: {
  active?: boolean
  payload?: { value?: number; payload?: { t?: number } }[]
  unit?: string
}) {
  const p = payload?.[0]
  if (!active || !p || p.value === undefined) return null
  return (
    <div className="card num px-2 py-1 text-[11px] text-ink-2 shadow-lg">
      {p.payload?.t !== undefined && <span className="text-ink-3">{timeTick(p.payload.t)} · </span>}
      <span className="font-medium text-ink">{p.value}</span>
      {unit && <span className="text-ink-3"> {unit}</span>}
    </div>
  )
}

/**
 * 数值趋势（通用）：任何数值观测序列都能画；折线/面积两种形态由调用方切换。
 * 序列来自 store/ws.ts 的通用采样（按 raw 属性名），标签/单位由调用方从数据传入，
 * 组件本身不认识「漂移」或任何具体设备字段。动画恒关（尊重 prefers-reduced-motion）。
 */
export function TrendChart({ points, unit, height = 112, kind = 'area' }: {
  points: SeriesPoint[]
  unit?: string
  height?: number
  kind?: 'area' | 'line'
}) {
  const gradientId = `trend-${useId().replace(/[^a-zA-Z0-9]/g, '')}`

  if (points.length < 2) {
    return (
      <p className="py-8 text-center text-xs text-ink-3">
        采样中——积累两个数据点后显示趋势{unit ? `（${unit}）` : ''}
      </p>
    )
  }

  const data = points.map((p) => ({ t: p.t, v: p.v }))
  const values = data.map((d) => d.v)
  const lo = Math.min(...values)
  const hi = Math.max(...values)
  const pad = (hi - lo) * 0.15 || Math.max(1, Math.abs(hi) * 0.1)
  const crossesZero = lo < 0 && hi > 0

  const axis = (
    <>
      <XAxis
        dataKey="t" tick={{ fontSize: 10, fill: 'var(--color-ink-3)' }} tickFormatter={timeTick}
        minTickGap={48} axisLine={false} tickLine={false} height={18}
      />
      <YAxis
        dataKey="v" width={38} tick={{ fontSize: 10, fill: 'var(--color-ink-3)' }}
        tickFormatter={(v: number) => (Number.isInteger(v) ? String(v) : v.toFixed(1))}
        domain={[lo - pad, hi + pad]} axisLine={false} tickLine={false}
      />
      <CartesianGrid stroke="var(--color-hairline)" strokeDasharray="2 4" vertical={false} />
      <Tooltip content={<ChartTooltip unit={unit} />} isAnimationActive={false} />
      {crossesZero && <ReferenceLine y={0} stroke="var(--color-hairline)" />}
    </>
  )

  return (
    <div className="w-full" style={{ height }}>
      <ResponsiveContainer>
        {kind === 'line' ? (
          <LineChart data={data} margin={{ top: 6, right: 4, bottom: 0, left: -14 }}>
            {axis}
            <Line
              type="monotone" dataKey="v" stroke="var(--color-accent)" strokeWidth={1.8}
              dot={false} isAnimationActive={false}
            />
          </LineChart>
        ) : (
          <AreaChart data={data} margin={{ top: 6, right: 4, bottom: 0, left: -14 }}>
            <defs>
              <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="var(--color-accent)" stopOpacity={0.22} />
                <stop offset="100%" stopColor="var(--color-accent)" stopOpacity={0.02} />
              </linearGradient>
            </defs>
            {axis}
            <Area
              type="monotone" dataKey="v" stroke="var(--color-accent)" strokeWidth={1.8}
              fill={`url(#${gradientId})`} dot={false} isAnimationActive={false}
            />
          </AreaChart>
        )}
      </ResponsiveContainer>
    </div>
  )
}
