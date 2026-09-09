// 设备控制面：命令集的唯一事实源是后端（Capability 声明 → Descriptor 扩展 →
// /api/adapters 白名单），前端**不得自建清单**。
//
// 测试只验证候选来自后端，以及空态和候选变化时的行为。
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import { CommandHistory } from '@/components/CommandHistory'
import DeviceDetail from '@/pages/DeviceDetail'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import { useAuth } from '@/store/auth'
import { makeDeviceView, catalogPayload, makeDescriptor } from '@/test/fixtures'

const KEY = 'edge-1/dev-9'
const ROUTE = '/devices/edge-1/dev-9'

function renderDetail(path = ROUTE) {
  return renderWithProviders(
    <Routes>
      <Route path="/devices/:edgeId/:deviceId" element={<DeviceDetail />} />
    </Routes>,
    path,
  )
}

interface Opts {
  adapters?: { name: string; commands: string[] }[]
  descriptor?: unknown
  capabilities?: unknown
  commands?: unknown[]
  device?: unknown
  edges?: unknown[]
}
function route(o: Opts = {}) {
  return installFetch((url) => {
    if (url === '/api/adapters') {
      return o.adapters ? stubResponse(200, { adapters: o.adapters }) : stubResponse(404, {})
    }
    if (url === `/api/devices/edge-1/dev-9`) return stubResponse(200, o.device ?? makeDeviceView())
    if (url === '/api/edges') return o.edges ? stubResponse(200, { edges: o.edges }) : stubResponse(404, {})
    if (url.endsWith('/descriptor')) {
      return o.descriptor ? stubResponse(200, o.descriptor) : stubResponse(404, {})
    }
    if (url === '/api/descriptors') return stubResponse(404, {})
    if (url === '/api/capabilities') {
      return o.capabilities ? stubResponse(200, o.capabilities) : stubResponse(404, {})
    }
    if (url.startsWith('/api/commands')) return stubResponse(200, { commands: o.commands ?? [] })
    if (url.startsWith('/api/devices/edge-1/dev-9/commands')) {
      return stubResponse(200, { id: 1, device_id: KEY, cmd: 'x', args: '', status: 'sent', created_at: 0, acked_at: 0, result: '' })
    }
    return stubResponse(404, {})
  })
}

async function gotoControls() {
  const user = userEvent.setup()
  await screen.findByRole('heading', { level: 1 })
  await user.click(screen.getByRole('tab', { name: /设备操作/ }))
  return user
}

beforeEach(() => {
  resetStores()
  useAuth.setState({ status: 'in', user: { id: 1, username: 'operator', name: '操作员', role: 'operator', tenant_id: 1, tenant_slug: 'default' } })
})

describe('命令集来自设备支持的操作', () => {
  it('适配器提供的命令逐条渲染成按钮，并放在高级：手动输入参数入口', async () => {
    route({ adapters: [{ name: 'demo', commands: ['raw', 'identify', 'query_state'] }] })
    renderDetail()
    await gotoControls()
    expect(await screen.findByText('高级：手动输入参数')).toBeInTheDocument()
    // 无 schema 的适配器命令走高级手动参数入口，候选来自白名单，不多不少
    const select = screen.getByRole('combobox', { name: '选择操作' })
    const options = within(select).getAllByRole('option').map((o) => o.textContent).filter((t) => t !== '选择操作')
    expect(options).toEqual(['原始命令', 'Identify', 'Query State'])
  })


  it('白名单变了，控件跟着变（证明不是写死的一张表）', async () => {
    route({ adapters: [{ name: 'demo', commands: ['alpha_only'] }] })
    renderDetail()
    await gotoControls()
    await screen.findByText('高级：手动输入参数')
    const select = screen.getByRole('combobox', { name: '选择操作' })
    expect(within(select).getAllByRole('option').map((o) => o.textContent)).toEqual(['选择操作', 'Alpha Only'])
  })

  it('没有可用操作 → 明确空态，不摆一排猜出来的按钮', async () => {
    route({})
    renderDetail()
    await gotoControls()
    expect(await screen.findByText('这台设备暂时没有可执行的操作')).toBeInTheDocument()
  })

  it('没有命令 → 同样走空态', async () => {
    route({ adapters: [{ name: 'demo', commands: [] }] })
    renderDetail()
    await gotoControls()
    expect(await screen.findByText('这台设备暂时没有可执行的操作')).toBeInTheDocument()
  })
})

