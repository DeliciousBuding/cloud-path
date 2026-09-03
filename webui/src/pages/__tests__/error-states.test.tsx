// 「接口失败」与「真的没有数据」是两种完全不同的结论，必须分开呈现。
// 把 500 渲染成「还没有设备接入」等于告诉用户集群是空的 —— 这也是一种假数据。
// 另外覆盖命令下发失败的状态码 → 人话映射（语义对齐 docs/design.md 的 REST 错误约定）。
import { screen } from '@testing-library/react'
import { Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import DeviceDetail from '@/pages/DeviceDetail'
import Devices from '@/pages/Devices'
import EdgeDetail from '@/pages/EdgeDetail'
import Edges from '@/pages/Edges'
import { ApiError } from '@/lib/api'
import { commandErrorCopy } from '@/lib/format'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import { makeDeviceView } from '@/test/fixtures'

/** 只有 healthz 通，其余全部 500 */
function failing(status = 500) {
  return installFetch((url) => (url === '/healthz'
    ? stubResponse(200, { ok: true, version: 'v0.1.0', uptime_s: 1, devices_online: 0, devices_total: 0, edges_online: 0 })
    : stubResponse(status, { error: 'boom' })))
}

beforeEach(() => { resetStores() })

describe('列表页：失败态不冒充空态', () => {
  it('Devices：/api/devices 500 → 加载失败 + 重试，不说「还没有设备接入」', async () => {
    failing()
    renderWithProviders(<Devices />)
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('设备列表加载失败')).toBeInTheDocument()
    expect(screen.getByText(/这不代表没有设备接入/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /重试/ })).toBeInTheDocument()
    expect(screen.queryByText('还没有设备接入')).not.toBeInTheDocument()
  })

  it('Edges：/api/edges 500 → 加载失败，不说「没有边缘节点」', async () => {
    failing()
    renderWithProviders(<Edges />)
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('边缘节点列表加载失败')).toBeInTheDocument()
    expect(screen.queryByText('没有边缘节点')).not.toBeInTheDocument()
  })

  it('401（会话失效）同样是失败态，不是空态', async () => {
    failing(401)
    renderWithProviders(<Devices />)
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('设备列表加载失败')).toBeInTheDocument()
    expect(screen.queryByText('还没有设备接入')).not.toBeInTheDocument()
  })
})

describe('详情页：失败态不冒充「不存在」', () => {
  it('DeviceDetail：接口 500 → 「设备信息加载失败」，不是「设备未注册」', async () => {
    failing()
    renderWithProviders(
      <Routes><Route path="/devices/:edgeId/:deviceId" element={<DeviceDetail />} /></Routes>,
      '/devices/edge-1/dev-9',
    )
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('设备信息加载失败')).toBeInTheDocument()
    expect(screen.getByText(/这不代表设备不存在/)).toBeInTheDocument()
    expect(screen.queryByText('设备未注册')).not.toBeInTheDocument()
  })

  it('DeviceDetail：接口正常但设备确实不存在 → 仍是「设备未注册」空态', async () => {
    installFetch((url) => (url === '/api/devices/edge-x/dev-x'
      ? stubResponse(404, { error: 'not found' }) : stubResponse(404, {})))
    renderWithProviders(
      <Routes><Route path="/devices/:edgeId/:deviceId" element={<DeviceDetail />} /></Routes>,
      '/devices/edge-x/dev-x',
    )
    // 404 属于「查过了、没有」：走空态而不是错误态
    expect(await screen.findByText('设备未注册')).toBeInTheDocument()
  })

  it('EdgeDetail：接口 500 → 「边缘节点信息加载失败」，不是「不存在」', async () => {
    failing()
    renderWithProviders(
      <Routes><Route path="/edges/:edgeId" element={<EdgeDetail />} /></Routes>,
      '/edges/edge-1',
    )
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('边缘节点信息加载失败')).toBeInTheDocument()
    expect(screen.queryByText('边缘节点不存在')).not.toBeInTheDocument()
  })
})

describe('命令下发失败 → 人话（docs/design.md REST 错误约定）', () => {
  const cases: [number, RegExp][] = [
    [400, /白名单|参数/],
    [401, /登录已失效/],
    [403, /权限不足/],
    [404, /设备不存在/],
    [409, /边缘节点离线/],
    [429, /频繁/],
    [503, /存储不可用|队列已满/],
    [500, /HTTP 500/],
  ]
  for (const [status, re] of cases) {
    it(`${status} → 说明可执行的下一步，不复述服务端原文`, () => {
      const copy = commandErrorCopy(new ApiError(status, 'viewer 只读'))
      expect(copy).toMatch(re)
      expect(copy).not.toContain('viewer 只读')
    })
  }

  it('429 带 Retry-After 时报秒数，不带时不编造', () => {
    expect(commandErrorCopy(new ApiError(429, 'x', 12))).toMatch(/12 秒/)
    expect(commandErrorCopy(new ApiError(429, 'x'))).not.toMatch(/\d+ 秒/)
  })

  it('网络不可达 → 说明是连接问题', () => {
    expect(commandErrorCopy(new TypeError('Failed to fetch'))).toMatch(/Failed to fetch|无法连接/)
    expect(commandErrorCopy(undefined)).toMatch(/无法连接 server/)
  })
})

describe('设备存在时正常渲染（失败态改造没有伤到正常路径）', () => {
  it('Devices 拿到数据就渲染列表，不出现错误态', async () => {
    installFetch((url) => {
      if (url === '/api/devices') return stubResponse(200, { devices: [makeDeviceView()] })
      return stubResponse(404, {})
    })
    renderWithProviders(<Devices />)
    expect(await screen.findByText('演示设备')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})