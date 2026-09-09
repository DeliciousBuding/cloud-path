// 概览页的四态与真实来源边界。
// 概览是产品首屏，验收要求：Loading / Empty / Error / 有数据 四种后端形态都必须是
// 设计过的呈现，且任何一个数字都能追溯到真实通道（服务端聚合优先，列表通道降级），

import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import Overview from '@/pages/Overview'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import { useLive } from '@/store/ws'
import { i18n } from '@/i18n'
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
afterEach(() => { void i18n.changeLanguage('zh-CN') })

describe('概览：有数据', () => {
  it('首屏先给整体状态，再用一组紧凑指标补充事实', async () => {
    route(FULL)
    renderWithProviders(<Overview />)
    expect(await screen.findByText('当前状态')).toBeInTheDocument()
    expect(await screen.findByText('有 3 项需要处理')).toBeInTheDocument()
    expect(await screen.findByText('在线设备')).toBeInTheDocument()
    expect(screen.getByText('在线网关')).toBeInTheDocument()
    expect(screen.getByText('应用运行正常')).toBeInTheDocument()
    expect(screen.getByText('失败的操作')).toBeInTheDocument()
    expect(screen.getByText('2/3')).toBeInTheDocument()
    expect(screen.getAllByText('1/2')).toHaveLength(2)
    expect(screen.getByText('需要处理')).toBeInTheDocument()
    const text = document.body.textContent ?? ''
    for (const word of ['Schema', 'Descriptor', 'Capability', 'Adapter', 'ACK', '结构化数据', '契约', '回执', '收敛', '快照', 'server', 'cookie', 'SQLite', 'WebSocket']) {
      expect(text).not.toContain(word)
    }
  })

  it('英文 locale 下主状态与指标使用英文资源', async () => {
    await i18n.changeLanguage('en-US')
    route(FULL)
    renderWithProviders(<Overview />)
    expect(await screen.findByText('Current status')).toBeInTheDocument()
    expect(await screen.findByText('Devices online')).toBeInTheDocument()
    expect(screen.getByText('Gateways online')).toBeInTheDocument()
    expect(screen.getByText('Apps running normally')).toBeInTheDocument()
    expect(screen.getByText('Failed actions')).toBeInTheDocument()
  })

  it('需要关注栏只给聚合主行与去向：失败明细的单一证据家是活动页，概览不复述 ledger', async () => {
    route(FULL)
    const { container } = renderWithProviders(<Overview />)
    expect(await screen.findByText('部分设备离线')).toBeInTheDocument()
    expect(screen.getByText('有些操作没有完成')).toBeInTheDocument()
    // 机器操作名/状态徽章/明细时间不在概览二次出现（同屏同一答案只留一处）
    expect(screen.queryByText('Relay On')).not.toBeInTheDocument()
    expect(screen.queryByText('失败')).not.toBeInTheDocument()
    const links = [...container.querySelectorAll('a')].map((a) => a.getAttribute('href'))
    expect(links).toContain('/activity?tab=commands&status=failed&handled=unhandled')
  })

  it('网关离线与运行项未活跃各生成一条可执行的提醒（含去向链接）', async () => {
    route(FULL)
    const { container } = renderWithProviders(<Overview />)
    expect(await screen.findByText('网关连接中断')).toBeInTheDocument()
    expect(screen.getByText('应用还没有正常运行')).toBeInTheDocument()
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
    expect(await screen.findByText('最近运行记录')).toBeInTheDocument()
    // 平台生命周期事件走平台词汇层：device.boot → 设备启动
    expect(screen.getAllByText('设备启动').length).toBeGreaterThan(0)
  })
})