describe('离线设备控制', () => {
  const edge = (online: boolean) => ({
    edge_id: 'edge-1', online, version: 'dev', devices: ['dev-9'], connected_at: 0,
  })

  it('设备离线时禁用操作并说明原因', async () => {
    route({ device: makeDeviceView({ online: false }), edges: [edge(true)], descriptor: makeDescriptor(), capabilities: catalogPayload })
    renderDetail()
    await gotoControls()
    expect(await screen.findByRole('status')).toHaveTextContent('设备当前离线')
    expect(screen.getByRole('button', { name: '闭合' })).toBeDisabled()
  })

  it('所属网关离线时也 fail-closed 禁用操作', async () => {
    route({ device: makeDeviceView({ online: true }), edges: [edge(false)], descriptor: makeDescriptor(), capabilities: catalogPayload })
    renderDetail()
    await gotoControls()
    expect(await screen.findByRole('status')).toHaveTextContent('所属网关当前离线')
    expect(screen.getByRole('button', { name: '闭合' })).toBeDisabled()
  })
})

describe('有设备能力声明时以声明为准', () => {
  it('设备说明 + 能力列表 → 操作来自声明，不显示内部术语', async () => {
    route({
      adapters: [{ name: 'demo', commands: ['raw'] }],
      descriptor: makeDescriptor(),
      capabilities: catalogPayload,
    })
    renderDetail()
    await gotoControls()
    expect(await screen.findByRole('heading', { level: 2, name: '设备操作' })).toBeInTheDocument()
    expect(screen.queryByText('Schema 声明')).not.toBeInTheDocument()
    expect(screen.queryByText('Descriptor')).not.toBeInTheDocument()
    expect(screen.queryByText('Capability')).not.toBeInTheDocument()
    const panel = screen.getByRole('heading', { level: 2, name: '设备操作' }).closest('section') as HTMLElement
    for (const declared of ['闭合', '断开', '恢复出厂']) {
      expect(within(panel).getByRole('button', { name: declared }), `声明动作 ${declared} 未渲染`).toBeInTheDocument()
    }
    expect(within(panel).getByRole('combobox', { name: '选择参数操作' })).toHaveValue('pulse')
    // 白名单命令不再另立入口（声明优先）
    expect(screen.queryByRole('combobox', { name: '选择操作' })).not.toBeInTheDocument()
  })

  it('危险声明动作带二次确认，文案逐字取自声明', async () => {
    const user = userEvent.setup()
    route({ descriptor: makeDescriptor(), capabilities: catalogPayload })
    renderDetail()
    await gotoControls()
    await user.click(await screen.findByRole('button', { name: '恢复出厂' }))
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('确认恢复出厂？设备侧配置将被清空。')
    expect(dialog).toHaveTextContent('演示设备')
    expect(dialog).not.toHaveTextContent(KEY)
    expect(within(dialog).getByRole('button', { name: '恢复出厂' })).toBeDisabled()
  })
})

