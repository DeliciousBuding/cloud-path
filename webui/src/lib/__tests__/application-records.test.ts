import { describe, expect, it } from 'vitest'
import { emptyRecordValue, recordEntries, recordFieldLabel, recordHeadline, recordTimestamp } from '@/lib/application-plane'

describe('应用记录可读投影', () => {
  it('已填写的状态类字段排在前面，空值不能挤掉结果', () => {
    const data = { closed_at: '', id: 'record-a', created: 'earlier', reminder_state: 'succeeded', state: 'missed', count: 0, enabled: false }
    const entries = recordEntries(data)
    expect(entries.slice(0, 2).map(([key]) => key)).toEqual(['reminder_state', 'state'])
    expect(entries.at(-1)).toEqual(['closed_at', ''])
    expect(entries.find(([key]) => key === 'state')?.[1]).toBe('missed')
    expect(data.closed_at).toBe('')
  })
  it('数组保留位置、false、零与 null，不按对象规则重新排序', () => {
    expect(recordEntries([null, false, 0, ''])).toEqual([['0', null], ['1', false], ['2', 0], ['3', '']])
    expect(emptyRecordValue(false)).toBe(false)
    expect(emptyRecordValue(0)).toBe(false)
  })
  it('未知字段保留身份，不伪造数据项名称或业务译文', () => {
    expect(recordFieldLabel('id')).toBe('标识')
    expect(recordFieldLabel('compartment')).toBe('仓位')
    expect(recordFieldLabel('checked_at')).toBe('检查时间')
    expect(recordFieldLabel('temperature_c')).toBe('温度 (℃)')
    expect(recordFieldLabel('opaque_domain_flag')).toBe('opaque_domain_flag')
    expect(recordFieldLabel('自定义备注')).toBe('自定义备注')
  })
  it('标题由通用内容生成，显式名称优先且未知结构回落', () => {
    expect(recordHeadline({ slot: 'A1', state: 'taken' }, '记录 1')).toEqual({
      title: '位置 A1 · 状态 taken', usedKeys: ['slot', 'state'],
    })
    expect(recordHeadline({ name: '入口检查', state: 'running' }, '记录 2')).toEqual({
      title: '入口检查', usedKeys: ['name'],
    })
    expect(recordHeadline({ opaque_domain_flag: true }, '记录 3')).toEqual({
      title: '记录 3', usedKeys: [],
    })
    expect(recordHeadline({ state: 'triggered' }, '记录 4', { state: { triggered: '已触发' } })).toEqual({
      title: '状态 已触发', usedKeys: ['state'],
    })
  })
  it('有效带时区时间统一到指定时区，等价时刻得到一致显示', () => {
    expect(recordTimestamp('2026-09-05T22:30:03Z', 'Asia/Shanghai')).toBe('2026/09/06 06:30:03')
    expect(recordTimestamp('2026-09-06T06:30:03+08:00', 'UTC')).toBe('2026/09/05 22:30:03')
    expect(recordTimestamp('1970-01-01T00:00:00Z', 'UTC')).toBe('1970/01/01 00:00:00')
  })
  it.each(['2026-02-30T06:30:00Z', '2026-09-06T06:30:00', '06:30', '123456', 'state-with-date-2026-09-06'])('不把 %s 猜成时间', (value) => {
    expect(recordTimestamp(value)).toBeUndefined()
  })
})
