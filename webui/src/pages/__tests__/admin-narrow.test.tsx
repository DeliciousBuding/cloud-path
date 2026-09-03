// TestAdminPanelsAt390px：管理面承载的长文本（用户名、显示名、租户 slug、令牌名、prefix、
// 以及把名字插进句子的确认文案）长度都不可控，必须自身截断/可断行，
// 否则一个 64 字符的名字就能把 body 顶出横向滚动。判据与 narrow-viewport.test.tsx 共用。
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import Admin from '@/pages/Admin'
import { useAuth } from '@/store/auth'
import { installFetch, stubResponse } from '@/test/http'
import { LONG, expectContained, expectNoInlinePixelWidth, expectSelfGuarded } from '@/test/narrow'
import { renderWithProviders, resetStores } from '@/test/render'
import type { CreatedToken, TokenView, UserView } from '@/lib/types'

const adminLong: UserView = {
  id: 1, username: LONG, name: LONG, role: 'admin', tenant_id: 1, tenant_slug: LONG,
}
const tokenLong: TokenView = {
  id: 2, name: LONG, prefix: LONG, scopes: ['read', 'admin'], created_at: 1_700_000_000,
}
const secretLong: CreatedToken = { ...tokenLong, id: 3, token: `cp_${LONG}` }

function installLongFetch() {
  return installFetch((url, init) => {
    const m = (init?.method ?? 'GET').toUpperCase()
    if (url === '/api/users' && m === 'GET') return stubResponse(200, { users: [adminLong] })
    if (url === '/api/tokens' && m === 'GET') return stubResponse(200, { tokens: [tokenLong] })
    if (url === '/api/tokens' && m === 'POST') return stubResponse(200, secretLong)
    return stubResponse(404, { error: 'not found' })
  })
}

beforeEach(() => {
  resetStores()
  useAuth.setState({ status: 'in', user: adminLong })
})

describe('TestAdminPanelsAt390px', () => {
  it('用户卡片与令牌卡片的长文本都收口，且不写内联像素宽度', async () => {
    installLongFetch()
    renderWithProviders(<Admin />)

    await screen.findByRole('list', { name: '用户列表' })
    await screen.findByRole('list', { name: '服务令牌列表' })
    await screen.findAllByText(LONG)

    expectContained(LONG)
    expectNoInlinePixelWidth()

    // 两块面板在窄屏是单列堆叠（xl 才并排），页面容器不给固定宽度
    const grid = document.querySelector('div.grid') as HTMLElement
    expect(grid.className).toContain('xl:grid-cols-2')
    expect(grid.className).not.toMatch(/\bw-\[\d+px\]/)
    // 列表自身不横滚：截断由每个文本容器负责
    const users = screen.getByRole('list', { name: '用户列表' })
    expect(within(users).getAllByText(LONG).length).toBeGreaterThan(0)
  })

  it('把长名字插进句子的内联确认文案自身可断行', async () => {
    const user = userEvent.setup()
    installLongFetch()
    renderWithProviders(<Admin />)

    const tokens = await screen.findByRole('list', { name: '服务令牌列表' })
    await user.click(within(tokens).getByRole('button', { name: `吊销令牌 ${LONG}` }))
    expectSelfGuarded(screen.getByText(/确认吊销「/))

    const users = screen.getByRole('list', { name: '用户列表' })
    await user.click(within(users).getByRole('button', { name: `重置密码：${LONG}` }))
    expectSelfGuarded(screen.getByText(/重置后该用户的全部会话会被撤销/))
    expectNoInlinePixelWidth()
  })

  it('创建表单与一次性明文面板在 390px 同样收口', async () => {
    const user = userEvent.setup()
    installLongFetch()
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '新建令牌' }))
    const form = screen.getByRole('form', { name: '新建服务令牌' })
    await user.type(within(form).getByLabelText('令牌名称'), LONG)
    await user.click(within(form).getByRole('button', { name: '创建令牌' }))

    const dialog = await screen.findByRole('dialog')
    // 明文在只读输入框里（value，不是文本节点），自身可断行；面板不写死宽度
    const secret = within(dialog).getByLabelText('令牌明文')
    expect(secret).toHaveValue(`cp_${LONG}`)
    expect(String(secret.className)).toMatch(/break-all|min-w-0/)
    expect(String(dialog.className)).not.toMatch(/\bw-\[\d+px\]/)
    expect(document.body.textContent).not.toContain(`cp_${LONG}`)
    expectNoInlinePixelWidth()
  })

  it('深色模式下管理面照常渲染（token 齐全，不掉色）', async () => {
    document.documentElement.classList.add('dark')
    installLongFetch()
    renderWithProviders(<Admin />)
    await screen.findByRole('list', { name: '用户列表' })
    expectContained(LONG)
  })
})