describe('列表页的关键读数同样来自声明', () => {
  it('无声明 → 「等待同步」，不猜读数', async () => {
    const { default: Devices } = await import('@/pages/Devices')
    installFetch((url) => {
      if (url === '/api/devices') return stubResponse(200, { devices: [makeDeviceView()] })
      if (url === '/api/descriptors') return stubResponse(404, {})
      if (url === '/api/capabilities') return stubResponse(404, {})
      return stubResponse(404, {})
    })
    renderWithProviders(<Devices />)
    expect(await screen.findByText('读数等待同步')).toBeInTheDocument()
  })

  it('有 Descriptor → 读数取声明主观测（实体名 + 值），不再铺能力芯片墙', async () => {
    const { default: Devices } = await import('@/pages/Devices')
    installFetch((url) => {
      if (url === '/api/devices') return stubResponse(200, { devices: [makeDeviceView()] })
      if (url === '/api/descriptors') {
        return stubResponse(200, { descriptors: [makeDescriptor()], capabilities: catalogPayload.capabilities })
      }
      if (url === '/api/capabilities') return stubResponse(200, catalogPayload)
      return stubResponse(404, {})
    })
    renderWithProviders(<Devices />)
    // 主观测：温度探针 current=26.5（CATEGORY_ORDER 里 sensor 优先）
    expect(await screen.findByText('温度探针')).toBeInTheDocument()
    expect(screen.getByText('26.5 °C')).toBeInTheDocument()
    // 旧芯片墙文案彻底退出列表行
    expect(screen.queryByText('能力未知（未上报声明）')).not.toBeInTheDocument()
    expect(screen.queryByText('未声明能力')).not.toBeInTheDocument()
  })
})


describe('设备分区深链接', () => {
  it('未指定分区时默认进入设备操作，让操作优先于概览', async () => {
    route({ descriptor: makeDescriptor(), capabilities: catalogPayload })
    renderDetail(ROUTE)
    expect(await screen.findByRole('tab', { name: /设备操作/ })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('heading', { level: 2, name: '设备操作' })).toBeInTheDocument()
  })

  it('点击概览会显式进入概览，不被默认设备操作弹回', async () => {
    route({ descriptor: makeDescriptor(), capabilities: catalogPayload })
    renderDetail(ROUTE)
    const user = userEvent.setup()
    await screen.findByRole('heading', { level: 1 })
    await user.click(screen.getByRole('tab', { name: /概览/ }))
    expect(await screen.findByRole('tab', { name: /概览/ })).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByText('设备摘要')).toBeInTheDocument()
  })

  it('controls 查询参数直接打开正确设备的控制区', async () => {
    route({ adapters: [{ name: 'demo', commands: ['identify'] }] })
    renderDetail(ROUTE + '?tab=controls')
    expect(await screen.findByText('高级：手动输入参数')).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '选择操作' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: /设备操作/ })).toHaveAttribute('aria-selected', 'true')
  })
  it('未知分区回落概览，不显示错误控制区', async () => {
    route()
    renderDetail(ROUTE + '?tab=unknown')
    expect(await screen.findByRole('tab', { name: /概览/ })).toHaveAttribute('aria-selected', 'true')
  })
})

