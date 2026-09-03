// 管理页 · 令牌面（docs/api.md §3.3）。
// 核心不变量：明文只活在一次性的组件内存里 —— 面板关闭/组件卸载即消失，
// 永不进 localStorage / sessionStorage / console / URL / toast 文本；列表只有 prefix 与元数据。
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Admin from '@/pages/Admin'
import { useAuth } from '@/store/auth'
import { useToasts } from '@/store/toast'
import { installFetch, stubResponse, type FetchRoute } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import type { CreatedToken, TokenView, UserView } from '@/lib/types'

const health = { ok: true, version: 'v0.1.0', uptime_s: 60, devices_online: 0, devices_total: 0, edges_online: 0 }
const root: UserView = { id: 1, username: 'root', name: 'Root', role: 'admin', tenant_id: 1, tenant_slug: 'local' }

const SECRET = 'cp_7d4f1a9c2e8b4d6f0a3c5e7b9d1f2a4c'
const PREFIX = 'cp_7d4f'
const tok: TokenView = {
  id: 7, name: 'ci-deploy', prefix: PREFIX, scopes: ['read'], created_at: 1_700_000_000,
}
const created: CreatedToken = { ...tok, token: SECRET }

interface Opts {
  tokens?: TokenView[]
  createStatus?: number
  createBody?: unknown
}

function tokenRoute(o: Opts = {}): FetchRoute {
  return (url, init) => {
    const m = (init?.method ?? 'GET').toUpperCase()
    if (url === '/healthz') return stubResponse(200, health)
    if (url === '/api/users' && m === 'GET') return stubResponse(200, { users: [root] })
    if (url === '/api/tokens' && m === 'GET') return stubResponse(200, { tokens: o.tokens ?? [tok] })
    if (url === '/api/tokens' && m === 'POST') {
      return stubResponse(o.createStatus ?? 200, o.createBody ?? created)
    }
    if (url.startsWith('/api/tokens/') && m === 'DELETE') return stubResponse(204)
    return stubResponse(404, { error: 'not found' })
  }
}

const asAdmin = () => useAuth.setState({ status: 'in', user: root })

/** 打开创建表单并提交。不断言结果：成功看 dialog，失败看 alert，由调用方决定 */
async function submitCreateToken(user: ReturnType<typeof userEvent.setup>, name = 'ci-deploy') {
  await user.click(await screen.findByRole('button', { name: '新建令牌' }))
  const form = screen.getByRole('form', { name: '新建服务令牌' })
  await user.type(within(form).getByLabelText('令牌名称'), name)
  await user.click(within(form).getByRole('button', { name: '创建令牌' }))
}

/** 两份 Web Storage 的全部键值拼成一个字符串（反向断言用） */
function storageDump(): string {
  const out: string[] = []
  for (const s of [window.localStorage, window.sessionStorage]) {
    for (let i = 0; i < s.length; i++) {
      const k = s.key(i)
      if (k) out.push(`${k}=${s.getItem(k) ?? ''}`)
    }
  }
  return out.join('\n')
}

beforeEach(() => { resetStores() })

