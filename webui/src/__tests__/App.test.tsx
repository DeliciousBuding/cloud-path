// 路由与鉴权守卫的端到端（组件级）测试：
//   ① 受保护路由在 401 时跳 /login，实时通道随之断开（不制造 401 重连风暴）
//   ② 已登录访问 /login 跳首页
//   ③ me 不可用（后端认证未就绪）→ 开放访问放行，不把全站锁死
//   ④ 未知路径 → NotFound 回落页
//   ⑤ Schema 端点全 404 → 设备详情页走通用回落视图（不白屏、不报错刷屏）
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from '@/App'
import { connectLive, disconnectLive } from '@/store/ws'
import { makeDeviceView } from '@/test/fixtures'
import { installFetch, stubResponse, type StubResponse } from '@/test/http'
import { resetStores } from '@/test/render'

// 实时通道在 jsdom 里不真连（只断言「跟随登录态连/断」这一契约）
vi.mock('@/store/ws', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/ws')>()
  return { ...actual, connectLive: vi.fn(), disconnectLive: vi.fn(), reconnectLive: vi.fn() }
})

function renderApp(route: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, refetchInterval: false, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>
        <App />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const me = { user: { id: 1, username: 'ops', name: 'Ops', role: 'admin', tenant_id: 1, tenant_slug: 'local' } }

beforeEach(() => { resetStores() })

describe('鉴权守卫', () => {
  it('me→401 时访问受保护路由被重定向到 /login，并断开实时通道', async () => {
    installFetch((url) => (url === '/api/auth/me' ? stubResponse(401, { error: '未登录' }) : stubResponse(404, {})))
    renderApp('/devices')
    expect(await screen.findByRole('heading', { level: 1, name: '登录 Cloudpath' })).toBeInTheDocument()
    expect(disconnectLive).toHaveBeenCalled()
    expect(connectLive).not.toHaveBeenCalled()
  })

  it('已登录访问 /login → 跳首页，实时通道接上', async () => {
    installFetch((url) => (url === '/api/auth/me' ? stubResponse(200, me) : stubResponse(404, {})))
    renderApp('/login')
    expect(await screen.findByRole('heading', { level: 1, name: '概览' })).toBeInTheDocument()
    expect(connectLive).toHaveBeenCalled()
  })

  it('me 不可用（后端认证未就绪，404）→ 开放访问放行，业务页照常渲染', async () => {
    installFetch((url) => (url === '/api/auth/me' ? stubResponse(404, { error: 'no such endpoint' }) : stubResponse(404, {})))
    renderApp('/devices')
    expect(await screen.findByRole('heading', { level: 1, name: '设备' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '登录 Cloudpath' })).not.toBeInTheDocument()
    expect(connectLive).toHaveBeenCalled()
  })

  it('探测中先给整页骨架，不闪登录页也不闪内容', async () => {
    // me 悬停 → status 停在 loading。用例末尾必须放行：
    // store/auth.ts 的 refreshing 是模块级闩，悬着不放会污染后续所有用例的登录态探测。
    let release: (r: StubResponse) => void = () => {}
    const gate = new Promise<StubResponse>((resolve) => { release = resolve })
    installFetch((url) => (url === '/api/auth/me' ? gate : stubResponse(404, {})))
    renderApp('/devices')

    expect(screen.queryByRole('heading', { name: '登录 Cloudpath' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { level: 1 })).not.toBeInTheDocument()
    // 桌面侧栏与移动端顶栏各有一份同名导航，真实视口下 CSS 只暴露其一（jsdom 不吃 CSS）
    expect(screen.getAllByRole('navigation', { name: '主导航' })).toHaveLength(2)

    release(stubResponse(401, { error: '未登录' }))
    expect(await screen.findByRole('heading', { level: 1, name: '登录 Cloudpath' })).toBeInTheDocument()
  })
})

describe('路由回落', () => {
  it('未知路径 → NotFound（有可读标题与返回入口）', async () => {
    installFetch((url) => (url === '/api/auth/me' ? stubResponse(200, me) : stubResponse(404, {})))
    renderApp('/this-route-does-not-exist')
    expect(await screen.findByText('页面不存在')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /返回概览/ })).toHaveAttribute('href', '/')
  })
})

describe('Schema 端点缺席时的设备详情页', () => {
  it('descriptor/capabilities 全 404 → 通用回落视图 + 命令集来自适配器白名单', async () => {
    installFetch((url) => {
      if (url === '/api/auth/me') return stubResponse(200, me)
      if (url === '/api/adapters') return stubResponse(200, { adapters: [{ name: 'demo', commands: ['raw', 'identify'] }] })
      if (url === '/api/devices/edge-1/dev-9') return stubResponse(200, makeDeviceView())
      return stubResponse(404, { error: 'not found' })
    })
    renderApp('/devices/edge-1/dev-9')

    expect(await screen.findByText('通用视图')).toBeInTheDocument()
    expect(screen.getByText('该设备尚未提供 Descriptor，此处按上报字段通用渲染')).toBeInTheDocument()
    expect(screen.getByText('上报字段（通用视图）')).toBeInTheDocument()
    // 命令面板回落到后端白名单，而不是前端自己编一张命令表
    expect(screen.getByText('适配器白名单')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Raw' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '选择命令' })).toBeInTheDocument()
    // 上报字段按类型渲染：标量成键值行，对象数组成表格，嵌套对象成 JSON
    expect(screen.getByText('Mode')).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Slots 数据表' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Diag 原始 JSON' })).toBeInTheDocument()
  })

  it('设备不存在 → 明确空态而不是崩溃', async () => {
    installFetch((url) => (url === '/api/auth/me' ? stubResponse(200, me) : stubResponse(404, {})))
    renderApp('/devices/edge-x/dev-x')
    expect(await screen.findByText('设备未注册')).toBeInTheDocument()
    expect(screen.getByText(/没有找到 edge-x\/dev-x/)).toBeInTheDocument()
  })
})

describe('渲染崩溃兜底', () => {
  it('ErrorBoundary 把异常收敛成可读卡片（不白屏）', async () => {
    const Boom = () => { throw new Error('组件炸了') }
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/']}>
          <App />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    // App 自身不会炸；这里单独验证边界组件的行为契约
    const { ErrorBoundary } = await import('@/components/ErrorBoundary')
    const { default: React } = await import('react')
    const { render: r2 } = await import('@testing-library/react')
    r2(React.createElement(ErrorBoundary, null, React.createElement(Boom)))
    expect(screen.getByText('界面出现异常')).toBeInTheDocument()
    expect(screen.getByText('组件炸了')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /尝试继续/ })).toBeInTheDocument()
    spy.mockRestore()
  })
})