import type { ChartPoint } from './types'

export interface SeriesStats {
  count: number
  latest: number
  min: number
  max: number
  average: number
  firstT: number
  lastT: number
}

/** 对图表序列做通用统计；空序列返回 null，调用方决定空态。 */
export function summarizeSeries(points: readonly ChartPoint[]): SeriesStats | null {
  if (points.length === 0) return null
  let min = points[0].v
  let max = points[0].v
  let sum = 0
  for (const point of points) {
    min = Math.min(min, point.v)
    max = Math.max(max, point.v)
    sum += point.v
  }
  const first = points[0]
  const last = points[points.length - 1]
  return {
    count: points.length,
    latest: last.v,
    min,
    max,
    average: sum / points.length,
    firstT: first.t,
    lastT: last.t,
  }
}

/** 按时间戳去重并升序合并多个序列；同一时刻以后出现的值优先。 */
export function mergeChartPoints(...groups: readonly ChartPoint[][]): ChartPoint[] {
  const byTime = new Map<number, ChartPoint>()
  for (const group of groups) {
    for (const point of group) byTime.set(point.t, point)
  }
  return [...byTime.values()].sort((a, b) => a.t - b.t)
}

/** 截取闭区间内的点；from/to 为空表示该侧无界。 */
export function clipChartPoints(points: readonly ChartPoint[], from?: number, to?: number): ChartPoint[] {
  return points.filter((point) => (from === undefined || point.t >= from) && (to === undefined || point.t <= to))
}