describe('TestScopeDefaultsLeastPrivilege', () => {
  it('scope 默认只勾 read；write/admin/edge 不预选，且危险范围有说明', async () => {
    asAdmin()
    const user = userEvent.setup()
    installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '新建令牌' }))
    const group = within(screen.getByRole('form', { name: '新建服务令牌' }))
      .getByRole('group', { name: /权限范围/ })

    expect(within(group).getByRole('checkbox', { name: 'read' })).toBeChecked()
    for (const s of ['write', 'admin', 'edge']) {
      expect(within(group).getByRole('checkbox', { name: s })).not.toBeChecked()
    }
    // 危险范围的说明文案必须可见（不是藏在 title 里）
    expect(within(group).getByText(/等同管理员权限/)).toBeInTheDocument()
    expect(within(group).getByText(/允许边缘代理以本租户身份接入/)).toBeInTheDocument()
  })

  it('用默认值提交 → body scopes 只有 read，expires_at 走默认 30 天', async () => {
    asAdmin()
    const user = userEvent.setup()
    const http = installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    const before = Math.floor(Date.now() / 1000)
    await submitCreateToken(user)
    await waitFor(() => expect(http.to('/api/tokens').some((c) => c.method === 'POST')).toBe(true))

    const body = http.to('/api/tokens').find((c) => c.method === 'POST')?.body as
      { name: string; scopes: string[]; expires_at?: number }
    expect(body.name).toBe('ci-deploy')
    expect(body.scopes).toEqual(['read'])
    expect(body.expires_at).toBeGreaterThan(before + 29 * 86_400)
    expect(body.expires_at).toBeLessThan(before + 31 * 86_400)
  })

  it('选「永不过期」→ expires_at 字段不出现在 body 里', async () => {
    asAdmin()
    const user = userEvent.setup()
    const http = installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '新建令牌' }))
    const form = screen.getByRole('form', { name: '新建服务令牌' })
    await user.type(within(form).getByLabelText('令牌名称'), 'edge-a')
    await user.selectOptions(within(form).getByLabelText('有效期'), 'never')
    await user.click(within(form).getByRole('button', { name: '创建令牌' }))

    await waitFor(() => expect(http.to('/api/tokens').some((c) => c.method === 'POST')).toBe(true))
    const body = http.to('/api/tokens').find((c) => c.method === 'POST')?.body as Record<string, unknown>
    expect(body).toEqual({ name: 'edge-a', scopes: ['read'] })
    expect(body).not.toHaveProperty('expires_at')
  })

  it('scope 全不勾 → 本地先拦（不发请求），并说明服务端要求非空', async () => {
    asAdmin()
    const user = userEvent.setup()
    const http = installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '新建令牌' }))
    const form = screen.getByRole('form', { name: '新建服务令牌' })
    await user.type(within(form).getByLabelText('令牌名称'), 'ci-deploy')
    await user.click(within(form).getByRole('checkbox', { name: 'read' }))
    await user.click(within(form).getByRole('button', { name: '创建令牌' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('至少选择一个权限范围')
    expect(http.to('/api/tokens').filter((c) => c.method === 'POST')).toHaveLength(0)
  })
})

describe('TestCreateTokenSecretShownOnce', () => {
  it('创建后弹出一次性面板：明文可见可复制，正文与列表都不含明文', async () => {
    asAdmin()
    const user = userEvent.setup()
    installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    await submitCreateToken(user)
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(within(dialog).getByLabelText('令牌明文')).toHaveValue(SECRET)
    expect(within(dialog).getByText(/明文只显示这一次/)).toBeInTheDocument()
    expect(within(dialog).getByText(/无法再次查看/)).toBeInTheDocument()

    // 明文只以只读输入框的 value 存在：页面正文（textContent）里没有它
    expect(document.body.textContent).not.toContain(SECRET)
    expect(screen.queryByText(SECRET)).toBeNull()
    // 列表里只有 prefix 与元数据
    expect(screen.getAllByText(PREFIX).length).toBeGreaterThan(0)
    expect(screen.getByRole('list', { name: '服务令牌列表' })).toBeInTheDocument()
    // toast 只带名称，不带明文
    expect(JSON.stringify(useToasts.getState().items)).not.toContain(SECRET)
  })

  it('关闭面板后 DOM 里再也找不到明文，也不能从列表恢复', async () => {
    asAdmin()
    const user = userEvent.setup()
    installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    await submitCreateToken(user)
    await screen.findByRole('dialog')
    await user.click(screen.getByRole('button', { name: '我已保存，关闭令牌明文' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(document.body.textContent).not.toContain(SECRET)
    expect(document.body.innerHTML).not.toContain(SECRET)
    // 列表仍在，但只有元数据：没有任何「查看明文」入口
    expect(screen.getByRole('list', { name: '服务令牌列表' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /查看明文|显示令牌/ })).toBeNull()
  })

  it('Esc 关闭面板同样丢弃明文', async () => {
    asAdmin()
    const user = userEvent.setup()
    installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    await submitCreateToken(user)
    await screen.findByRole('dialog')
    await user.keyboard('{Escape}')

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(document.body.innerHTML).not.toContain(SECRET)
  })

  it('复制给出可访问状态提示（role=status + aria-live）', async () => {
    asAdmin()
    const user = userEvent.setup()
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    await submitCreateToken(user)
    const dialog = await screen.findByRole('dialog')
    const status = within(dialog).getByRole('status')
    expect(status).toHaveAttribute('aria-live', 'polite')
    expect(status).toHaveTextContent('复制后请立即粘贴到安全位置')

    await user.click(within(dialog).getByRole('button', { name: '复制令牌明文' }))
    expect(await within(dialog).findByText('已复制到剪贴板')).toBeInTheDocument()
    expect(writeText).toHaveBeenCalledWith(SECRET)

    delete (navigator as { clipboard?: unknown }).clipboard
  })

  it('剪贴板被拒（非安全上下文/权限）时明确说明失败，并保留可手动选择的只读明文', async () => {
    asAdmin()
    const user = userEvent.setup()
    // userEvent 会给 jsdom 装一个可用的 clipboard 替身，这里显式换成「拒绝」的现实：
    // 浏览器在非安全上下文或权限被拒时 writeText 会 reject（NotAllowedError）
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: vi.fn(async () => { throw new Error('NotAllowedError') }) },
    })
    installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    await submitCreateToken(user)
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '复制令牌明文' }))
    expect(await within(dialog).findByText(/复制失败：请手动选中上方文本复制/)).toBeInTheDocument()
    expect(within(dialog).getByLabelText('令牌明文')).toHaveValue(SECRET)
    expect(within(dialog).getByRole('status')).toHaveAttribute('aria-live', 'polite')

    delete (navigator as { clipboard?: unknown }).clipboard
  })
})

