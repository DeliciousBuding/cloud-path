import { describe, expect, it } from 'vitest'
import { clipChartPoints, mergeChartPoints, summarizeSeries } from '@/components/charts'

describe('chart-utils', () => {
  it('统计只描述输入事实，空序列返回 null', () => {
    expect(summarizeSeries([])).toBeNull()
    expect(summarizeSeries([
      { t: 10, v: 2 },
      { t: 20, v: 6 },
      { t: 30, v: 4 },
    ])).toEqual({
      count: 3, latest: 4, min: 2, max: 6, average: 4, firstT: 10, lastT: 30,
    })
  })

  it('按时间合并去重，后出现的序列覆盖同刻值', () => {
    expect(mergeChartPoints(
      [{ t: 10, v: 1 }, { t: 20, v: 2 }],
      [{ t: 20, v: 9 }, { t: 30, v: 3 }],
    )).toEqual([{ t: 10, v: 1 }, { t: 20, v: 9 }, { t: 30, v: 3 }])
  })

  it('时间窗裁剪为闭区间', () => {
    const points = [{ t: 1, v: 1 }, { t: 2, v: 2 }, { t: 3, v: 3 }]
    expect(clipChartPoints(points, 2, 3)).toEqual([{ t: 2, v: 2 }, { t: 3, v: 3 }])
  })
})
