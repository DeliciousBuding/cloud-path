// 390px 溢出收口的共享断言：narrow-viewport 与 admin-narrow 两组用例用同一把尺子，
// 避免「同一件事两套标准」。判据与具体页面无关：
//   承载后端可控长文本的元素，要么自身截断/可断行（truncate / break-words / break-all），
//   要么待在局部滚动容器里（overflow-x-auto / overflow-auto / overflow-hidden），二者其一即算收口。
// 最坏情况统一用 64 个不可断字符。
import { screen } from '@testing-library/react'
import { expect } from 'vitest'

export const LONG = 'x'.repeat(64)

/** 自身截断/可断行，或位于局部滚动容器内 —— 二者其一即视为已收口 */
export function guarded(el: Element): boolean {
  if (/truncate|break-words|break-all/.test(String(el.className ?? ''))) return true
  for (let p = el.parentElement; p; p = p.parentElement) {
    if (/overflow-x-auto|overflow-auto|overflow-hidden|overflow-x-hidden/.test(String(p.className ?? ''))) return true
  }
  return false
}

/** 页面上每一处该文本都必须已收口 */
export function expectContained(text: string): void {
  const hits = screen.getAllByText(text)
  expect(hits.length).toBeGreaterThan(0)
  for (const el of hits) {
    expect(guarded(el), `<${el.tagName.toLowerCase()} class="${String(el.className)}"> 未做截断/滚动收口`).toBe(true)
  }
}

/** 组件不许写内联像素宽度（量程条那类百分比宽度除外） */
export function expectNoInlinePixelWidth(): void {
  for (const el of document.querySelectorAll<HTMLElement>('[style]')) {
    expect(el.style.width).not.toMatch(/px$/)
    expect(el.style.minWidth).not.toMatch(/px$/)
  }
}

/** 单个元素自身必须可断行/截断（用于把长变量插进句子里的提示文案） */
export function expectSelfGuarded(el: Element): void {
  expect(guarded(el), `<${el.tagName.toLowerCase()} class="${String(el.className)}"> 未做截断/滚动收口`).toBe(true)
}