describe('TestTokenNeverPersistedClientSide', () => {
  it('明文不进 localStorage / sessionStorage / console / URL / toast（反向断言）', async () => {
    asAdmin()
    const user = userEvent.setup()
    installFetch(tokenRoute())

    const writes: string[] = []
    vi.spyOn(Storage.prototype, 'setItem')
      .mockImplementation((key: string, value: string) => { writes.push(`${key}=${value}`) })
    const consoleText: string[] = []
    for (const k of ['log', 'info', 'warn', 'error', 'debug'] as const) {
      vi.spyOn(console, k).mockImplementation((...args: unknown[]) => {
        consoleText.push(args.map((a) => String(a)).join(' '))
      })
    }
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })

    renderWithProviders(<Admin />)
    await submitCreateToken(user)
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '复制令牌明文' }))
    await within(dialog).findByText('已复制到剪贴板')
    await user.click(screen.getByRole('button', { name: '我已保存，关闭令牌明文' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    // 剪贴板是唯一允许的明文出口
    expect(writeText).toHaveBeenCalledWith(SECRET)
    expect(writes.join('\n')).not.toContain(SECRET)
    expect(storageDump()).not.toContain(SECRET)
    expect(storageDump()).not.toContain(PREFIX)
    expect(consoleText.join('\n')).not.toContain(SECRET)
    expect(`${location.href}${location.search}${location.hash}`).not.toContain(SECRET)
    expect(JSON.stringify(useToasts.getState().items)).not.toContain(SECRET)
    expect(document.body.innerHTML).not.toContain(SECRET)

    delete (navigator as { clipboard?: unknown }).clipboard
  })

  it('明文不出现在任何请求的 URL 或查询串里（只走 POST body 的服务端方向）', async () => {
    asAdmin()
    const user = userEvent.setup()
    const http = installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    await submitCreateToken(user)
    await screen.findByRole('dialog')
    for (const c of http.calls) {
      expect(c.url).not.toContain(SECRET)
      expect(c.url).not.toContain('token=')
    }
  })
})

