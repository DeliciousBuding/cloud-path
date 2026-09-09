// Vitest 全局装配：jest-dom 断言 + 可控 matchMedia + jsdom 缺失的浏览器 API 替身。
// 每个用例结束后自动卸载 React 树并清空 localStorage（令牌不跨用例泄漏）。
// setup 也可能被非 DOM 用例加载，这里必须按环境降级而不是直接引用 window。
import '@testing-library/jest-dom/vitest'
import { configure } from '@testing-library/dom'
import { cleanup } from '@testing-library/react'
import { afterEach, beforeEach } from 'vitest'
import { i18n } from '@/i18n'
import { DEFAULT_LOCALE } from '@/i18n/config'
import { installMatchMediaStub } from './media'

const hasDom = typeof window !== 'undefined' && typeof document !== 'undefined'

class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

if (hasDom) {
  // 并行 CI 满载时 findBy*/waitFor 默认 1000ms 过紧，会误报超时（单线程串行本可通过）。
  // 放宽到 5000ms：只影响等待窗口，不改变断言语义。
  configure({ asyncUtilTimeout: 5000 })
  installMatchMediaStub()
  if (typeof globalThis.ResizeObserver === 'undefined') {
    Object.defineProperty(globalThis, 'ResizeObserver', {
      writable: true, configurable: true, value: ResizeObserverStub,
    })
  }
}

beforeEach(async () => {
  if (!hasDom) return
  await i18n.changeLanguage(DEFAULT_LOCALE)
})

afterEach(() => {
  if (!hasDom) return
  cleanup()
  localStorage.clear()
})


