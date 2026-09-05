import { describe, expect, it } from 'vitest'
import { fmtDay } from '@/lib/format'

describe('fmtDay（时间线 day 组头）', () => {
  const nowSec = () => Math.floor(Date.now() / 1000)

  it('当天 -> 今天', () => {
    expect(fmtDay(nowSec())).toBe('今天')
  })

  it('前一日 -> 昨天（与当前时刻无关，按自然日边界）', () => {
    expect(fmtDay(nowSec() - 86_400)).toBe('昨天')
  })

  it('更早的同年日期 -> 月日', () => {
    const d = new Date()
    d.setDate(d.getDate() - 5)
    const want = d.toLocaleDateString('zh-CN', { month: 'long', day: 'numeric' })
    expect(fmtDay(Math.floor(d.getTime() / 1000))).toBe(want)
  })

  it('跨年补年份，避免「12月31日」不知何年', () => {
    const ts = Math.floor(new Date(2025, 11, 31, 12).getTime() / 1000)
    expect(fmtDay(ts)).toContain('2025')
  })
})
