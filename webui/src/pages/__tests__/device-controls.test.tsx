// 设备控制面：命令集的唯一事实源是后端（Capability 声明 → Descriptor 扩展 →
// /api/adapters 白名单），前端**不得自建清单**。
//
// 因此这里的断言以「反向」为主：白名单里没有的命令名不许出现；白名单为空就不许摆按钮；
// 白名单变了按钮就跟着变（证明不是写死的一张表）。
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import DeviceDetail from '@/pages/DeviceDetail'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import { makeDeviceView, catalogPayload, makeDescriptor } from '@/test/fixtures'

const KEY = 'edge-1/dev-9'
const ROUTE = '/devices/edge-1/dev-9'

function renderDetail() {
  return renderWithProviders(
    <Routes>
      <Route path="/devices/:edgeId/:deviceId" element={<DeviceDetail />} />
    </Routes>,
    ROUTE,
  )
}

interface Opts { adapters?: { name: string; commands: string[] }[]; descriptor?: unknown; capabilities?: unknown }
function route(o: Opts = {}) {
  return installFetch((url) => {
    if (url === '/api/adapters') {
      return o.adapters ? stubResponse(200, { adapters: o.adapters }) : stubResponse(404, {})
    }
    if (url === `/api/devices/edge-1/dev-9`) return stubResponse(200, makeDeviceView())
    if (url.endsWith('/descriptor')) {
      return o.descriptor ? stubResponse(200, o.descriptor) : stubResponse(404, {})
    }
    if (url === '/api/descriptors') return stubResponse(404, {})
    if (url === '/api/capabilities') {
      return o.capabilities ? stubResponse(200, o.capabilities) : stubResponse(404, {})
    }
    if (url.startsWith('/api/devices/edge-1/dev-9/commands')) {
      return stubResponse(200, { id: 1, device_id: KEY, cmd: 'x', args: '', status: 'sent', created_at: 0, acked_at: 0, result: '' })
    }
    return stubResponse(404, {})
  })
}

async function gotoControls() {
  const user = userEvent.setup()
  await screen.findByRole('heading', { level: 1 })
  await user.click(screen.getByRole('tab', { name: /控制/ }))
  return user
}

/** 控制分区里所有可下发的命令按钮名 */
function commandButtons(): string[] {
  const panel = screen.getByText('命令').closest('section') as HTMLElement
  return within(panel).getAllByRole('button')
    .map((b) => b.getAttribute('aria-label') ?? b.textContent ?? '')
    .filter((n) => n && !/带参数|下发$/.test(n))
}

beforeEach(() => { resetStores() })

describe('命令集来自适配器白名单', () => {
  it('白名单里的命令逐条渲染成按钮，标注「适配器白名单」', async () => {
    route({ adapters: [{ name: 'demo', commands: ['raw', 'identify', 'query_state'] }] })
    renderDetail()
    await gotoControls()
    expect(await screen.findByText('适配器白名单')).toBeInTheDocument()
    const names = commandButtons()
    for (const expect0 of ['原始命令', 'Identify', 'Query State']) {
      expect(names, `白名单命令 ${expect0} 没有渲染成控件`).toContain(expect0)
    }
    // 带参数下发入口的候选同样来自白名单，不多不少
    const select = screen.getByRole('combobox', { name: '选择命令' })
    const options = within(select).getAllByRole('option').map((o) => o.textContent).filter((t) => t !== '选择命令')
    expect(options).toEqual(['raw', 'identify', 'query_state'])
  })

  it('反向断言：白名单里没有的命令名绝不出现（前端不自建清单）', async () => {
    route({ adapters: [{ name: 'demo', commands: ['identify'] }] })
    const { container } = renderDetail()
    await gotoControls()
    await screen.findByText('适配器白名单')
    const text = container.textContent ?? ''
    for (const forbidden of ['reboot', 'relay_on', 'relay_off', 'factory_reset', 'sync', 'Raw']) {
      expect(text, `出现了白名单之外的命令 ${forbidden}`).not.toContain(forbidden)
    }
  })

  it('白名单变了，控件跟着变（证明不是写死的一张表）', async () => {
    route({ adapters: [{ name: 'demo', commands: ['alpha_only'] }] })
    renderDetail()
    await gotoControls()
    await screen.findByText('适配器白名单')
    expect(commandButtons()).toContain('Alpha Only')
    const select = screen.getByRole('combobox', { name: '选择命令' })
    expect(within(select).getAllByRole('option').map((o) => o.textContent)).toEqual(['选择命令', 'alpha_only'])
  })

  it('适配器缺席（/api/adapters 404）且无 Descriptor → 明确空态，不摆一排猜出来的按钮', async () => {
    route({})
    renderDetail()
    await gotoControls()
    expect(await screen.findByText('该设备未声明可下发命令（等待 Descriptor / Capability catalog）')).toBeInTheDocument()
    expect(screen.getByText('无声明')).toBeInTheDocument()
  })

  it('白名单为空数组 → 同样走空态', async () => {
    route({ adapters: [{ name: 'demo', commands: [] }] })
    renderDetail()
    await gotoControls()
    expect(await screen.findByText('该设备未声明可下发命令（等待 Descriptor / Capability catalog）')).toBeInTheDocument()
  })
})

describe('有 Schema 声明时以声明为准', () => {
  it('Descriptor + Capability catalog → 命令来自声明，标注「Schema 声明」', async () => {
    route({
      adapters: [{ name: 'demo', commands: ['raw'] }],
      descriptor: makeDescriptor(),
      capabilities: catalogPayload,
    })
    renderDetail()
    await gotoControls()
    expect(await screen.findByText('Schema 声明')).toBeInTheDocument()
    const names = commandButtons()
    // 声明里的动作（close/open/pulse/factory_reset）→ 中文标题来自 Capability
    for (const declared of ['闭合', '断开', '点动', '恢复出厂']) {
      expect(names, `声明动作 ${declared} 未渲染`).toContain(declared)
    }
    // 白名单命令不再另立入口（声明优先）
    expect(screen.queryByRole('combobox', { name: '选择命令' })).not.toBeInTheDocument()
  })

  it('危险声明动作带二次确认，文案逐字取自声明', async () => {
    const user = userEvent.setup()
    route({ descriptor: makeDescriptor(), capabilities: catalogPayload })
    renderDetail()
    await gotoControls()
    await user.click(await screen.findByRole('button', { name: '恢复出厂' }))
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('确认恢复出厂？设备侧配置将被清空。')
    expect(within(dialog).getByRole('button', { name: '恢复出厂' })).toBeDisabled()
  })
})

describe('列表页的关键读数同样来自声明', () => {
  it('无声明 → 「等待声明」，不猜读数', async () => {
    const { default: Devices } = await import('@/pages/Devices')
    installFetch((url) => {
      if (url === '/api/devices') return stubResponse(200, { devices: [makeDeviceView()] })
      if (url === '/api/descriptors') return stubResponse(404, {})
      if (url === '/api/capabilities') return stubResponse(404, {})
      return stubResponse(404, {})
    })
    renderWithProviders(<Devices />)
    expect(await screen.findByText('等待声明')).toBeInTheDocument()
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
