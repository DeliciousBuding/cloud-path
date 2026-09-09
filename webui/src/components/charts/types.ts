export interface ChartPoint {
  /** Unix 秒 */
  t: number
  v: number
}

export type ChartKind = 'area' | 'line' | 'bar'

export type ChartTimeFormatter = (t: number) => string