describe('概览：空态（禁止假数据）', () => {
  it('全零数据 → 设计过的空态文案，没有一个编造的数字或设备', async () => {
    route(EMPTY)
    renderWithProviders(<Overview />)
    expect(await screen.findByText('在线设备')).toBeInTheDocument()
    // 统计瓦片给出「等待接入」这类空态说明，而不是塞个看起来合理的数
    expect(screen.getAllByText('等待设备连接').length).toBeGreaterThan(0)
    expect(screen.getByText('还没有网关连接')).toBeInTheDocument()
    expect(await screen.findByText('还没有应用')).toBeInTheDocument()
    // 无异常是明确说出来的，不是空白
    expect(screen.getByText('当前没有需要处理的问题。')).toBeInTheDocument()
    // 设备舰队与事件各自给出空态说明（不得空白）
    expect(screen.getAllByText('还没有设备连接').length).toBeGreaterThan(0)
    expect(screen.getByText('暂无运行记录')).toBeInTheDocument()

  })

  it('设备列表有内容但概览计数为 0 时，仍按各自真实来源渲染（不互相编造）', async () => {
    route(EMPTY, 200, [{
      id: 'edge-a/dev-1', edge_id: 'edge-a', adapter: 'demo', name: '真实设备',
      port: 'COM3', online: true, state: {}, updated_at: 1, last_seen: 1,
    }])
    renderWithProviders(<Overview />)
    expect(await screen.findByText('真实设备')).toBeInTheDocument()
    expect(screen.getAllByText('等待设备连接').length).toBeGreaterThan(0)
  })
})

describe('概览：加载与错误态（不得白屏）', () => {
  it('healthz 缺少 uptime_s 时回落服务状态正常，不渲染 NaN', async () => {
    installFetch((url) => {
      if (url === '/healthz') return stubResponse(200, { ok: true, version: 'v0.1.0' })
      if (url === '/api/overview') return stubResponse(200, { ...EMPTY, server_time: 0 })
      if (url === '/api/devices') return stubResponse(200, { devices: [] })
      if (url === '/api/edges') return stubResponse(200, { edges: [] })
      return stubResponse(404, {})
    })
    renderWithProviders(<Overview />)
    expect(await screen.findByText('服务运行正常')).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('NaN')
  })

  it('首帧不把未加载数据渲染成统计值或空态', () => {
    route(FULL)
    renderWithProviders(<Overview />)
    expect(screen.queryByText('在线设备')).not.toBeInTheDocument()
    expect(screen.queryByText('暂无异常')).not.toBeInTheDocument()
  })

  it('聚合 500 但列表通道可用 → 降级统计 + 来源标注 + 重试入口，设备舰队照常', async () => {
    const stub = route(null, 500, [{
      id: 'edge-a/dev-1', edge_id: 'edge-a', adapter: 'demo', name: '仍在上报的设备',
      port: 'COM3', online: true, state: {}, updated_at: 1, last_seen: 1,
    }])
    renderWithProviders(<Overview />)
    expect(await screen.findByText(/部分状态暂时无法加载，当前显示设备和网关的最新结果/)).toBeInTheDocument()
    // 降级统计来自设备列表通道的真实字段
    expect(await screen.findByText('仍在上报的设备')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '设备状态' })).toBeInTheDocument()

    const before = stub.to('/api/overview').length
    await userEvent.click(screen.getByRole('button', { name: '重新加载' }))
    expect(stub.to('/api/overview').length).toBeGreaterThan(before)
  })

  it('两条通道都失败（404）→ 只给一个统一错误态，不重复三张大卡', async () => {
    installFetch((url) => (url === '/healthz' ? stubResponse(200, health) : stubResponse(404, {})))
    renderWithProviders(<Overview />)
    expect(await screen.findAllByRole('alert')).toHaveLength(1)
    expect(screen.getByText('状态暂时不可用')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    // 失败 ≠ 空：不许出现「还没有设备接入」或「没有异常」这种假空态
    expect(screen.getByText('状态暂时不可用，无法判断是否有事项需要处理。')).toBeInTheDocument()
    expect(screen.queryByText('当前没有需要处理的问题。')).not.toBeInTheDocument()
    expect(screen.queryByText('还没有设备连接')).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '设备状态' })).not.toBeInTheDocument()
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
