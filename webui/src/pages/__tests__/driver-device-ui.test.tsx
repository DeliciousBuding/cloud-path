import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import DeviceDetail from '@/pages/DeviceDetail'
import { installFetch, stubResponse } from '@/test/http'
import { catalogPayload, makeDescriptor, makeDeviceView } from '@/test/fixtures'
import { renderWithProviders, resetStores } from '@/test/render'
import { useAuth } from '@/store/auth'
import type { PluginCatalogView } from '@/lib/types'

const ROUTE = '/devices/edge-1/dev-9'

function driverPlugin(over: Partial<PluginCatalogView> = {}): PluginCatalogView {
  return {
    id: 'io.github.example.driver-stcb',
    kind: 'driver',
    version: '0.2.9',
    source: '',
    digest: '',
    verified: true,
    protocol: 1,
    permissions: {},
    contributes: {
      drivers: [{
        id: 'stcb',
        title: 'STC-B Driver',
        ui: {
          apiVersion: 1,
          device: {
            sections: [
              { type: 'status', source: 'device' },
              { type: 'actions', source: 'device-actions' },
              { type: 'diagnostics', source: 'diagnostics' },
            ],
          },
        },
      }],
    },
    ...over,
  }
}

function driverDescriptor() {
  const base = makeDescriptor()
  return makeDescriptor({
    manufacturer: 'STC',
    model: 'STC-B 学习板',
    entities: base.entities.map((entity) => entity.category === 'diagnostic'
      ? { ...entity, name: '驱动自检' }
      : entity),
  })
}

interface Opts {
  plugins?: PluginCatalogView[]
  descriptor?: unknown
}

function route(o: Opts = {}) {
  return installFetch((url) => {
    if (url === '/api/plugins') return stubResponse(200, { plugins: o.plugins ?? [] })
    if (url === '/api/devices/edge-1/dev-9') return stubResponse(200, makeDeviceView({ adapter: 'stcb' }))
    if (url === '/api/edges') return stubResponse(200, { edges: [{ edge_id: 'edge-1', online: true, version: 'dev', devices: ['dev-9'], connected_at: 0 }] })
    if (url === '/api/adapters') return stubResponse(200, { adapters: [] })
    if (url === '/api/descriptors') return stubResponse(404, {})
    if (url.endsWith('/descriptor')) return stubResponse(200, o.descriptor ?? driverDescriptor())
    if (url === '/api/capabilities') return stubResponse(200, catalogPayload)
    if (url.startsWith('/api/events')) return stubResponse(200, { events: [] })
    if (url.startsWith('/api/commands')) return stubResponse(200, { commands: [] })
    return stubResponse(404, {})
  })
}

function setRole(role: 'operator' | 'viewer') {
  useAuth.setState({
    status: 'in',
    user: { id: 1, username: role, name: role, role, tenant_id: 1, tenant_slug: 'default' },
  })
}

async function renderAndOpenDriver(role: 'operator' | 'viewer' = 'operator') {
  setRole(role)
  const user = userEvent.setup()
  renderWithProviders(
    <Routes>
      <Route path="/devices/:edgeId/:deviceId" element={<DeviceDetail />} />
    </Routes>,
    ROUTE,
  )
  await screen.findByRole('heading', { level: 1 })
  await user.click(screen.getByRole('tab', { name: /高级/ }))
  await user.click(await screen.findByRole('button', { name: 'STC-B Driver' }))
  return { user, root: screen.getByTestId('driver-device-sections') }
}

beforeEach(() => {
  resetStores()
})

describe('Driver device UI contribution', () => {
  it('renders only the matching verified Driver sections from descriptor and capability facts', async () => {
    route({ plugins: [driverPlugin()] })
    const { root } = await renderAndOpenDriver()

    expect(screen.getByRole('button', { name: 'STC-B Driver' })).toHaveAttribute('aria-pressed', 'true')
    expect(within(root).getByText('STC-B 学习板')).toBeInTheDocument()
    expect(within(root).getByText('0.2.9')).toBeInTheDocument()
    expect(within(root).getByText('驱动自检')).toBeInTheDocument()
    expect(within(root).getByRole('button', { name: '闭合' })).toBeInTheDocument()
    expect(root).toHaveClass('min-w-0')
    expect(root.querySelector('ul')).toHaveClass('grid-cols-2')
    expect(root.querySelector('[class*="min-w-["]')).toBeNull()
  })

  it.each([
    ['not installed', []],
    ['unverified', [driverPlugin({ verified: false })]],
    ['not a Driver plugin', [driverPlugin({ kind: 'application' })]],
  ])('does not add a driver view when the contribution is %s', async (_label, plugins) => {
    route({ plugins })
    const user = userEvent.setup()
    renderWithProviders(
      <Routes>
        <Route path="/devices/:edgeId/:deviceId" element={<DeviceDetail />} />
      </Routes>,
      ROUTE,
    )
    await screen.findByRole('heading', { level: 1 })
    await user.click(screen.getByRole('tab', { name: /高级/ }))
    await screen.findByText('设备信息与原始状态')

    expect(screen.queryByRole('button', { name: 'STC-B Driver' })).not.toBeInTheDocument()
    expect(screen.queryByTestId('driver-device-sections')).not.toBeInTheDocument()
  })

  it('keeps Driver actions behind the same operator RBAC boundary', async () => {
    route({ plugins: [driverPlugin()] })
    const { root } = await renderAndOpenDriver('viewer')

    expect(within(root).getByText('设备操作')).toBeInTheDocument()
    expect(within(root).getByText('当前账号没有操作权限。')).toBeInTheDocument()
    expect(within(root).queryByRole('button', { name: '闭合' })).not.toBeInTheDocument()
    expect(within(root).getByText('驱动自检')).toBeInTheDocument()
  })
})