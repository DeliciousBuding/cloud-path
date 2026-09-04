// 390px 回归守卫：后端给的标识符（edge_id / 适配器名 / 设备名 / 命令名 / 版本号）长度不可控，
// 承载它们的文本容器必须自己截断（truncate/break）或待在局部滚动容器里，
// 否则一个长名字就能把 body 顶出横向滚动。用 64 个不可断字符做最坏情况。
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import Activity from '@/pages/Activity'
import DeviceDetail from '@/pages/DeviceDetail'
import Devices from '@/pages/Devices'
import EdgeDetail from '@/pages/EdgeDetail'
import Edges from '@/pages/Edges'
import Overview from '@/pages/Overview'
import PluginInstanceDetail from '@/pages/PluginInstanceDetail'
import Pillbox from '@/pages/Pillbox'
import Plugins from '@/pages/Plugins'
import Settings from '@/pages/Settings'
import { installFetch, stubResponse } from '@/test/http'
import { LONG, expectContained, expectNoInlinePixelWidth } from '@/test/narrow'
import { renderWithProviders, resetStores } from '@/test/render'
import type { DeviceView, OverviewView, PluginCatalogView, PluginInstanceView } from '@/lib/types'

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
/* ================================================================== *
 * v0.1 收口新增页面：Overview / Plugins / Activity / DeviceDetail / EdgeDetail
 * 同一把尺子：后端可控的长标识符必须自己截断，或待在局部滚动容器里。
 * ================================================================== */

const longInstance: PluginInstanceView = {
  id: `${LONG}/${LONG}`, tenant_id: 1, edge_id: LONG,
  desired: {
    instance_id: LONG, plugin_id: LONG, version: LONG, enabled: true, isolation: LONG,
    config: { [LONG]: LONG, pass: 'secret://x' }, secret_refs: [LONG],
    revision: 42, updated_at: 1_770_000_000,
  },
  has_observed: true,
  observed: {
    state: LONG, health: LONG, version: LONG, detail: LONG,
    restart_count: 3, reported_at: 1_770_000_010,
  },
  edge_online: true, desired_revision: 42, applied_revision: 41,
  drift: true, stale: true, last_ack_at: 1_770_000_011,
}

const longCatalog: PluginCatalogView = {
  id: LONG, kind: LONG, version: LONG, source: LONG, digest: `sha256:${LONG}`,
  verified: false, compatibility: LONG, protocol: 1,
  permissions: { hardware: [LONG], network: [LONG], filesystem: [LONG], secrets: [LONG] },
  contributes: {
    drivers: [{ id: LONG, title: LONG, discovery: LONG }],
    applications: [{ id: LONG, title: LONG }],
    connectors: [{ id: LONG, title: LONG, direction: LONG, host: LONG }],
  },
}

const longOverview: OverviewView = {
  devices_online: 1, devices_total: 2, edges_online: 1, edges_total: 2,
  plugins_active: 1, plugins_desired: 2, commands_failed: 1,
  recent_events: [{ id: 1, device_id: `${LONG}/${LONG}`, ts: 1_770_000_000, type: LONG, payload: '{}' }],
  offline_devices: [{ ...longDevice, id: `${LONG}/${LONG}`, name: LONG }],
  failed_commands: [{
    id: 2, device_id: `${LONG}/${LONG}`, cmd: LONG, args: LONG, status: 'failed',
    created_at: 1_770_000_000, acked_at: 0, result: LONG,
  }],
  server_time: 1_770_000_500,
}

describe('Overview 页', () => {
  it('离线设备名 / 边缘 ID / 命令与参数 / 事件类型都收口', async () => {
    installFetch((url) => {
      if (url.startsWith('/api/overview')) return stubResponse(200, longOverview)
      if (url === '/healthz') return stubResponse(200, health)
      if (url === '/api/devices') return stubResponse(200, { devices: [longDevice] })
      return stubResponse(404, {})
    })
    renderWithProviders(<Overview />)
    await screen.findAllByText(LONG)
    expectContained(LONG)
    expectNoInlinePixelWidth()
  })
})

describe('Plugins 页（三分都要过）', () => {
  function pluginFetch() {
    return installFetch((url) => {
      if (url === '/api/plugins') return stubResponse(200, { plugins: [longCatalog] })
      if (url === '/api/plugin-instances') return stubResponse(200, { instances: [longInstance] })
      if (url === '/api/edges') return stubResponse(200, { edges: [{ edge_id: LONG, online: true, version: LONG, devices: [], connected_at: 1 }] })
      return stubResponse(404, {})
    })
  }

  it('目录：插件 ID / 类型 / 版本 / 摘要 / 权限项 / 贡献都收口', async () => {
    pluginFetch()
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await screen.findAllByText(LONG)
    expectContained(LONG)
    expectNoInlinePixelWidth()
    // source（可能是本机绝对路径）不渲染，这条由 plugins.test.tsx 用真实路径断言
    await user.click(screen.getByRole('tab', { name: /已安装/ }))
    expectContained(LONG)
    await user.click(screen.getByRole('tab', { name: /实例/ }))
    expectContained(LONG)
    expectNoInlinePixelWidth()
  })

  it('实例详情：期望/实际双栏在 390px 仍并排且各自收口', async () => {
    pluginFetch()
    renderWithProviders(
      <Routes><Route path="/plugins/:id" element={<PluginInstanceDetail />} /></Routes>,
      `/plugins/${encodeURIComponent(longInstance.id)}`,
    )
    await screen.findAllByText(LONG)
    expectContained(LONG)
    expectNoInlinePixelWidth()
    // 双栏并排是这个分区的价值所在：390px 也不许塌成一栏
    const grid = screen.getByText('期望态 · Desired').closest('.grid') as HTMLElement
    expect(grid.className).toContain('grid-cols-2')
  })
})

