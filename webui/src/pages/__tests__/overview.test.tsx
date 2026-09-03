// 概览页的四态守卫 + 「禁止假数据」反向断言。
// 概览是产品首屏，验收要求：Loading / Empty / Error / 有数据 四种后端形态都必须是
// 设计过的呈现，且任何一个数字都能追溯到 GET /api/overview 的响应，不得有写死的 demo。
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import Overview from '@/pages/Overview'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import { useLive } from '@/store/ws'
import { normalizeOverview, overviewAlerts, overviewStats } from '@/lib/overview'
import type { OverviewView } from '@/lib/types'

const health = { ok: true, version: 'v0.1.0', uptime_s: 600, devices_online: 0, devices_total: 0, edges_online: 0 }

const FULL: OverviewView = {
  devices_online: 2, devices_total: 3,
  edges_online: 1, edges_total: 2,
  plugins_active: 1, plugins_desired: 2,
  commands_failed: 1,
  recent_events: [
    { id: 11, device_id: 'edge-a/dev-1', ts: 1_770_000_000, type: 'device.boot', payload: '{}' },
  ],
  offline_devices: [{
    id: 'edge-b/dev-9', edge_id: 'edge-b', adapter: 'demo', name: '走廊药盒', port: 'COM7',
    online: false, state: {}, updated_at: 1_769_990_000, last_seen: 1_769_990_000,
  }],
  failed_commands: [{
    id: 77, device_id: 'edge-a/dev-1', cmd: 'relay_on', args: 'A1',
    status: 'failed', created_at: 1_769_995_000, acked_at: 1_769_995_010, result: 'device busy',
  }],
  server_time: 1_770_000_500,
}

const EMPTY: OverviewView = {
  devices_online: 0, devices_total: 0, edges_online: 0, edges_total: 0,
  plugins_active: 0, plugins_desired: 0, commands_failed: 0,
  recent_events: [], offline_devices: [], failed_commands: [], server_time: 1_770_000_500,
}

/** overview 有数据、设备列表独立通道也有数据（贴近真实首屏） */
function route(overview: OverviewView | null, status = 200, devices: unknown[] = []) {
  return installFetch((url) => {
    if (url.startsWith('/api/overview')) {
      return overview === null ? stubResponse(status, { error: 'boom' }) : stubResponse(200, overview)
    }
    if (url === '/healthz') return stubResponse(200, health)
    if (url === '/api/devices') return stubResponse(200, { devices })
    return stubResponse(404, { error: 'not found' })
  })
}

beforeEach(() => { resetStores() })

describe('概览：有数据', () => {
  it('四个统计瓦片的数字全部来自服务端聚合（在线/总数成对呈现）', async () => {
    route(FULL)
    renderWithProviders(<Overview />)
    expect(await screen.findByText('在线设备')).toBeInTheDocument()
    expect(screen.getByText('在线边缘')).toBeInTheDocument()
    expect(screen.getByText('活跃插件')).toBeInTheDocument()
    // 「失败命令」同时是统计瓦片标签与明细面板标题，两处都必须在\n    expect(screen.getAllByText('失败命令').length).toBeGreaterThanOrEqual(2)
    // 2/3、1/2、1/2 成对呈现；失败命令只有计数
    expect(screen.getByText('2').parentElement?.textContent).toMatch(/2\/3/)
    expect(screen.getByText('需要关注')).toBeInTheDocument()
  })

  it('离线设备与失败命令给出可跳转明细，时间用绝对时间', async () => {
    route(FULL)
    renderWithProviders(<Overview />)
    expect(await screen.findByText('走廊药盒')).toBeInTheDocument()
    expect(screen.getByText(/edge-b · 最后见/)).toBeInTheDocument()
    // 绝对时间：出现完整年月日，而不是只有「x 分钟前」
    expect(screen.getByText(/最后见/).textContent).toMatch(/\d{4}\/\d{1,2}\/\d{1,2}/)
    expect(screen.getByText(/relay_on A1/)).toBeInTheDocument()
    expect(screen.getByText('失败')).toBeInTheDocument()
  })

  it('边缘离线与插件未活跃各生成一条可执行的提醒（含去向链接）', async () => {
    route(FULL)
    const { container } = renderWithProviders(<Overview />)
    expect(await screen.findByText('1 台边缘节点离线')).toBeInTheDocument()
    expect(screen.getByText('1 个插件实例未达到活跃')).toBeInTheDocument()
    const links = [...container.querySelectorAll('a')].map((a) => a.getAttribute('href'))
    expect(links).toContain('/edges')
    expect(links).toContain('/plugins')
    // 有专属面板的两类不重复出现在提醒行里
    expect(screen.queryByText('1 台设备离线')).not.toBeInTheDocument()
    expect(screen.queryByText('1 条命令执行失败')).not.toBeInTheDocument()
  })

  it('近期事件与 WS 实时事件合并去重（同一事件不出现两次）', async () => {
    route(FULL)
    useLive.setState({
      status: 'open',
      events: [{ id: -1, device_id: 'edge-a/dev-1', ts: 1_770_000_000, type: 'device.boot', payload: '{}' }],
    })
    renderWithProviders(<Overview />)
    expect(await screen.findByText('近期事件')).toBeInTheDocument()
    expect(screen.getAllByText(/Device Boot|device\.boot/i).length).toBeGreaterThan(0)
  })
})

