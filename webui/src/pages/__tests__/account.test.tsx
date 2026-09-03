// 账号可见性与登出：真实登录后用户必须能看到自己是谁、并且能登出（共用机器场景）。
// 同时守住 Settings 对鉴权方式的诚实描述 —— 账号模式靠会话 cookie，本机令牌是可选的，
// 不能再把 legacy 共享令牌说成「必须携带」（那会把用户推回 D3 那种假登录心智）。
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import Layout from '@/components/Layout'
import Settings from '@/pages/Settings'
import { getToken, setToken } from '@/lib/api'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import { useAuth } from '@/store/auth'
import { useLive } from '@/store/ws'
import type { UserView } from '@/lib/types'

const admin: UserView = {
  id: 1, username: 'ops-admin', name: '运维管理员', role: 'admin',
  tenant_id: 1, tenant_slug: 'default',
}
const health = { ok: true, version: 'v0.1.0', uptime_s: 60, devices_online: 0, devices_total: 0, edges_online: 0 }
const stats = {
  devices: 0, events: 0, commands: 0, oldest_event: 0, schema_version: 8,
  retention_days: 30, auth_enabled: true,
}

function route(opts: { authEnabled?: boolean } = {}) {
  return installFetch((url, init) => {
    if (url === '/healthz') return stubResponse(200, health)
    if (url === '/api/stats') {
      return stubResponse(200, { ...stats, auth_enabled: opts.authEnabled ?? true })
    }
    if (url === '/api/auth/logout') return stubResponse(204, undefined)
    if (url === '/api/adapters') return stubResponse(200, { adapters: [] })
    if (init?.method) return stubResponse(404, {})
    return stubResponse(404, {})
  })
}

function renderLayout() {
  return renderWithProviders(
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<h1>页面内容</h1>} />
      </Route>
    </Routes>,
  )
}

beforeEach(() => {
  resetStores()
  // 实时通道连上，避免系统提示条抢占 role=status
  useLive.setState({ status: 'open' })
})

describe('侧栏账号区', () => {
  it('已登录时显示姓名与角色，并给出可读的登出按钮', () => {
    route()
    useAuth.setState({ status: 'in', user: admin })
    renderLayout()
    expect(screen.getByText('运维管理员')).toBeInTheDocument()
    expect(screen.getByText('管理员')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '登出' })).toBeInTheDocument()
  })

  it('登出真的打 POST /api/auth/logout、清掉本机令牌并把状态置为未登录', async () => {
    const user = userEvent.setup()
    const http = route()
    setToken('stale-token')
    useAuth.setState({ status: 'in', user: admin })
    renderLayout()

    await user.click(screen.getByRole('button', { name: '登出' }))
    expect(http.to('/api/auth/logout')).toHaveLength(1)
    expect(http.to('/api/auth/logout')[0]?.method).toBe('POST')
    expect(getToken()).toBe('')
    expect(useAuth.getState().status).toBe('out')
    expect(useAuth.getState().user).toBeNull()
  })

  it('未登录 / 开放访问时不渲染账号区（不给「登出」这种无意义入口）', () => {
    route()
    useAuth.setState({ status: 'open', user: null })
    renderLayout()
    expect(screen.queryByRole('button', { name: '登出' })).not.toBeInTheDocument()

    useAuth.setState({ status: 'out', user: null })
    expect(screen.queryByRole('button', { name: '登出' })).not.toBeInTheDocument()
  })

  it('长用户名/长姓名在侧栏里截断，不撑破 240px 侧栏', () => {
    route()
    const LONG = 'y'.repeat(64)
    useAuth.setState({ status: 'in', user: { ...admin, username: LONG, name: LONG } })
    renderLayout()
    const hits = screen.getAllByText(LONG)
    expect(hits.length).toBeGreaterThan(0)
    for (const el of hits) {
      expect(String(el.className), `${el.tagName} 未做截断收口`).toMatch(/truncate|break-words|break-all/)
    }
  })
})

describe('Settings 账号与令牌面板', () => {
  it('已登录：显示用户名/角色/租户，鉴权标注为账号鉴权', async () => {
    route()
    useAuth.setState({ status: 'in', user: admin })
    renderWithProviders(<Settings />)
    expect(await screen.findByText('ops-admin')).toBeInTheDocument()
    expect(screen.getByText('管理员')).toBeInTheDocument()
    expect(screen.getByText('default')).toBeInTheDocument()
    // stats 是异步的，等它落地再断言鉴权标注\n    expect(await screen.findByText('已启用账号鉴权')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /登出/ })).toBeInTheDocument()
  })

  it('开放访问：说明这是 me 不可用，而不是假装已登录', async () => {
    route({ authEnabled: false })
    useAuth.setState({ status: 'open', user: null })
    renderWithProviders(<Settings />)
    expect(await screen.findByText('开放访问')).toBeInTheDocument()
    expect(screen.getByText(/GET \/api\/auth\/me 不可用/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^登出$/ })).not.toBeInTheDocument()
  })

  it('未登录：如实说明受保护接口会 401，并指向登录页', async () => {
    route()
    useAuth.setState({ status: 'out', user: null })
    renderWithProviders(<Settings />)
    expect(await screen.findByText('未登录')).toBeInTheDocument()
    expect(screen.getByText(/受保护的数据接口会返回 401/)).toBeInTheDocument()
  })

  it('令牌面板不再把 legacy 共享令牌说成必需：账号模式默认走会话 cookie', async () => {
    route()
    useAuth.setState({ status: 'in', user: admin })
    renderWithProviders(<Settings />)
    const panel = (await screen.findByText('本机令牌（可选）')).closest('section') as HTMLElement
    expect(within(panel).getByText(/不需要填任何东西/)).toBeInTheDocument()
    expect(within(panel).getByText(/会话 cookie/)).toBeInTheDocument()
    expect(within(panel).getByPlaceholderText(/留空 = 用会话 cookie/)).toBeInTheDocument()
    // 旧文案（把共享令牌说成强制）必须消失
    expect(panel.textContent).not.toContain('都必须携带同一令牌')
  })

  it('登出后回到登录页（Settings 里的登出与侧栏一致）', async () => {
    const user = userEvent.setup()
    route()
    useAuth.setState({ status: 'in', user: admin })
    renderWithProviders(
      <Routes>
        <Route path="/login" element={<h1>登录页占位</h1>} />
        <Route path="*" element={<Settings />} />
      </Routes>,
      '/settings',
    )
    await user.click(await screen.findByRole('button', { name: /登出/ }))
    expect(await screen.findByRole('heading', { name: '登录页占位' })).toBeInTheDocument()
    expect(useAuth.getState().status).toBe('out')
  })
})