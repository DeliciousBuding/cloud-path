// 「减少动效」偏好：CSS 层已有全局兜底（index.css 的 prefers-reduced-motion 块），
// JS 层负责把条件动画类名彻底摘掉。这里验证 hook 的实时跟随、监听清理，
// 以及 KPI 瓦片的告警闪动在偏好开启时不挂载（信息本身不得因偏好而消失）。
import { act, render, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MetricTile } from '@/components/SchemaRenderer'
import { useReducedMotion } from '@/hooks/useReducedMotion'
import type { SummaryValue } from '@/lib/descriptor'
import { REDUCED_MOTION_QUERY, mediaListenerCount, setMediaMatches } from '@/test/media'

const bad: SummaryValue = { label: '探针', text: '12', tone: 'bad', title: 'probe · level' }

describe('useReducedMotion', () => {
  it('初始值取自系统偏好', () => {
    setMediaMatches(REDUCED_MOTION_QUERY, true)
    const { result } = renderHook(() => useReducedMotion())
    expect(result.current).toBe(true)
  })

  it('偏好变化时实时跟随（不需要刷新页面）', () => {
    const { result } = renderHook(() => useReducedMotion())
    expect(result.current).toBe(false)
    act(() => { setMediaMatches(REDUCED_MOTION_QUERY, true) })
    expect(result.current).toBe(true)
    act(() => { setMediaMatches(REDUCED_MOTION_QUERY, false) })
    expect(result.current).toBe(false)
  })

  it('卸载时摘掉监听（长会话里不堆积订阅）', () => {
    const { unmount } = renderHook(() => useReducedMotion())
    expect(mediaListenerCount(REDUCED_MOTION_QUERY)).toBeGreaterThan(0)
    unmount()
    expect(mediaListenerCount(REDUCED_MOTION_QUERY)).toBe(0)
  })
})

describe('MetricTile 的动效偏好落地', () => {
  it('偏好关闭：告警值挂 .remind 闪动类', () => {
    setMediaMatches(REDUCED_MOTION_QUERY, false)
    const { container } = render(<MetricTile v={bad} />)
    expect(container.querySelector('.remind')).not.toBeNull()
  })

  it('偏好开启：不挂任何闪动类，但数值与标签信息一个都不少', () => {
    setMediaMatches(REDUCED_MOTION_QUERY, true)
    const { container } = render(<MetricTile v={bad} />)
    expect(container.querySelector('.remind')).toBeNull()
    // .fade-up 这类进场动画仍留在 DOM 上，由 index.css 的 prefers-reduced-motion 全局块压到 0.001ms
    // （见 src/test/design-system.test.ts 的 CSS 兜底断言），JS 侧只负责摘掉循环闪动。
    expect(container.textContent).toContain('12')
    expect(container.textContent).toContain('探针')
  })

  it('运行中切换偏好，闪动类即时摘除', () => {
    setMediaMatches(REDUCED_MOTION_QUERY, false)
    const { container } = render(<MetricTile v={bad} />)
    expect(container.querySelector('.remind')).not.toBeNull()
    act(() => { setMediaMatches(REDUCED_MOTION_QUERY, true) })
    expect(container.querySelector('.remind')).toBeNull()
  })
})