describe('概览：空态（禁止假数据）', () => {
  it('全零数据 → 设计过的空态文案，没有一个编造的数字或设备', async () => {
    route(EMPTY)
    const { container } = renderWithProviders(<Overview />)
    expect(await screen.findByText('在线设备')).toBeInTheDocument()
    // 统计瓦片给出「等待接入」这类空态说明，而不是塞个看起来合理的数
    expect(screen.getByText('等待边缘节点接入设备')).toBeInTheDocument()
    expect(screen.getByText('尚未有边缘节点注册')).toBeInTheDocument()
    expect(screen.getByText('还没有插件实例')).toBeInTheDocument()
    // 离线设备/失败命令都是「全部正常」而不是空白
    expect(screen.getByText('全部在线')).toBeInTheDocument()
    // 离线设备面板与设备栅格各自给出空态说明（两处都不得空白）\n    expect(screen.getAllByText('还没有设备接入').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('还没有事件上报')).toBeInTheDocument()
    // 没有数据就不该出现「需要关注」
    expect(screen.queryByText('需要关注')).not.toBeInTheDocument()
    // 反向断言：不得写死任何示例设备/示例数字冒充真实数据
    const text = container.textContent ?? ''
    for (const fake of ['demo-device', '示例设备', '客厅', '药盒', '1234']) {
      expect(text, `概览空态里出现了疑似写死数据 ${fake}`).not.toContain(fake)
    }
  })

  it('设备列表有内容但概览计数为 0 时，仍按各自真实来源渲染（不互相编造）', async () => {
    route(EMPTY, 200, [{
      id: 'edge-a/dev-1', edge_id: 'edge-a', adapter: 'demo', name: '真实设备',
      port: 'COM3', online: true, state: {}, updated_at: 1, last_seen: 1,
    }])
    renderWithProviders(<Overview />)
    expect(await screen.findByText('真实设备')).toBeInTheDocument()
    expect(screen.getByText('等待边缘节点接入设备')).toBeInTheDocument()
  })
})

describe('概览：加载与错误态（不得白屏）', () => {
  it('首帧是骨架，不是空白也不是 0', async () => {
    route(FULL)
    const { container } = renderWithProviders(<Overview />)
    expect(container.querySelectorAll('.skeleton').length).toBeGreaterThan(0)
  })

  it('500 → 可读错误态 + 重试按钮，且实时设备区仍独立可用', async () => {
    const stub = route(null, 500, [{
      id: 'edge-a/dev-1', edge_id: 'edge-a', adapter: 'demo', name: '仍在上报的设备',
      port: 'COM3', online: true, state: {}, updated_at: 1, last_seen: 1,
    }])
    renderWithProviders(<Overview />)
    const alert = await screen.findByRole('alert')
    expect(within(alert).getByText('概览数据加载失败')).toBeInTheDocument()
    expect(within(alert).getByText(/仍来自独立通道/)).toBeInTheDocument()
    // 概览挂了不等于整页挂：设备实时状态照常渲染
    expect(await screen.findByText('仍在上报的设备')).toBeInTheDocument()
    expect(screen.getByText('实时设备状态')).toBeInTheDocument()

    const before = stub.to('/api/overview').length
    await userEvent.click(within(alert).getByRole('button', { name: /重试/ }))
    expect(stub.to('/api/overview').length).toBeGreaterThan(before)
  })

  it('端点缺席（404，server lane 未合并）→ 错误态而不是崩溃', async () => {
    installFetch((url) => (url === '/healthz' ? stubResponse(200, health) : stubResponse(404, {})))
    renderWithProviders(<Overview />)
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('概览数据加载失败')).toBeInTheDocument()
    expect(screen.getByText('实时设备状态')).toBeInTheDocument()
  })

  it('响应体畸形（字段全缺）→ 归一化成安全空值，不抛未捕获异常', () => {
    const o = normalizeOverview({ devices_online: 'x', recent_events: null })
    expect(o.devices_online).toBe(0)
    expect(o.recent_events).toEqual([])
    expect(o.offline_devices).toEqual([])
    expect(normalizeOverview(null).devices_total).toBe(0)
    expect(overviewStats(o)).toHaveLength(4)
    expect(overviewAlerts(o)).toEqual([])
  })
})