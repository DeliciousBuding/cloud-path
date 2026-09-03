// 「减少动效」偏好：CSS 层已有全局兜底（index.css 的 prefers-reduced-motion 块），
// JS 层负责把条件动画类名彻底摘掉。这里验证 hook 的实时跟随、监听清理，
// 以及 DeviceCard 的告警闪动在偏好开启时不挂载（信息本身不得因偏好而消失）。
import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { DeviceCard } from '@/components/DeviceCard'
import { useReducedMotion } from '@/hooks/useReducedMotion'
import { UNKNOWN_CAP, makeDescriptor, makeDeviceView } from '@/test/fixtures'
import { installFetch, stubResponse } from '@/test/http'
import {
  REDUCED_MOTION_QUERY, mediaListenerCount, setMediaMatches,
} from '@/test/media'
import { renderWithProviders, resetStores } from '@/test/render'

/** quality=bad 且 Capability 未收录 → 摘要主值 tone=bad，触发告警闪动类 */
const badDevice = makeDeviceView({
  name: '探针',
  state: {
    descriptor: makeDescriptor({
      entities: [{
        entity_id: 'e-probe', unique_key: 'probe', category: 'sensor', capabilities: [UNKNOWN_CAP],
        observations: { level: { capability: UNKNOWN_CAP, property: 'level', value: 12, quality: 'bad' } },
      }],
    }),
  },
})

beforeEach(() => {
  resetStores()
  installFetch(() => stubResponse(404, { error: 'not found' }))
})

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

describe('DeviceCard 的动效偏好落地', () => {
  it('偏好关闭：告警值挂 .remind 闪动类', () => {
    setMediaMatches(REDUCED_MOTION_QUERY, false)
    const { container } = renderWithProviders(<DeviceCard d={badDevice} />)
    expect(container.querySelector('.remind')).not.toBeNull()
  })

  it('偏好开启：不挂任何闪动类，但数值与状态信息一个都不少', () => {
    setMediaMatches(REDUCED_MOTION_QUERY, true)
    const { container } = renderWithProviders(<DeviceCard d={badDevice} />)
    expect(container.querySelector('.remind')).toBeNull()
    // .fade-up 这类进场动画仍留在 DOM 上，由 index.css 的 prefers-reduced-motion 全局块压到 0.001ms
    // （见 src/test/design-system.test.ts 的 CSS 兜底断言），JS 侧只负责摘掉循环闪动。
    // 信息不得因偏好而消失
    expect(container.textContent).toContain('12')
    expect(container.querySelector('a[aria-label="设备 探针 详情"]')).not.toBeNull()
  })

  it('运行中切换偏好，闪动类即时摘除', () => {
    setMediaMatches(REDUCED_MOTION_QUERY, false)
    const { container } = renderWithProviders(<DeviceCard d={badDevice} />)
    expect(container.querySelector('.remind')).not.toBeNull()
    act(() => { setMediaMatches(REDUCED_MOTION_QUERY, true) })
    expect(container.querySelector('.remind')).toBeNull()
  })
})