describe('TestRevokeToken', () => {
  it('吊销走两步确认：先给人话警告且不发请求，确认后 DELETE /api/tokens/{id}', async () => {
    asAdmin()
    const user = userEvent.setup()
    let revoked = false
    const http = installFetch((url, init) => {
      const m = (init?.method ?? 'GET').toUpperCase()
      if (url === '/healthz') return stubResponse(200, health)
      if (url === '/api/users' && m === 'GET') return stubResponse(200, { users: [root] })
      if (url === '/api/tokens' && m === 'GET') {
        return stubResponse(200, { tokens: [revoked ? { ...tok, revoked_at: 1_700_000_900 } : tok] })
      }
      if (url.startsWith('/api/tokens/') && m === 'DELETE') { revoked = true; return stubResponse(204) }
      return stubResponse(404, { error: 'not found' })
    })
    renderWithProviders(<Admin />)

    const list = await screen.findByRole('list', { name: '服务令牌列表' })
    expect(within(list).getByText('有效')).toBeInTheDocument()

    await user.click(within(list).getByRole('button', { name: '吊销令牌 ci-deploy' }))
    expect(screen.getByText(/确认吊销「ci-deploy」/)).toBeInTheDocument()
    expect(screen.getByText(/立即失效且无法恢复/)).toBeInTheDocument()
    expect(http.to('/api/tokens/7')).toHaveLength(0)

    await user.click(screen.getByRole('button', { name: '确认吊销令牌 ci-deploy' }))
    await waitFor(() => expect(http.to('/api/tokens/7')).toHaveLength(1))
    expect(http.to('/api/tokens/7')[0]?.method).toBe('DELETE')
    expect(http.to('/api/tokens/7')[0]?.credentials).toBe('same-origin')

    // 列表刷新为已吊销，并且不再有吊销按钮
    expect(await within(list).findByText('已吊销')).toBeInTheDocument()
    expect(within(list).queryByRole('button', { name: '吊销令牌 ci-deploy' })).toBeNull()
    expect(within(list).getByText(/不能恢复；需要时请新建一个/)).toBeInTheDocument()
  })

  it('取消确认 → 不发请求，状态保持有效', async () => {
    asAdmin()
    const user = userEvent.setup()
    const http = installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    const list = await screen.findByRole('list', { name: '服务令牌列表' })
    await user.click(within(list).getByRole('button', { name: '吊销令牌 ci-deploy' }))
    await user.click(screen.getByRole('button', { name: '取消吊销 ci-deploy' }))

    expect(http.to('/api/tokens/7')).toHaveLength(0)
    expect(screen.queryByText(/确认吊销「ci-deploy」/)).toBeNull()
    expect(within(list).getByText('有效')).toBeInTheDocument()
  })

  it('过期令牌标记为「已过期」，仍可吊销以留下审计痕迹', async () => {
    asAdmin()
    const user = userEvent.setup()
    const expired: TokenView = { ...tok, name: 'old-ci', expires_at: Math.floor(Date.now() / 1000) - 60 }
    const http = installFetch(tokenRoute({ tokens: [expired] }))
    renderWithProviders(<Admin />)

    const list = await screen.findByRole('list', { name: '服务令牌列表' })
    expect(within(list).getByText('已过期')).toBeInTheDocument()
    expect(within(list).queryByText('已吊销')).toBeNull()

    await user.click(within(list).getByRole('button', { name: '吊销令牌 old-ci' }))
    await user.click(screen.getByRole('button', { name: '确认吊销令牌 old-ci' }))
    await waitFor(() => expect(http.to('/api/tokens/7')).toHaveLength(1))
    expect(http.to('/api/tokens/7')[0]?.method).toBe('DELETE')
  })

  it('创建被拒（403）→ 展示人话提示，且不产生任何明文面板', async () => {
    asAdmin()
    const user = userEvent.setup()
    installFetch(tokenRoute({ createStatus: 403, createBody: { error: 'permission denied' } }))
    renderWithProviders(<Admin />)

    await submitCreateToken(user)
    expect(await screen.findByRole('alert')).toHaveTextContent('权限不足：该操作需要管理员角色')
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(document.body.innerHTML).not.toContain(SECRET)
  })
})

describe('令牌列表卫生', () => {
  it('列表只展示 prefix/名称/范围/时间，从不展示明文', async () => {
    asAdmin()
    installFetch(tokenRoute())
    renderWithProviders(<Admin />)

    const list = await screen.findByRole('list', { name: '服务令牌列表' })
    expect(within(list).getByText('ci-deploy')).toBeInTheDocument()
    expect(within(list).getByText(PREFIX)).toBeInTheDocument()
    expect(within(list).getByText('read')).toBeInTheDocument()
    expect(within(list).getByText('从未使用')).toBeInTheDocument()
    expect(within(list).getByText('永不过期')).toBeInTheDocument()
    expect(list.textContent).not.toContain(SECRET)
  })
})