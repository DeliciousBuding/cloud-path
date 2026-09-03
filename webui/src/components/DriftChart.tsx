import { Area, AreaChart, ReferenceLine, ResponsiveContainer, YAxis } from 'recharts'
import type { DriftPoint } from '@/store/ws'

/** 漂移趋势（会话内采样）：±1min 参考带，苹果蓝渐变面积 */
export function DriftChart({ points }: { points: DriftPoint[] }) {
  if (points.length < 2) {
    return (
      <p className="py-8 text-center text-xs text-ink-3">
        采样中——积累两个数据点后显示趋势
      </p>
    )
  }
  const data = points.map((p) => ({ t: p.t, v: p.v }))
  return (
    <div className="h-28 w-full">
      <ResponsiveContainer>
        <AreaChart data={data} margin={{ top: 6, right: 4, bottom: 0, left: -18 }}>
          <defs>
            <linearGradient id="driftFill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--color-accent)" stopOpacity={0.22} />
              <stop offset="100%" stopColor="var(--color-accent)" stopOpacity={0.02} />
            </linearGradient>
          </defs>
          <YAxis
            dataKey="v" width={34} tick={{ fontSize: 10, fill: 'var(--color-ink-3)' }}
            tickFormatter={(v: number) => `${v > 0 ? '+' : ''}${v}`}
            domain={(auto) => {
              const m = Math.max(2, ...auto.map(Math.abs))
              return [-m - 1, m + 1]
            }}
          />
          <ReferenceLine y={0} stroke="var(--color-hairline)" />
          <Area
            type="monotone" dataKey="v" stroke="var(--color-accent)" strokeWidth={1.8}
            fill="url(#driftFill)" dot={false} isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}