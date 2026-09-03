// 390px 回归守卫：后端给的标识符（edge_id / 适配器名 / 设备名 / 命令名 / 版本号）长度不可控，
// 承载它们的文本容器必须自己截断（truncate/break）或待在局部滚动容器里，
// 否则一个长名字就能把 body 顶出横向滚动。用 64 个不可断字符做最坏情况。
import { screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import Devices from '@/pages/Devices'
import Edges from '@/pages/Edges'
import Settings from '@/pages/Settings'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import type { DeviceView } from '@/lib/types'

const LONG = 'x'.repeat(64)
const health = { ok: true, version: LONG, uptime_s: 60, devices_online: 1, devices_total: 1, edges_online: 1 }

/** 自身截断/可断行，或位于局部滚动容器内 —— 二者其一即视为已收口 */
function guarded(el: Element): boolean {
  if (/truncate|break-words|break-all/.test(String(el.className ?? ''))) return true
  for (let p = el.parentElement; p; p = p.parentElement) {
    if (/overflow-x-auto|overflow-auto|overflow-hidden|overflow-x-hidden/.test(String(p.className ?? ''))) return true
  }
  return false
}

function expectContained(text: string): void {
  const hits = screen.getAllByText(text)
  expect(hits.length).toBeGreaterThan(0)
  for (const el of hits) {
    expect(guarded(el), `<${el.tagName.toLowerCase()} class="${String(el.className)}"> 未做截断/滚动收口`).toBe(true)
  }
}

function expectNoInlinePixelWidth(): void {
  for (const el of document.querySelectorAll<HTMLElement>('[style]')) {
    expect(el.style.width).not.toMatch(/px$/)
    expect(el.style.minWidth).not.toMatch(/px$/)
  }
}

const longDevice: DeviceView = {
  id: `edge-1/${LONG}`, edge_id: LONG, adapter: LONG, name: LONG, port: LONG,
  online: true, state: {}, updated_at: 0, last_seen: 0,
}

beforeEach(() => { resetStores() })

describe('Edges 页', () => {
  it('edge_id / 版本 / 所辖设备键都收口', async () => {
    installFetch((url) => (url === '/api/edges'
      ? stubResponse(200, {
        edges: [{ edge_id: LONG, online: true, version: LONG, devices: [longDevice.id], connected_at: 0 }],
      })
      : stubResponse(404, {})))
    renderWithProviders(<Edges />)
    await screen.findAllByText(LONG)
    expectContained(LONG)
    expectNoInlinePixelWidth()
  })
})

describe('Devices 页 / DeviceCard', () => {
  it('设备名 / 短 ID / 适配器名都收口', async () => {
    installFetch((url) => (url === '/api/devices'
      ? stubResponse(200, { devices: [longDevice] })
      : stubResponse(404, {})))
    renderWithProviders(<Devices />)
    await screen.findAllByText(LONG)
    expectContained(LONG)
    expectNoInlinePixelWidth()
  })
})

describe('Settings 页', () => {
  it('服务版本 / 适配器名 / 命令名都收口', async () => {
    installFetch((url) => {
      if (url === '/healthz') return stubResponse(200, health)
      if (url === '/api/adapters') return stubResponse(200, { adapters: [{ name: LONG, commands: [LONG] }] })
      return stubResponse(404, {})
    })
    renderWithProviders(<Settings />)
    await screen.findAllByText(LONG)
    expectContained(LONG)
    expectNoInlinePixelWidth()
  })
})