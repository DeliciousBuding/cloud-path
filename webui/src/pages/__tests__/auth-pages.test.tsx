// 认证页（Login / Setup）：真实账号鉴权、错误语义、凭据卫生、跳转，以及路由守卫收敛。
//
// 这一组用例的存在理由就是 P0 缺陷 D3：旧登录页把「任意字符串塞进 localStorage + 打一次
// 无需鉴权的 /healthz」当成登录成功，公网上表现为「登录进去了但整站 401、没有数据」。
// 因此这里的核心断言是**反向**的：错误凭据必须失败、公开端点可达绝不能算成功。
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactElement } from 'react'
import App from '@/App'
import Login from '@/pages/Login'
import Setup from '@/pages/Setup'
import { api, getToken } from '@/lib/api'
import { installFetch, stubResponse } from '@/test/http'
import { resetStores } from '@/test/render'
import { useAuth } from '@/store/auth'

const health = { ok: true, version: 'v0.1.0', uptime_s: 60, devices_online: 0, devices_total: 0, edges_online: 0 }
const admin = { id: 1, username: 'admin', name: 'Admin', role: 'admin', tenant_id: 1, tenant_slug: 'default' }

/** 挂一个「首页」标记，用来断言认证成功后的跳转真的发生了 */
function renderPage(page: ReactElement, route: string) {
  return render(
    <MemoryRouter initialEntries={[route]}>
      <Routes>
        <Route path="/" element={<h1>首页占位</h1>} />
        <Route path="*" element={page} />
      </Routes>
    </MemoryRouter>,
  )
}

/** /healthz 永远 200：它是公开端点，可达与否**不得**影响登录结论 */
type Router = (url: string) => ReturnType<typeof stubResponse>
function routeWith(auth: Router) {
  return installFetch((url) => (url === '/healthz' ? stubResponse(200, health) : auth(url)))
}

// 实时通道在 jsdom 里不真连（只断言「跟随登录态连/断」这一契约）
vi.mock('@/store/ws', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/ws')>()
  return { ...actual, connectLive: vi.fn(), disconnectLive: vi.fn(), reconnectLive: vi.fn() }
})

beforeEach(() => { resetStores() })

