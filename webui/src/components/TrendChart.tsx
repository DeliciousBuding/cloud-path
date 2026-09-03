import { useId } from 'react'
import { Area, AreaChart, ReferenceLine, ResponsiveContainer, YAxis } from 'recharts'
import type { SeriesPoint } from '@/store/ws'

/**
 * 会话内数值趋势（通用）：任何数值观测序列都能画。
 * 序列来自 store/ws.ts 的通用采样（按 raw 属性名），标签/单位由调用方从数据传入，
 * 组件本身不认识「漂移」或任何具体设备字段。动画恒关（尊重 prefers-reduced-motion）。
 */
export function TrendChart({ points, unit, height = 112 }: {
  points: SeriesPoint[]
  unit?: string
  height?: number
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

  return (
    <div className="w-full" style={{ height }}>
      <ResponsiveContainer>
        <AreaChart data={data} margin={{ top: 6, right: 4, bottom: 0, left: -18 }}>
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--color-accent)" stopOpacity={0.22} />
              <stop offset="100%" stopColor="var(--color-accent)" stopOpacity={0.02} />
            </linearGradient>
          </defs>
          <YAxis
            dataKey="v" width={38} tick={{ fontSize: 10, fill: 'var(--color-ink-3)' }}
            tickFormatter={(v: number) => (Number.isInteger(v) ? String(v) : v.toFixed(1))}
            domain={[lo - pad, hi + pad]}
          />
          {crossesZero && <ReferenceLine y={0} stroke="var(--color-hairline)" />}
          <Area
            type="monotone" dataKey="v" stroke="var(--color-accent)" strokeWidth={1.8}
            fill={`url(#${gradientId})`} dot={false} isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}