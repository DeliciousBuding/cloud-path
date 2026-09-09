// 账号可见性与登出：真实登录后用户必须能看到自己是谁、并且能登出（共用机器场景）。
// 同时守住 Settings 对鉴权方式的诚实描述 —— 账号模式靠会话 cookie，本机令牌是可选的，
// 不能再把 legacy 共享令牌说成「必须携带」（那会把用户推回 D3 那种假登录心智）。
import { screen, waitFor, within } from '@testing-library/react'
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
import type { AdapterView, AuthMode, UserView } from '@/lib/types'

const admin: UserView = {
  id: 1, username: 'ops-admin', name: '运维管理员', role: 'admin',
  tenant_id: 1, tenant_slug: 'default',
}
const viewer: UserView = {
  id: 3, username: 'viewer', name: '只读访客', role: 'viewer',
  tenant_id: 1, tenant_slug: 'default',
}
const health = { ok: true, version: 'v0.1.0', uptime_s: 60, devices_online: 0, devices_total: 0, edges_online: 0 }
const stats = {
  devices: 0, events: 0, commands: 0, oldest_event: 0, schema_version: 8,
  retention_days: 30, auth_mode: 'account' as AuthMode,
}

function route(opts: { authMode?: AuthMode; adapters?: AdapterView[]; tokenMeStatus?: number; meUser?: UserView } = {}) {
  return installFetch((url, init) => {
    if (url === '/healthz') return stubResponse(200, health)
    if (url === '/api/auth/me') {
      return stubResponse(opts.tokenMeStatus ?? 200, opts.tokenMeStatus && opts.tokenMeStatus >= 400
        ? { error: 'not authenticated' } : { user: opts.meUser ?? admin })
    }
    if (url === '/api/stats') {
      return stubResponse(200, { ...stats, auth_mode: opts.authMode ?? 'account' })
    }
    if (url === '/api/auth/logout') return stubResponse(204, undefined)
    if (url === '/api/adapters') return stubResponse(200, { adapters: opts.adapters ?? [] })
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
  setToken('')
  // 实时通道连上，避免系统提示条抢占 role=status
  useLive.setState({ status: 'open' })
})

async function openDiagnostics() {
  const user = userEvent.setup()
  const summary = screen.getByText('高级诊断').closest('summary')
  expect(summary).not.toBeNull()
  expect(summary).toHaveTextContent('展开查看')
  await user.click(summary as HTMLElement)
  expect(summary).toHaveTextContent('收起')
}

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
    expect(screen.getByText('查看当前账号、外观和高级诊断。')).toBeInTheDocument()
    expect((await screen.findAllByText('ops-admin')).length).toBeGreaterThan(0)
    expect(screen.getAllByText('管理员').length).toBeGreaterThan(0)
    expect(screen.getByText('default')).toBeInTheDocument()
    await openDiagnostics()
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
    await openDiagnostics()
    expect(await screen.findByText('需要账号登录')).toBeInTheDocument()
    expect(screen.queryByText(/未启用/)).not.toBeInTheDocument()
    expect(screen.queryByText(/本机模式/)).not.toBeInTheDocument()
  })

  it('仅共享 legacy 令牌 / L0 单机：各自如实说明读写边界，不共用一句含糊话', async () => {
    route({ authMode: 'token' })
    useAuth.setState({ status: 'in', user: admin })
    const { unmount } = renderWithProviders(<Settings />)
    await openDiagnostics()
    expect(await screen.findByText('使用访问令牌：可查看，修改需令牌或本机操作')).toBeInTheDocument()
    unmount()

    route({ authMode: 'open' })
    renderWithProviders(<Settings />)
    await openDiagnostics()
    expect(await screen.findByText('无需登录：可查看，修改仅限本机')).toBeInTheDocument()
  })

  it('未知鉴权形态回落原值展示，不伪造中文语义', async () => {
    route({ authMode: 'future_mode' as AuthMode })
    useAuth.setState({ status: 'in', user: admin })
    renderWithProviders(<Settings />)
    await openDiagnostics()
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

  it('已登录时不提供保存访问令牌，避免 Bearer 静默覆盖当前身份', async () => {
    route()
    useAuth.setState({ status: 'in', user: admin })
    renderWithProviders(<Settings />)
    const panel = (await screen.findByRole('heading', { name: '访问令牌' })).closest('section') as HTMLElement
    expect(within(panel).getByText(/当前已登录，不需要再保存访问令牌/)).toBeInTheDocument()
    expect(within(panel).queryByPlaceholderText('收到令牌或使用自动化工具时填写')).toBeNull()
    expect(within(panel).queryByRole('button', { name: '保存令牌' })).toBeNull()
    // 旧文案（把共享令牌说成强制）必须消失
    expect(panel.textContent).not.toContain('都必须携带同一令牌')
  })

  it('开放访问保存访问令牌：先无 cookie 验证，再同步身份并落盘', async () => {
    const user = userEvent.setup()
    const http = route({ tokenMeStatus: 200 })
    useAuth.setState({ status: 'open', user: null })
    renderWithProviders(<Settings />)

    const input = screen.getByPlaceholderText('收到令牌或使用自动化工具时填写')
    await user.type(input, 'cp_valid_token')
    expect(screen.queryByText('已保存')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '保存令牌' }))

    await waitFor(() => expect(getToken()).toBe('cp_valid_token'))
    await waitFor(() => expect(useAuth.getState().status).toBe('in'))
    const probes = http.to('/api/auth/me')
    expect(probes.length).toBeGreaterThanOrEqual(2)
    expect(probes[0]?.credentials).toBe('omit')
    expect(probes[0]?.headers.Authorization).toBe('Bearer cp_valid_token')
  })

  it('仅网关权限的访问令牌不能保存为平台登录凭据', async () => {
    const user = userEvent.setup()
    route({ tokenMeStatus: 200, meUser: { ...admin, role: '' as UserView['role'] } })
    useAuth.setState({ status: 'open', user: null })
    renderWithProviders(<Settings />)

    const input = screen.getByPlaceholderText('收到令牌或使用自动化工具时填写')
    await user.type(input, 'cp_edge_only')
    await user.click(screen.getByRole('button', { name: '保存令牌' }))

    expect(await screen.findByText(/只能用于网关接入/)).toBeInTheDocument()
    expect(getToken()).toBe('')
    expect(useAuth.getState().status).not.toBe('in')
  })

  it('访问令牌验证失败：不覆盖旧令牌，恢复旧值并提示重试', async () => {
    const user = userEvent.setup()
    setToken('cp_previous_token')
    route({ tokenMeStatus: 401 })
    useAuth.setState({ status: 'open', user: null })
    renderWithProviders(<Settings />)

    const input = screen.getByPlaceholderText('收到令牌或使用自动化工具时填写')
    await user.clear(input)
    await user.type(input, 'cp_invalid_token')
    await user.click(screen.getByRole('button', { name: '保存令牌' }))

    expect(await screen.findByText(/访问令牌无效、已吊销或权限不足/)).toBeInTheDocument()
    expect(getToken()).toBe('cp_previous_token')
    expect(input).toHaveValue('cp_previous_token')
  })

  it('外观是普通用户可操作项，切换会保存偏好', async () => {
    const user = userEvent.setup()
    route()
    useAuth.setState({ status: 'in', user: admin })
    renderWithProviders(<Settings />)

    const group = screen.getByRole('group', { name: '外观' })
    await user.click(within(group).getByRole('button', { name: '深色' }))
    expect(localStorage.getItem('cloudpath.theme')).toBe('dark')
  })

  it('高级诊断默认收起；读取失败时显示错误，不留假 0 或假加载', async () => {
    installFetch((url) => {
      if (url === '/healthz') return stubResponse(200, health)
      if (url === '/api/stats') return stubResponse(500, { error: 'boom' })
      if (url === '/api/adapters') return stubResponse(200, { adapters: [] })
      return stubResponse(404, {})
    })
    useAuth.setState({ status: 'in', user: admin })
    renderWithProviders(<Settings />)

    expect(screen.queryByText('平台版本')).not.toBeInTheDocument()
    await openDiagnostics()
    expect(await screen.findByText('无法读取记录统计')).toBeInTheDocument()
    expect(screen.queryByText('运行记录总数')).not.toBeInTheDocument()
    expect(screen.queryByText('正在读取记录统计…')).not.toBeInTheDocument()
  })

  it('普通成员的高级诊断不暴露适配器内部标识、命令码或记录格式版本', async () => {
    route({ adapters: [{ name: 'stcb-internal-adapter-id', commands: ['raw_internal_command'] }] })
    useAuth.setState({ status: 'in', user: viewer })
    renderWithProviders(<Settings />)
    await openDiagnostics()

    expect(await screen.findByText('1 种接入方式')).toBeInTheDocument()
    expect(screen.getByText(/已登记 1 种设备接入方式/)).toBeInTheDocument()
    expect(screen.queryByText('stcb-internal-adapter-id')).not.toBeInTheDocument()
    expect(screen.queryByText('raw_internal_command')).not.toBeInTheDocument()
    expect(screen.queryByText(/记录格式版本/)).not.toBeInTheDocument()
    expect(screen.queryByText('账号 ID')).not.toBeInTheDocument()
    expect(screen.queryByText('所属组织')).not.toBeInTheDocument()
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