describe('Login：真实账号鉴权（D3 修复）', () => {
  it('渲染用户名与密码两个字段，不再是单一「访问令牌」', () => {
    routeWith(() => stubResponse(404, {}))
    renderPage(<Login />, '/login')
    expect(screen.getByRole('heading', { level: 1, name: '登录 Cloudpath' })).toBeInTheDocument()
    expect(screen.getByLabelText('用户名')).toBeInTheDocument()
    expect(screen.getByLabelText('密码')).toBeInTheDocument()
    expect(screen.queryByLabelText(/访问令牌/)).not.toBeInTheDocument()
    // 浏览器密码管理器可用
    expect(screen.getByLabelText('用户名')).toHaveAttribute('autocomplete', 'username')
    expect(screen.getByLabelText('密码')).toHaveAttribute('autocomplete', 'current-password')
    expect(screen.getByLabelText('密码')).toHaveAttribute('type', 'password')
    expect(screen.getByRole('button', { name: '登录' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '首次部署？运行设置向导' })).toHaveAttribute('href', '/setup')
  })

  it('提交走 POST /api/auth/login，且**不**把 /healthz 当成功判据', async () => {
    const user = userEvent.setup()
    const healthSpy = vi.spyOn(api, 'health')
    const loginSpy = vi.spyOn(api, 'login')
    const http = routeWith((url) => (url.startsWith('/api/auth/')
      ? stubResponse(200, { user: admin }) : stubResponse(404, {})))
    renderPage(<Login />, '/login')

    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'correct-horse')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByRole('heading', { name: '首页占位' })).toBeInTheDocument()
    expect(loginSpy).toHaveBeenCalledWith('admin', 'correct-horse')
    expect(healthSpy).not.toHaveBeenCalled()
    expect(http.to('/healthz')).toHaveLength(0)
    const post = http.to('/api/auth/login')
    expect(post).toHaveLength(1)
    expect(post[0]?.method).toBe('POST')
    expect(post[0]?.body).toEqual({ username: 'admin', password: 'correct-horse' })
  })

  it('成功后用 GET /api/auth/me 复核并把用户写进 auth store', async () => {
    const user = userEvent.setup()
    const meSpy = vi.spyOn(api, 'me')
    routeWith((url) => (url.startsWith('/api/auth/') ? stubResponse(200, { user: admin }) : stubResponse(404, {})))
    renderPage(<Login />, '/login')
    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'pw')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByRole('heading', { name: '首页占位' })).toBeInTheDocument()
    expect(meSpy).toHaveBeenCalled()
    expect(useAuth.getState().status).toBe('in')
    expect(useAuth.getState().user?.username).toBe('admin')
  })

  it('反向验证：401 → 「用户名或密码错误」，不跳转、不写登录态、清空密码', async () => {
    const user = userEvent.setup()
    routeWith((url) => (url === '/api/auth/login'
      ? stubResponse(401, { error: '用户名或密码错误' }) : stubResponse(404, {})))
    renderPage(<Login />, '/login')
    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'whatever-i-typed')
    await user.click(screen.getByRole('button', { name: '登录' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('用户名或密码错误')
    expect(screen.queryByRole('heading', { name: '首页占位' })).not.toBeInTheDocument()
    expect(useAuth.getState().status).not.toBe('in')
    expect(screen.getByLabelText('密码')).toHaveValue('')
    expect(screen.getByRole('button', { name: '登录' })).toBeEnabled()
  })

  it('反向验证：/healthz 200 也不能让错误凭据「登录成功」', async () => {
    const user = userEvent.setup()
    // 公开端点全部 200，只有真正的鉴权端点拒绝
    routeWith((url) => (url === '/api/auth/login'
      ? stubResponse(401, { error: '用户名或密码错误' }) : stubResponse(200, health)))
    renderPage(<Login />, '/login')
    await user.type(screen.getByLabelText('用户名'), 'attacker')
    await user.type(screen.getByLabelText('密码'), 'x')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('用户名或密码错误')
    expect(screen.queryByRole('heading', { name: '首页占位' })).not.toBeInTheDocument()
  })

  it('login 200 但 me 复核失败 → 不算登录成功（会话没真正落地）', async () => {
    const user = userEvent.setup()
    routeWith((url) => {
      if (url === '/api/auth/login') return stubResponse(200, { user: admin })
      if (url === '/api/auth/me') return stubResponse(401, { error: 'not authenticated' })
      return stubResponse(200, health)
    })
    renderPage(<Login />, '/login')
    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'pw')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '首页占位' })).not.toBeInTheDocument()
    expect(useAuth.getState().status).not.toBe('in')
  })

  it('429 → 用服务端 Retry-After 报秒数，按钮禁用并倒计时', async () => {
    const user = userEvent.setup()
    routeWith((url) => (url === '/api/auth/login'
      ? stubResponse(429, { error: '登录尝试过多，请稍后再试' }, { 'Retry-After': '7' })
      : stubResponse(404, {})))
    renderPage(<Login />, '/login')
    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'pw')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('登录尝试过多，请 7 秒后重试')
    const btn = screen.getByRole('button', { name: /请 7 秒后重试/ })
    expect(btn).toBeDisabled()
  })

  it('429 但没有 Retry-After → 不编造秒数', async () => {
    const user = userEvent.setup()
    routeWith((url) => (url === '/api/auth/login'
      ? stubResponse(429, { error: '登录尝试过多' }) : stubResponse(404, {})))
    renderPage(<Login />, '/login')
    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'pw')
    await user.click(screen.getByRole('button', { name: '登录' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('登录尝试过多')
    expect(alert.textContent).not.toMatch(/\d+ 秒/)
  })

  it('server 不可达 → 说明是连接问题，不是账号问题', async () => {
    const user = userEvent.setup()
    installFetch(() => { throw new TypeError('Failed to fetch') })
    renderPage(<Login />, '/login')
    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'pw')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('无法连接 server')
  })

  it('本地必填校验：缺字段就地报错且不发请求', async () => {
    const user = userEvent.setup()
    const http = routeWith(() => stubResponse(200, { user: admin }))
    renderPage(<Login />, '/login')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(screen.getByLabelText('用户名')).toHaveAccessibleDescription('请输入用户名')
    expect(screen.getByLabelText('用户名')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByLabelText('密码')).toHaveAccessibleDescription('请输入密码')
    expect(http.to('/api/auth/login')).toHaveLength(0)
  })

  it('显示/隐藏密码切换 input type 与按钮可读名称', async () => {
    const user = userEvent.setup()
    routeWith(() => stubResponse(404, {}))
    renderPage(<Login />, '/login')
    await user.click(screen.getByRole('button', { name: '显示密码' }))
    expect(screen.getByLabelText('密码')).toHaveAttribute('type', 'text')
    await user.click(screen.getByRole('button', { name: '隐藏密码' }))
    expect(screen.getByLabelText('密码')).toHaveAttribute('type', 'password')
  })

  it('服务令牌是默认折叠的次要入口，展开后才有输入框', async () => {
    const user = userEvent.setup()
    routeWith(() => stubResponse(404, {}))
    renderPage(<Login />, '/login')
    const toggle = screen.getByRole('button', { name: /使用服务令牌登录/ })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByLabelText('服务令牌')).not.toBeInTheDocument()
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByLabelText('服务令牌')).toBeInTheDocument()
  })

  it('服务令牌任意字符串 + healthz 200 → 仍然失败并回滚（不留无效凭据）', async () => {
    const user = userEvent.setup()
    const healthSpy = vi.spyOn(api, 'health')
    routeWith((url) => (url === '/api/auth/me'
      ? stubResponse(401, { error: 'not authenticated' }) : stubResponse(200, health)))
    renderPage(<Login />, '/login')
    await user.click(screen.getByRole('button', { name: /使用服务令牌登录/ }))
    await user.type(screen.getByLabelText('服务令牌'), 'literally-anything')
    await user.click(screen.getByRole('button', { name: '用令牌登录' }))

    expect(await screen.findByText(/令牌被拒绝/)).toBeInTheDocument()
    expect(healthSpy).not.toHaveBeenCalled()
    expect(getToken()).toBe('')
    expect(screen.queryByRole('heading', { name: '首页占位' })).not.toBeInTheDocument()
  })
})

