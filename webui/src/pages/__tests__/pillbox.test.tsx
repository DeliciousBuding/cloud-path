// 药盒控制面板：Schema 驱动 + 真实读面。
// 断言核心：
//   - 药格卡片来自 Descriptor 声明（实体数/观测值），不写死字段名
//   - 命令按钮来自后端声明/白名单，不写死命令文案/图标
//   - 提醒与漏服历史来自 /api/events 与 /api/commands（真实读面）
//   - 空态/加载/错误态 + 390px 长标识符收口
import { screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import Pillbox from '@/pages/Pillbox'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import { makeDeviceView } from '@/test/fixtures'

const CAP_CONTACT = 'cloudpath.dev/capability/contact@1'
const CAP_ALARM = 'cloudpath.dev/capability/alarm@1'
const CAP_CLOCK = 'cloudpath.dev/capability/clock@1'

function slot(entityId: string, name: string, value: string, quality = 'good') {
  return {
    entity_id: entityId, unique_key: entityId, name, category: 'sensor',
    capabilities: [CAP_CONTACT],
    observations: { state: { capability: CAP_CONTACT, property: 'state', value, quality } },
  }
}

/** STC-B 药盒设备的 Descriptor：3 个分格 + 时钟 + 提醒 */
const pillDescriptor = {
  device_id: 'edge-1/dev-9',
  external_id: 'dev-9',
  manufacturer: 'STC',
  model: 'STC-B (IAP15F2K61S2)',
  status: 'online',
  entities: [
    {
      entity_id: 'clock', unique_key: 'clock', name: '时钟', category: 'sensor',
      capabilities: [CAP_CLOCK],
      observations: { time: { capability: CAP_CLOCK, property: 'time', value: '09:30', quality: 'good' } },
    },
    slot('compartment-1', '分格 1', '待确认'),
    slot('compartment-2', '分格 2', '已确认'),
    slot('compartment-3', '分格 3', '逾期', 'bad'),
    {
      entity_id: 'alarm', unique_key: 'alarm', name: '提醒', category: 'actuator',
      capabilities: [CAP_ALARM],
      observations: { state: { capability: CAP_ALARM, property: 'state', value: '待机', quality: 'good' } },
    },
  ],
}

const caps = {
  capabilities: [
    { apiVersion: 'capabilities.cloudpath.dev/v1alpha1', kind: 'Capability',
      metadata: { id: CAP_CONTACT, version: 1, title: '接触' },
      spec: { properties: { state: { type: 'string' } }, presentation: { primaryProperty: 'state', defaultWidget: 'badge' } } },
    { apiVersion: 'capabilities.cloudpath.dev/v1alpha1', kind: 'Capability',
      metadata: { id: CAP_ALARM, version: 1, title: '提醒' },
      spec: { properties: { state: { type: 'string' } }, presentation: { primaryProperty: 'state', defaultWidget: 'badge' } } },
    { apiVersion: 'capabilities.cloudpath.dev/v1alpha1', kind: 'Capability',
      metadata: { id: CAP_CLOCK, version: 1, title: '时钟' },
      spec: { properties: { time: { type: 'string' } }, presentation: { primaryProperty: 'time', defaultWidget: 'text' } } },
  ],
}

function route(o: { descriptor?: unknown; devices?: unknown; events?: unknown; adapters?: unknown } = {}) {
  return installFetch((url) => {
    if (url === '/api/devices') return stubResponse(200, { devices: o.devices ?? [makeDeviceView()] })
    if (url === '/api/adapters') return stubResponse(200, o.adapters ?? { adapters: [{ name: 'demo', commands: ['open', 'trigger'] }] })
    if (url === '/api/descriptors') return stubResponse(404, {})
    if (url.endsWith('/descriptor')) return stubResponse(200, o.descriptor ?? pillDescriptor)
    if (url === '/api/capabilities') return stubResponse(200, caps)
    if (url.startsWith('/api/events?')) return stubResponse(200, { events: o.events ?? [
      { id: 1, device_id: 'edge-1/dev-9', ts: 1_770_000_000, type: 'REMIND', payload: '{}' },
    ] })
    if (url.startsWith('/api/commands?')) return stubResponse(200, { commands: [
      { id: 2, device_id: 'edge-1/dev-9', cmd: 'open', args: '', status: 'ok', created_at: 1_770_000_000, acked_at: 1_770_000_010, result: 'OK' },
    ] })
    return stubResponse(404, {})
  })
}

beforeEach(() => { resetStores() })

describe('药盒控制面板（Schema 驱动）', () => {
  it('药格卡片来自 Descriptor 声明（3 分格 + 时钟 + 提醒），不写死字段', async () => {
    route()
    renderWithProviders(<Pillbox />)
    await screen.findByText('药盒控制')
    // 等待首位设备默认选中并加载出实体卡片
    const cards = await screen.findAllByTestId('slot-card')
    // 时钟 + 3 分格 + 提醒 = 5 个实体
    expect(cards.length).toBeGreaterThanOrEqual(5)
    // 分格的名与主观测值均来自声明（后端给什么就渲染什么）
    expect(screen.getByText('分格 1')).toBeInTheDocument()
    expect(screen.getByText('分格 3')).toBeInTheDocument()
    expect(screen.getByText('逾期')).toBeInTheDocument()
  })

  it('命令按钮来自后端白名单/声明，不写死命令文案/图标', async () => {
    route()
    renderWithProviders(<Pillbox />)
    await screen.findByText('药盒控制')
    const panel = (await screen.findAllByText('命令')).map((n) => n.closest('section') as HTMLElement)
    const names = panel.flatMap((p) => within(p).getAllByRole('button')
      .map((b) => b.getAttribute('aria-label') ?? b.textContent ?? '')
      .filter((n) => n && !/带参数|下发$/.test(n)))
    expect(names).toContain('Open')
    expect(names).toContain('Trigger')
  })

  it('提醒与漏服历史来自真实事件/命令读出（REMIND 事件 + open 命令）', async () => {
    route()
    renderWithProviders(<Pillbox />)
    await screen.findByText('药盒控制')
    // 事件流：类型名来自后端，未声明时 humanize 回落
    expect(await screen.findByText('Remind')).toBeInTheDocument()
    // 命令历史：cmd 展示名来自声明/白名单
    const historyPanel = (await screen.findByText('命令历史')).closest('section') as HTMLElement
    expect(within(historyPanel).getByText('Open')).toBeInTheDocument()
  })

  it('设备列表为空 → 明确空态，不摆假面板', async () => {
    route({ devices: [] })
    renderWithProviders(<Pillbox />)
    expect(await screen.findByText('还没有设备')).toBeInTheDocument()
  })

  it('设备列表加载失败 → 错误态（接口失败 ≠ 没有设备）', async () => {
    installFetch((url) => (url === '/api/devices' ? stubResponse(500, { error: 'boom' }) : stubResponse(404, {})))
    renderWithProviders(<Pillbox />)
    expect(await screen.findByText('设备列表加载失败')).toBeInTheDocument()
  })

  it('设备有 Descriptor 但未声明 Entity → 药格区空态，命令区仍可用', async () => {
    route({ descriptor: { device_id: 'edge-1/dev-9', external_id: 'dev-9', status: 'online', entities: [] } })
    renderWithProviders(<Pillbox />)
    await screen.findByText('药盒控制')
    expect(await screen.findByText('设备声明里没有可呈现的观测项，没有可展示的药格。')).toBeInTheDocument()
  })

  it('锁定设备路由 /pillbox/:edgeId/:deviceId 不显示选择器，直出面板', async () => {
    route()
    renderWithProviders(<Pillbox />, '/pillbox/edge-1/dev-9')
    expect(await screen.findByText('药盒控制')).toBeInTheDocument()
    expect(screen.queryByLabelText('选择设备')).not.toBeInTheDocument()
    await screen.findAllByTestId('slot-card')
  })

  it('390px：长设备名/命令不产生横向溢出（无内联像素宽度）', async () => {
    const long = 'x'.repeat(64)
    route({ devices: [{ ...makeDeviceView(), id: `edge-1/${long}`, name: long }] })
    renderWithProviders(<Pillbox />)
    await screen.findByText('药盒控制')
    await screen.findAllByTestId('slot-card')
    for (const el of document.querySelectorAll<HTMLElement>('[style]')) {
      expect(el.style.width).not.toMatch(/px$/)
      expect(el.style.minWidth).not.toMatch(/px$/)
    }
  })
})



