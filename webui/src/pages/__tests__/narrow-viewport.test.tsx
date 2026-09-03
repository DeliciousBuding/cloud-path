// 390px 回归守卫：后端给的标识符（edge_id / 适配器名 / 设备名 / 命令名 / 版本号）长度不可控，
// 承载它们的文本容器必须自己截断（truncate/break）或待在局部滚动容器里，
// 否则一个长名字就能把 body 顶出横向滚动。用 64 个不可断字符做最坏情况。
import { screen } from '@testing-library/react'
import { beforeEach, describe, it } from 'vitest'
import Devices from '@/pages/Devices'
import Edges from '@/pages/Edges'
import Settings from '@/pages/Settings'
import { installFetch, stubResponse } from '@/test/http'
import { LONG, expectContained, expectNoInlinePixelWidth } from '@/test/narrow'
import { renderWithProviders, resetStores } from '@/test/render'
import type { DeviceView } from '@/lib/types'

const health = { ok: true, version: LONG, uptime_s: 60, devices_online: 1, devices_total: 1, edges_online: 1 }

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