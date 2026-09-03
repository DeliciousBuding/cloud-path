// Vitest 全局装配：jest-dom 断言 + 可控 matchMedia + jsdom 缺失的浏览器 API 替身。
// 每个用例结束后自动卸载 React 树并清空媒体查询登记与 localStorage（令牌不跨用例泄漏）。
// 纯文件系统用例（design-system.test.ts）跑在 node 环境下，这里必须按环境降级而不是直接引用 window。
import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'
import { installMatchMediaStub, resetMediaQueries } from './media'

const hasDom = typeof window !== 'undefined' && typeof document !== 'undefined'

class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

if (hasDom) {
  installMatchMediaStub()
  if (typeof globalThis.ResizeObserver === 'undefined') {
    Object.defineProperty(globalThis, 'ResizeObserver', {
      writable: true, configurable: true, value: ResizeObserverStub,
    })
  }
}

afterEach(() => {
  if (!hasDom) return
  cleanup()
  resetMediaQueries()
  localStorage.clear()
})