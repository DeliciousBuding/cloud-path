import { describe, expect, it } from 'vitest'
import { cmdMeta, fmtDay } from '@/lib/format'
import { indexCapabilities, normalizeCapabilityDocs } from '@/lib/descriptor'
import { catalogPayload } from '@/test/fixtures'

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

describe('cmdMeta（命令展示名回落顺序）', () => {
  const idx = indexCapabilities(normalizeCapabilityDocs(catalogPayload))

  it('设备命令集声明最优先（同一 cmd 在不同设备可以有不同标题）', () => {
    expect(cmdMeta('relay_on', [{ cmd: 'relay_on', label: '设备侧标题' }], idx).label).toBe('设备侧标题')
  })

  it('没有命令集时吃 catalog 里的 action 声明，并带上说明作为 hint', () => {
    expect(cmdMeta('relay_on', undefined, idx)).toEqual({ label: '闭合', hint: '接通负载' })
  })

  it('声明缺席 → 平台通用词典（跨设备列表不甩英文机器名）', () => {
    expect(cmdMeta('ping')).toEqual({ label: '连通性探测', hint: '' })
    expect(cmdMeta('diag')).toEqual({ label: '板级诊断', hint: '' })
  })

  it('词典也没有 → humanize，机器名原样可读但不假装是中文', () => {
    expect(cmdMeta('some_vendor_cmd')).toEqual({ label: 'Some Vendor Cmd', hint: '' })
  })

  it('不传 idx 就拿不到声明（说明跨设备面必须显式传索引，不是隐式全局态）', () => {
    expect(cmdMeta('relay_on').label).toBe('Relay On')
  })
})
