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
import type { AuthMode, UserView } from '@/lib/types'

const admin: UserView = {
  id: 1, username: 'ops-admin', name: '运维管理员', role: 'admin',
  tenant_id: 1, tenant_slug: 'default',
}
const health = { ok: true, version: 'v0.1.0', uptime_s: 60, devices_online: 0, devices_total: 0, edges_online: 0 }
const stats = {
  devices: 0, events: 0, commands: 0, oldest_event: 0, schema_version: 8,
  retention_days: 30, auth_mode: 'account' as AuthMode,
}

function route(opts: { authMode?: AuthMode } = {}) {
  return installFetch((url, init) => {
    if (url === '/healthz') return stubResponse(200, health)
    if (url === '/api/stats') {
      return stubResponse(200, { ...stats, auth_mode: opts.authMode ?? 'account' })
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
    const aside = document.querySelector('aside') as HTMLElement
    expect(within(aside).getByText('运维管理员')).toBeInTheDocument()
    expect(within(aside).getByText('管理员')).toBeInTheDocument()
    expect(within(aside).getByRole('button', { name: '登出' })).toBeInTheDocument()
  })

  it('登出真的打 POST /api/auth/logout、清掉本机令牌并把状态置为未登录', async () => {
    const user = userEvent.setup()
    const http = route()
    setToken('stale-token')
    useAuth.setState({ status: 'in', user: admin })
    renderLayout()

    await user.click(within(document.querySelector('aside') as HTMLElement).getByRole('button', { name: '登出' }))
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

})

describe('Settings 账号与令牌面板', () => {
  it('已登录：显示用户名/角色/组织，鉴权标注为账号鉴权', async () => {
    route()
    useAuth.setState({ status: 'in', user: admin })
    renderWithProviders(<Settings />)
    expect(await screen.findByText('ops-admin')).toBeInTheDocument()
    expect(screen.getByText('管理员')).toBeInTheDocument()
    expect(screen.getByText('default')).toBeInTheDocument()
    // stats 是异步的，等它落地再断言鉴权标注
    expect(await screen.findByText('需要账号登录')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /退出登录/ })).toBeInTheDocument()
  })

  // 回归：auth_enabled 时代只看「有没有配 legacy 令牌」，账号模式（已建用户、无 CLOUDPATH_TOKEN）
  // 会被报成 false，系统页于是显示「未启用（本机模式）」——把必须登录的部署说成裸奔，
  // 而同一页下方还写着「账号模式下浏览器靠会话 cookie 鉴权」，自相矛盾。
  it('鉴权行照 server 实际形态说话：账号模式绝不说成未启用', async () => {
    route({ authMode: 'account' })
    useAuth.setState({ status: 'in', user: admin })
    renderWithProviders(<Settings />)
    expect(await screen.findByText('需要账号登录')).toBeInTheDocument()
    expect(screen.queryByText(/未启用/)).not.toBeInTheDocument()
    expect(screen.queryByText(/本机模式/)).not.toBeInTheDocument()
  })

  it('仅共享 legacy 令牌 / L0 单机：各自如实说明读写边界，不共用一句含糊话', async () => {
    route({ authMode: 'token' })
    useAuth.setState({ status: 'in', user: admin })
    const { unmount } = renderWithProviders(<Settings />)
    expect(await screen.findByText('使用访问令牌：可查看，修改需令牌或本机操作')).toBeInTheDocument()
    unmount()

    route({ authMode: 'open' })
    renderWithProviders(<Settings />)
    expect(await screen.findByText('无需登录：可查看，修改仅限本机')).toBeInTheDocument()
  })

  it('未知鉴权形态回落原值展示，不伪造中文语义', async () => {
    route({ authMode: 'future_mode' as AuthMode })
    useAuth.setState({ status: 'in', user: admin })
    renderWithProviders(<Settings />)
    expect(await screen.findByText('future_mode')).toBeInTheDocument()
  })

  it('开放访问：说明这是 me 不可用，而不是假装已登录', async () => {
    route({ authMode: 'open' })
    useAuth.setState({ status: 'open', user: null })
    renderWithProviders(<Settings />)
    expect(await screen.findByText('无需登录')).toBeInTheDocument()
    expect(screen.getByText(/当前无需登录即可查看/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^退出登录$/ })).not.toBeInTheDocument()
  })

  it('未登录：如实说明受保护接口会 401，并指向登录页', async () => {
    route()
    useAuth.setState({ status: 'out', user: null })
    renderWithProviders(<Settings />)
    expect(await screen.findByText('未登录')).toBeInTheDocument()
    expect(screen.getByText(/尚未登录。请先到登录页用账号密码登录/)).toBeInTheDocument()
  })

  it('访问令牌是可选入口：账号登录默认使用浏览器登录状态', async () => {
    route()
    useAuth.setState({ status: 'in', user: admin })
    renderWithProviders(<Settings />)
    const panel = (await screen.findByText('访问令牌（可选）')).closest('section') as HTMLElement
    expect(within(panel).getByText(/不需要填写/)).toBeInTheDocument()
    expect(within(panel).getByPlaceholderText('留空即使用当前登录状态')).toBeInTheDocument()
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
    await user.click(await screen.findByRole('button', { name: /退出登录/ }))
    expect(await screen.findByRole('heading', { name: '登录页占位' })).toBeInTheDocument()
    expect(useAuth.getState().status).toBe('out')
  })
})