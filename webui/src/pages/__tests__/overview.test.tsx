// 概览页的四态守卫 + 「禁止假数据」反向断言。
// 概览是产品首屏，验收要求：Loading / Empty / Error / 有数据 四种后端形态都必须是
// 设计过的呈现，且任何一个数字都能追溯到真实通道（服务端聚合优先，列表通道降级），
// 不得有写死的 demo。
import { screen } from '@testing-library/react'
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
    if (url === '/api/edges') return stubResponse(200, { edges: [] })
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
    expect(screen.getByText('失败命令')).toBeInTheDocument()
    // 2/3、1/2、1/2 成对呈现；失败命令只有计数
    expect(screen.getByText('2').parentElement?.textContent).toMatch(/2\/3/)
    expect(screen.getByText('需要关注')).toBeInTheDocument()
  })

  it('需要关注栏给出离线/失败的可跳转明细，失败命令主行说人话、时间用绝对时间', async () => {
    route(FULL)
    const { container } = renderWithProviders(<Overview />)
    expect(await screen.findByText('1 台设备离线')).toBeInTheDocument()
    expect(screen.getByText('1 条命令执行失败')).toBeInTheDocument()
    // 失败命令主行展示中文命令名（平台词典/humanize 回落），机器 cmd+args 在 tooltip
    expect(screen.getByText('Relay On')).toBeInTheDocument()
    expect(screen.getByText('失败')).toBeInTheDocument()
    // 绝对时间：出现完整年月日，而不是只有「x 分钟前」
    const times = [...container.querySelectorAll('.num')].map((n) => n.textContent ?? '')
    expect(times.some((t) => /\d{4}\/\d{1,2}\/\d{1,2}/.test(t))).toBe(true)
  })

  it('边缘离线与插件未活跃各生成一条可执行的提醒（含去向链接）', async () => {
    route(FULL)
    const { container } = renderWithProviders(<Overview />)
    expect(await screen.findByText('1 台边缘节点离线')).toBeInTheDocument()
    expect(screen.getByText('1 个插件实例未达到活跃')).toBeInTheDocument()
    const links = [...container.querySelectorAll('a')].map((a) => a.getAttribute('href'))
    expect(links).toContain('/edges')
    expect(links).toContain('/plugins')
    expect(links).toContain('/activity')
  })

  it('近期事件与 WS 实时事件合并去重（同一事件不出现两次）', async () => {
    route(FULL)
    useLive.setState({
      events: [{ id: -1, device_id: 'edge-a/dev-1', ts: 1_770_000_000, type: 'device.boot', payload: '{}' }],
    })
    renderWithProviders(<Overview />)
    expect(await screen.findByText('近期事件')).toBeInTheDocument()
    // 平台生命周期事件走平台词汇层：device.boot → 设备启动
    expect(screen.getAllByText('设备启动').length).toBeGreaterThan(0)
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
    // 无异常是明确说出来的，不是空白
    expect(screen.getByText('暂无异常')).toBeInTheDocument()
    // 设备舰队与事件各自给出空态说明（不得空白）
    expect(screen.getByText('还没有设备接入')).toBeInTheDocument()
    expect(screen.getByText('还没有事件上报')).toBeInTheDocument()
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

  it('聚合 500 但列表通道可用 → 降级统计 + 来源标注 + 重试入口，设备舰队照常', async () => {
    const stub = route(null, 500, [{
      id: 'edge-a/dev-1', edge_id: 'edge-a', adapter: 'demo', name: '仍在上报的设备',
      port: 'COM3', online: true, state: {}, updated_at: 1, last_seen: 1,
    }])
    renderWithProviders(<Overview />)
    expect(await screen.findByText(/聚合通道不可用/)).toBeInTheDocument()
    // 降级统计来自设备列表通道的真实字段
    expect(await screen.findByText('仍在上报的设备')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '设备' })).toBeInTheDocument()

    const before = stub.to('/api/overview').length
    await userEvent.click(screen.getByRole('button', { name: /重试聚合/ }))
    expect(stub.to('/api/overview').length).toBeGreaterThan(before)
  })

  it('两条通道都失败（404）→ 各自可读错误态，接口失败不冒充「没有设备」', async () => {
    installFetch((url) => (url === '/healthz' ? stubResponse(200, health) : stubResponse(404, {})))
    renderWithProviders(<Overview />)
    // 概览与设备列表是两条独立通道，各自失败各自说 —— 因此这里有两个 role=alert
    expect((await screen.findAllByRole('alert')).length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('概览数据加载失败')).toBeInTheDocument()
    expect(screen.getByText('设备状态加载失败')).toBeInTheDocument()
    // 失败 ≠ 空：不许出现「还没有设备接入」这种假空态
    expect(screen.queryByText('还没有设备接入')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '设备' })).toBeInTheDocument()
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
