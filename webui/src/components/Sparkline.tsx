import { useId } from 'react'
import { cn } from '@/lib/cn'
import type { SeriesPoint } from '@/store/ws'

/**
 * 迷你趋势线（纯 SVG，无图表库依赖）：给卡片/列表行一个「最近在动吗」的视觉锚。
 * 只画形状不画坐标——精确读数交给趋势视图与 tooltip，避免小图塞假精度。
 * 数据不足两点时不渲染（不画假线）。
 */
export function Sparkline({ points, className, height = 22 }: {
  points: SeriesPoint[]
  className?: string
  height?: number
}) {
  const id = `spark-${useId().replace(/[^a-zA-Z0-9]/g, '')}`
  if (points.length < 2) return null
  const vs = points.map((p) => p.v)
  const lo = Math.min(...vs)
  const hi = Math.max(...vs)
  const span = hi - lo || 1
  const W = 100
  const step = W / (points.length - 1)
  const xy = points.map((p, i) => [i * step, 2 + (1 - (p.v - lo) / span) * (height - 4)] as const)
  const path = xy.map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`).join(' ')
  const area = `${path} L${W},${height} L0,${height} Z`
  const last = xy[xy.length - 1]
  return (
    <svg viewBox={`0 0 ${W} ${height}`} preserveAspectRatio="none" aria-hidden
      className={cn('block w-full', className)} style={{ height }}>
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="var(--color-accent)" stopOpacity={0.25} />
          <stop offset="100%" stopColor="var(--color-accent)" stopOpacity={0} />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${id})`} stroke="none" />
      <path d={path} fill="none" stroke="var(--color-accent)" strokeWidth={1.2}
        vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" />
      {last && <circle cx={last[0]} cy={last[1]} r={1.6} fill="var(--color-accent)" />}
    </svg>
  )
}