describe('Activity 页', () => {
  it('设备键 / 命令 / 参数 / 结果 / 事件类型都收口（两个分区）', async () => {
    installFetch((url) => {
      if (url.startsWith('/api/events')) {
        return stubResponse(200, { events: [{ id: 1, device_id: `${LONG}/${LONG}`, ts: 1, type: LONG, payload: '{}' }] })
      }
      if (url.startsWith('/api/commands')) {
        return stubResponse(200, { commands: [longOverview.failed_commands[0]] })
      }
      if (url === '/api/devices') return stubResponse(200, { devices: [longDevice] })
      if (url === '/api/edges') return stubResponse(200, { edges: [{ edge_id: LONG, online: true, version: LONG, devices: [], connected_at: 1 }] })
      return stubResponse(404, {})
    })
    const user = userEvent.setup()
    renderWithProviders(<Activity />)
    await screen.findAllByText(LONG)
    expectContained(LONG)
    expectNoInlinePixelWidth()

    await user.click(screen.getByRole('button', { name: /命令历史/ }))
    await screen.findAllByText(LONG)
    expectContained(LONG)
    expectNoInlinePixelWidth()
  })
})

describe('DeviceDetail / EdgeDetail 页', () => {
  it('设备详情六个分区都不被长标识符撑宽', async () => {
    installFetch((url) => {
      if (url === `/api/devices/${LONG}/${LONG}`) return stubResponse(200, longDevice)
      if (url === '/api/adapters') return stubResponse(200, { adapters: [{ name: LONG, commands: [LONG] }] })
      if (url.startsWith('/api/events')) return stubResponse(200, { events: [] })
      return stubResponse(404, {})
    })
    const user = userEvent.setup()
    renderWithProviders(
      <Routes><Route path="/devices/:edgeId/:deviceId" element={<DeviceDetail />} /></Routes>,
      `/devices/${LONG}/${LONG}`,
    )
    await screen.findAllByText(LONG)
    expectContained(LONG)
    expectNoInlinePixelWidth()
    for (const tab of ['实时状态', '控制', '事件', '能力', '诊断']) {
      await user.click(screen.getByRole('tab', { name: new RegExp(tab) }))
      expectNoInlinePixelWidth()
    }
  })

  it('边缘详情：节点 ID / 版本 / 设备名都收口', async () => {
    installFetch((url) => {
      if (url === '/api/edges') {
        return stubResponse(200, { edges: [{ edge_id: LONG, online: false, version: LONG, devices: [longDevice.id], connected_at: 1 }] })
      }
      if (url === '/api/devices') return stubResponse(200, { devices: [longDevice] })
      if (url.startsWith('/api/events')) return stubResponse(200, { events: [] })
      return stubResponse(404, {})
    })
    renderWithProviders(
      <Routes><Route path="/edges/:edgeId" element={<EdgeDetail />} /></Routes>,
      `/edges/${LONG}`,
    )
    await screen.findAllByText(LONG)
    expectContained(LONG)
    expectNoInlinePixelWidth()
  })
})
/** 药盒控制面板：长设备名/命令名/实体名在 390px 下必须收口 */
describe('Pillbox 页', () => {
  const longEntity = {
    entity_id: LONG, unique_key: LONG, name: LONG, category: 'sensor' as const,
    capabilities: [LONG],
    observations: { state: { capability: LONG, property: 'state', value: LONG, quality: 'good' as const } },
  }
  const longPillboxDescriptor = {
    device_id: `edge-1/${LONG}`, external_id: LONG, status: 'online' as const,
    entities: [longEntity],
  }

  it('药格卡片 / 命令 / 状态横幅都被长标识符收口', async () => {
    installFetch((url) => {
      if (url === '/api/devices') return stubResponse(200, { devices: [longDevice] })
      if (url === '/api/adapters') return stubResponse(200, { adapters: [{ name: LONG, commands: [LONG] }] })
      if (url === '/api/descriptors') return stubResponse(404, {})
      if (url.endsWith('/descriptor')) return stubResponse(200, longPillboxDescriptor)
      if (url === '/api/capabilities') return stubResponse(404, {})
      if (url.startsWith('/api/events?')) return stubResponse(200, { events: [] })
      if (url.startsWith('/api/commands?')) return stubResponse(200, { commands: [] })
      return stubResponse(404, {})
    })
    renderWithProviders(<Pillbox />)
    await screen.findAllByText(LONG)
    expectContained(LONG)
    expectNoInlinePixelWidth()
  })
})

