import { describe, expect, it } from 'vitest'
import { sectionSlot } from '@/components/plugin-ui/ApplicationConsole'

describe('应用页分区排序', () => {
  it('动作排在只读数据之前', () => {
    expect(sectionSlot({ type: 'actions' })).toBeLessThan(sectionSlot({ type: 'metrics' }))
    expect(sectionSlot({ type: 'metrics' })).toBeLessThan(sectionSlot({ type: 'chart' }))
    expect(sectionSlot({ type: 'chart' })).toBeLessThan(sectionSlot({ type: 'records' }))
    expect(sectionSlot({ type: 'records' })).toBe(sectionSlot({ type: 'timeline' }))
    expect(sectionSlot({ type: 'timeline' })).toBeLessThan(sectionSlot({ type: 'markdown' }))
  })
})