describe('操作记录', () => {
  it('失败给出人话原因与下一步，等待中的操作不会被误报为失败', async () => {
    route({
      descriptor: makeDescriptor(),
      capabilities: catalogPayload,
      commands: [
        {
          id: 51, device_id: KEY, cmd: 'pulse', args: '{"ms":100}', status: 'failed',
          created_at: 1_780_000_000, acked_at: 1_780_000_001, result: 'ERR_BUSY queue full; raw=0x05',
        },
        {
          id: 50, device_id: KEY, cmd: 'close', args: '', status: 'sent',
          created_at: 1_780_000_100, acked_at: 0, result: '',
        },
      ],
    })
    renderDetail(ROUTE + '?tab=events')

    expect(await screen.findByRole('heading', { level: 2, name: '操作记录' })).toBeInTheDocument()
    expect(await screen.findByText('设备正忙')).toBeInTheDocument()
    expect(screen.getByText(/等待设备空闲后重试/)).toBeInTheDocument()
    expect(screen.getByText('已发送，正在等待设备确认，请勿重复操作。')).toBeInTheDocument()
    expect(screen.queryByText('操作失败，请稍后重试')).not.toBeInTheDocument()

    const failedDetails = screen.getAllByText('技术详情')[0].closest('details') as HTMLElement
    expect(within(failedDetails).getByText('ERR_BUSY queue full; raw=0x05')).toBeInTheDocument()
  })

  it('超时状态使用设备响应超时与下一步，不落回通用失败文案', async () => {
    route({
      descriptor: makeDescriptor(),
      capabilities: catalogPayload,
      commands: [{
        id: 52, device_id: KEY, cmd: 'motor', args: '{"steps":4}', status: 'timeout',
        created_at: 1_780_000_000, acked_at: 1_780_000_015, result: 'device did not acknowledge command before deadline',
      }],
    })
    renderDetail(ROUTE + '?tab=events')

    expect(await screen.findByText('设备响应超时')).toBeInTheDocument()
    expect(screen.getByText(/确认设备在线且空闲后重试/)).toBeInTheDocument()
    expect(screen.queryByText('设备没有完成操作')).not.toBeInTheDocument()
  })

  it('失败记录可直接重试，并沿用原命令参数与权限边界', async () => {
    const user = userEvent.setup()
    const http = route({
      descriptor: makeDescriptor(),
      capabilities: catalogPayload,
      commands: [{
        id: 51, device_id: KEY, cmd: 'pulse', args: '{"ms":100}', status: 'failed',
        created_at: 1_780_000_000, acked_at: 1_780_000_001, result: 'busy',
      }],
    })
    renderDetail(ROUTE + '?tab=events')

    await user.click(await screen.findByRole('button', { name: '重试点动' }))
    expect([...http.calls].reverse().find((call) => call.method === 'POST')).toMatchObject({
      url: '/api/devices/edge-1/dev-9/commands',
      method: 'POST',
      body: { cmd: 'pulse', args: '{"ms":100}' },
    })
  })

  it('未传在线态时保持历史重试入口', async () => {
    route({
      commands: [{
        id: 53, device_id: KEY, cmd: 'pulse', args: '{"ms":100}', status: 'failed',
        created_at: 1_780_000_000, acked_at: 1_780_000_001, result: 'busy',
      }],
    })
    renderWithProviders(<CommandHistory deviceId={KEY} actions={[{ cmd: 'pulse', label: '点动' }]} />)
    expect(await screen.findByRole('button', { name: '重试点动' })).toBeInTheDocument()
  })

  it('设备离线时禁用历史重试并说明恢复条件', async () => {
    route({
      commands: [{
        id: 54, device_id: KEY, cmd: 'pulse', args: '{"ms":100}', status: 'failed',
        created_at: 1_780_000_000, acked_at: 1_780_000_001, result: 'busy',
      }],
    })
    renderWithProviders(<CommandHistory deviceId={KEY} online={false} actions={[{ cmd: 'pulse', label: '点动' }]} />)
    expect(await screen.findByText('设备恢复在线后可重试')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试点动' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '去设备操作中重试' })).not.toBeInTheDocument()
  })

  it('设备详情离线时也不显示历史重试入口', async () => {
    route({
      device: makeDeviceView({ online: false }),
      edges: [{ edge_id: 'edge-1', online: true, version: 'dev', devices: ['dev-9'], connected_at: 0 }],
      descriptor: makeDescriptor(),
      capabilities: catalogPayload,
      commands: [{
        id: 55, device_id: KEY, cmd: 'pulse', args: '{"ms":100}', status: 'failed',
        created_at: 1_780_000_000, acked_at: 1_780_000_001, result: 'busy',
      }],
    })
    renderDetail(ROUTE + '?tab=events')
    expect(await screen.findByText('设备恢复在线后可重试')).toBeVisible()
    expect(screen.queryByRole('button', { name: '重试点动' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '去设备操作中重试' })).not.toBeInTheDocument()
  })
})

it('只读身份可进入控制分区但没有参数表单或下发按钮', async () => {
  useAuth.setState({ status: 'in', user: { id: 2, username: 'viewer', name: '只读', role: 'viewer', tenant_id: 1, tenant_slug: 'default' } })
  route({ descriptor: makeDescriptor(), capabilities: catalogPayload })
  renderDetail(ROUTE + '?tab=controls')
  expect(await screen.findByText('当前账号没有操作权限。')).toBeInTheDocument()
  const panel = screen.getByRole('heading', { level: 2, name: '设备操作' }).closest('section') as HTMLElement
  expect(within(panel).queryByRole('button')).not.toBeInTheDocument()
  expect(within(panel).queryByRole('textbox')).not.toBeInTheDocument()
  expect(within(panel).queryByRole('spinbutton')).not.toBeInTheDocument()
})