describe('Setup：真实创建首个账号', () => {
  it('第一步探测连通性；已登录时直接给「进入管理台」', async () => {
    routeWith((url) => (url === '/api/auth/me' ? stubResponse(200, { user: admin }) : stubResponse(404, {})))
    renderPage(<Setup />, '/setup')
    expect(await screen.findByText('server 已连接')).toBeInTheDocument()
    expect(screen.getByText('你已经登录了，无需再初始化。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /进入管理台/ })).toBeInTheDocument()
  })

  it('探测失败 → 明确原因 + 重试；「下一步」在连通前禁用', async () => {
    const user = userEvent.setup()
    installFetch(() => { throw new TypeError('Failed to fetch') })
    renderPage(<Setup />, '/setup')
    expect(await screen.findByText('无法连接 server')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /下一步/ })).toBeDisabled()

    routeWith((url) => (url === '/api/auth/me' ? stubResponse(401, {}) : stubResponse(404, {})))
    await user.click(screen.getByRole('button', { name: /重试/ }))
    expect(await screen.findByText('server 已连接')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /下一步/ })).toBeEnabled()
  })

  it('创建账号真的调 POST /api/auth/setup，成功后 me 复核并进入管理台', async () => {
    const user = userEvent.setup()
    const setupSpy = vi.spyOn(api, 'setup')
    // 贴近真实：初始化前未登录（me→401），setup 成功后会话才落地（me→200）
    let created = false
    const http = routeWith((url) => {
      if (url === '/api/auth/setup') { created = true; return stubResponse(200, { user: admin }) }
      if (url === '/api/auth/me') {
        return created ? stubResponse(200, { user: admin }) : stubResponse(401, { error: 'not authenticated' })
      }
      return stubResponse(404, {})
    })
    renderPage(<Setup />, '/setup')
    await user.click(await screen.findByRole('button', { name: /下一步/ }))

    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'pw-12345')
    await user.type(screen.getByLabelText('确认密码'), 'pw-12345')
    await user.click(screen.getByRole('button', { name: /创建账号并继续/ }))

    expect(await screen.findByText('设置完成')).toBeInTheDocument()
    expect(setupSpy).toHaveBeenCalledWith('admin', 'pw-12345')
    expect(http.to('/api/auth/setup')[0]?.method).toBe('POST')
    expect(useAuth.getState().status).toBe('in')

    await user.click(screen.getByRole('button', { name: '进入管理台' }))
    expect(await screen.findByRole('heading', { name: '首页占位' })).toBeInTheDocument()
  })

  it('两次密码不一致 → 本地先拦，不发请求', async () => {
    const user = userEvent.setup()
    const http = routeWith((url) => (url === '/api/auth/me'
      ? stubResponse(401, { error: 'not authenticated' }) : stubResponse(404, {})))
    renderPage(<Setup />, '/setup')
    await user.click(await screen.findByRole('button', { name: /下一步/ }))
    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'aaaa')
    await user.type(screen.getByLabelText('确认密码'), 'bbbb')
    await user.click(screen.getByRole('button', { name: /创建账号并继续/ }))
    expect(screen.getByLabelText('确认密码')).toHaveAccessibleDescription('两次输入的密码不一致')
    expect(http.to('/api/auth/setup')).toHaveLength(0)
  })

  it('403（公网/非回环）→ 说成人话并导流登录页，不甩原始错误', async () => {
    const user = userEvent.setup()
    routeWith((url) => {
      if (url === '/api/auth/setup') return stubResponse(403, { error: 'setup 需要回环来源或一次性 setup token' })
      if (url === '/api/auth/me') return stubResponse(401, {})
      return stubResponse(404, {})
    })
    renderPage(<Setup />, '/setup')
    await user.click(await screen.findByRole('button', { name: /下一步/ }))
    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'pw')
    await user.type(screen.getByLabelText('确认密码'), 'pw')
    await user.click(screen.getByRole('button', { name: /创建账号并继续/ }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('请联系管理员为你创建账号')
    expect(alert.textContent).not.toContain('setup 需要回环来源或一次性 setup token')
    expect(within(alert).getByRole('link', { name: /去登录页/ })).toHaveAttribute('href', '/login')
  })

  it('409（已初始化）→ 说明去登录，不是失败噪音', async () => {
    const user = userEvent.setup()
    routeWith((url) => {
      if (url === '/api/auth/setup') return stubResponse(409, { error: 'already set up' })
      if (url === '/api/auth/me') return stubResponse(401, {})
      return stubResponse(404, {})
    })
    renderPage(<Setup />, '/setup')
    await user.click(await screen.findByRole('button', { name: /下一步/ }))
    await user.type(screen.getByLabelText('用户名'), 'admin')
    await user.type(screen.getByLabelText('密码'), 'pw')
    await user.type(screen.getByLabelText('确认密码'), 'pw')
    await user.click(screen.getByRole('button', { name: /创建账号并继续/ }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('已经初始化过了')
    expect(within(alert).getByRole('link', { name: /去登录页/ })).toBeInTheDocument()
  })
})

describe('路由守卫：me 是登录态唯一事实源', () => {
  // 走完整 App（含路由表）：QueryClient 关掉重试/缓存，401 用例不必等退避。
  // 注意不能套 renderWithProviders —— 它自己已经带了一层 MemoryRouter。
  function renderApp(route: string) {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0, refetchInterval: false, staleTime: 0 } },
    })
    return render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[route]}><App /></MemoryRouter>
      </QueryClientProvider>,
    )
  }

  it('me→401 → 访问受保护路由被收敛到 /login', async () => {
    installFetch((url) => {
      if (url === '/api/auth/me') return stubResponse(401, { error: 'not authenticated' })
      if (url === '/healthz') return stubResponse(200, health)
      return stubResponse(404, {})
    })
    renderApp('/devices')
    expect(await screen.findByRole('heading', { level: 1, name: '登录 Cloudpath' })).toBeInTheDocument()
    expect(useAuth.getState().status).toBe('out')
  })

  it('受保护端点 401 也会全局收敛（markUnauthenticated），把用户送回 /login', async () => {
    // 会话在使用过程中失效：首帧 me→200 放行，随后受保护端点 401，此后 me 也 401。
    // 若不这样安排，App 会在跳转后重新探测 me 并（正确地）把用户送回首页。
    let meCalls = 0
    installFetch((url) => {
      if (url === '/api/auth/me') {
        meCalls++
        return meCalls === 1
          ? stubResponse(200, { user: admin })
          : stubResponse(401, { error: 'not authenticated' })
      }
      if (url === '/api/devices') return stubResponse(401, { error: 'authentication required' })
      if (url === '/healthz') return stubResponse(200, health)
      return stubResponse(404, {})
    })
    renderApp('/devices')
    expect(await screen.findByRole('heading', { level: 1, name: '登录 Cloudpath' })).toBeInTheDocument()
    expect(useAuth.getState().status).toBe('out')
  })

  it('已登录访问 /login → 送回首页', async () => {
    installFetch((url) => {
      if (url.startsWith('/api/auth/me')) return stubResponse(200, { user: admin })
      if (url === '/healthz') return stubResponse(200, health)
      return stubResponse(404, {})
    })
    renderApp('/login')
    expect(await screen.findByRole('heading', { level: 1, name: '概览' })).toBeInTheDocument()
  })
})

describe('认证页主题切换（脱离侧栏时仍可达）', () => {
  it('切换按钮有可读名称，点击后 html 上挂 .dark 且不写内联色值', async () => {
    const user = userEvent.setup()
    routeWith(() => stubResponse(404, {}))
    renderPage(<Login />, '/login')
    const toggle = screen.getByRole('button', { name: '切换为深色外观' })
    expect(document.documentElement).not.toHaveClass('dark')
    await user.click(toggle)
    expect(document.documentElement).toHaveClass('dark')
    expect(screen.getByRole('button', { name: '切换为浅色外观' })).toBeInTheDocument()
    const card = screen.getByRole('heading', { level: 1, name: '登录 Cloudpath' }).closest('.card, div')
    expect(card?.getAttribute('style')).toBeNull()
  })
})