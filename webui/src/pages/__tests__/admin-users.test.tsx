// 管理页 · 用户面（docs/api.md §3.2）。
// 三件事必须成立：admin 能管（创建/改角色/禁用/重置密码）、非 admin 零渲染（不是 disabled）、
// 最后一个 admin 的 409 用服务端人话展示（前端不做本地伪判）。
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import Layout from '@/components/Layout'
import Admin from '@/pages/Admin'
import { useAuth } from '@/store/auth'
import { installFetch, stubResponse, type FetchRoute } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import type { UserView } from '@/lib/types'

const health = { ok: true, version: 'v0.1.0', uptime_s: 60, devices_online: 0, devices_total: 0, edges_online: 0 }

const root: UserView = { id: 1, username: 'root', name: 'Root', role: 'admin', tenant_id: 1, tenant_slug: 'local' }
const ops: UserView = { id: 2, username: 'ops', name: 'Ops', role: 'operator', tenant_id: 1, tenant_slug: 'local' }
const vic: UserView = { id: 9, username: 'vic', name: 'Vic', role: 'viewer', tenant_id: 1, tenant_slug: 'local' }

interface Opts {
  users?: UserView[]
  usersStatus?: number
  createStatus?: number
  createBody?: unknown
  patchStatus?: number
  patchBody?: unknown
}

function adminRoute(o: Opts = {}): FetchRoute {
  return (url, init) => {
    const m = (init?.method ?? 'GET').toUpperCase()
    if (url === '/healthz') return stubResponse(200, health)
    if (url === '/api/users' && m === 'GET') {
      return stubResponse(o.usersStatus ?? 200, o.usersStatus ? { error: 'nope' } : { users: o.users ?? [root, ops] })
    }
    if (url === '/api/users' && m === 'POST') {
      return stubResponse(o.createStatus ?? 200, o.createBody ?? { user: { ...vic, id: 3, username: 'newbie', name: 'newbie' } })
    }
    if (url.startsWith('/api/users/') && m === 'PATCH') {
      return stubResponse(o.patchStatus ?? 200, o.patchBody ?? { user: ops })
    }
    if (url === '/api/tokens' && m === 'GET') return stubResponse(200, { tokens: [] })
    return stubResponse(404, { error: 'not found' })
  }
}

const asAdmin = () => useAuth.setState({ status: 'in', user: root })

beforeEach(() => { resetStores() })

describe('TestAdminCanManageUsers', () => {
  it('admin 看到本租户用户列表（含角色与租户，永不含密码哈希）', async () => {
    asAdmin()
    installFetch(adminRoute())
    renderWithProviders(<Admin />)

    const list = await screen.findByRole('list', { name: '用户列表' })
    expect(within(list).getByText('root')).toBeInTheDocument()
    expect(within(list).getByText('ops')).toBeInTheDocument()
    expect(within(list).getByText('管理员')).toBeInTheDocument()
    expect(within(list).getByText('操作员')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: '管理' })).toBeInTheDocument()
    expect(document.body.textContent).not.toMatch(/password_hash|\$2a\$|bcrypt/i)
  })

  it('新建用户：角色默认最小权限 viewer，POST 只带 {username,role,password}', async () => {
    asAdmin()
    const user = userEvent.setup()
    const http = installFetch(adminRoute())
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '新建用户' }))
    const form = screen.getByRole('form', { name: '新建用户' })
    expect(within(form).getByLabelText('角色')).toHaveValue('viewer')

    await user.type(within(form).getByLabelText('用户名'), 'newbie')
    await user.type(within(form).getByLabelText('密码'), 'pw-12345')
    await user.click(within(form).getByRole('button', { name: '创建用户' }))

    await waitFor(() => expect(http.to('/api/users').some((c) => c.method === 'POST')).toBe(true))
    const post = http.to('/api/users').find((c) => c.method === 'POST')
    expect(post?.body).toEqual({ username: 'newbie', role: 'viewer', password: 'pw-12345' })
    expect(post?.credentials).toBe('same-origin')
    // 名称留空 → 字段不出现（服务端回落为 username），不发空串
    expect(post?.body).not.toHaveProperty('name')
  })

  it('新建用户：空用户名/空密码就地报错并标记 aria-invalid，不发请求', async () => {
    asAdmin()
    const user = userEvent.setup()
    const http = installFetch(adminRoute())
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '新建用户' }))
    const form = screen.getByRole('form', { name: '新建用户' })
    await user.click(within(form).getByRole('button', { name: '创建用户' }))

    expect(within(form).getByLabelText('用户名')).toHaveAttribute('aria-invalid', 'true')
    expect(within(form).getByLabelText('密码')).toHaveAttribute('aria-invalid', 'true')
    expect(within(form).getByText('请输入用户名')).toBeInTheDocument()
    expect(http.to('/api/users').filter((c) => c.method === 'POST')).toHaveLength(0)
  })

  it('改角色 + 禁用：PATCH /api/users/{id} 带 {name,role,disabled}', async () => {
    asAdmin()
    const user = userEvent.setup()
    const http = installFetch(adminRoute())
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '编辑用户 ops' }))
    const form = screen.getByRole('form', { name: '编辑用户 ops' })
    await user.selectOptions(within(form).getByLabelText('角色'), 'admin')
    await user.click(within(form).getByRole('checkbox', { name: '禁用该账号' }))
    await user.click(within(form).getByRole('button', { name: '保存用户 ops 的修改' }))

    await waitFor(() => expect(http.to('/api/users/2')).toHaveLength(1))
    expect(http.to('/api/users/2')[0]).toMatchObject({ method: 'PATCH' })
    expect(http.to('/api/users/2')[0]?.body).toEqual({ name: 'Ops', role: 'admin', disabled: true })
  })

  it('重置密码：未勾选确认时提交按钮禁用，PATCH 只带 {password}', async () => {
    asAdmin()
    const user = userEvent.setup()
    const http = installFetch(adminRoute())
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '重置密码：ops' }))
    const form = screen.getByRole('form', { name: '重置 ops 的密码' })
    const submit = within(form).getByRole('button', { name: '确认重置 ops 的密码' })
    expect(submit).toBeDisabled()

    await user.type(within(form).getByLabelText('新密码'), 'new-pw-1')
    await user.click(within(form).getByRole('checkbox', { name: '确认重置 ops 的密码' }))
    expect(submit).toBeEnabled()
    await user.click(submit)

    await waitFor(() => expect(http.to('/api/users/2')).toHaveLength(1))
    expect(http.to('/api/users/2')[0]?.body).toEqual({ password: 'new-pw-1' })
  })

  it('重置密码：空密码就地报错，不发请求', async () => {
    asAdmin()
    const user = userEvent.setup()
    const http = installFetch(adminRoute())
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '重置密码：ops' }))
    const form = screen.getByRole('form', { name: '重置 ops 的密码' })
    await user.click(within(form).getByRole('checkbox', { name: '确认重置 ops 的密码' }))
    await user.click(within(form).getByRole('button', { name: '确认重置 ops 的密码' }))

    expect(within(form).getByText('请输入新密码')).toBeInTheDocument()
    expect(http.to('/api/users/2')).toHaveLength(0)
  })

  it('PATCH 403 → 就地展示「权限不足」人话，表单不关闭', async () => {
    asAdmin()
    const user = userEvent.setup()
    installFetch(adminRoute({ patchStatus: 403, patchBody: { error: 'permission denied' } }))
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '编辑用户 ops' }))
    const form = screen.getByRole('form', { name: '编辑用户 ops' })
    await user.click(within(form).getByRole('button', { name: '保存用户 ops 的修改' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('权限不足：该操作需要管理员角色')
    expect(screen.getByRole('form', { name: '编辑用户 ops' })).toBeInTheDocument()
  })

  it('列表 401 → 全局收敛为未登录，管理面立即收回（不留在页面上）', async () => {
    asAdmin()
    installFetch(adminRoute({ usersStatus: 401 }))
    renderWithProviders(<Admin />)

    expect(await screen.findByText('需要管理员权限')).toBeInTheDocument()
    expect(useAuth.getState().status).toBe('out')
    expect(screen.queryByRole('list', { name: '用户列表' })).toBeNull()
  })
})

describe('TestLastAdminConflictShown', () => {
  it('409 原样展示服务端人话，并且请求真的发出去了（无本地伪判）', async () => {
    asAdmin()
    const user = userEvent.setup()
    const msg = '不能禁用或降级最后一个可用 admin'
    const http = installFetch(adminRoute({ users: [root], patchStatus: 409, patchBody: { error: msg } }))
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '编辑用户 root' }))
    const form = screen.getByRole('form', { name: '编辑用户 root' })
    await user.selectOptions(within(form).getByLabelText('角色'), 'viewer')
    await user.click(within(form).getByRole('button', { name: '保存用户 root 的修改' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(msg)
    // 前端没有「本地判定最后一个 admin 就不发请求」：请求发出去了，由 server 拒绝
    expect(http.to('/api/users/1')).toHaveLength(1)
    expect(http.to('/api/users/1')[0]?.body).toMatchObject({ role: 'viewer' })
    // 失败后表单仍在，用户可以直接改回来
    expect(screen.getByRole('form', { name: '编辑用户 root' })).toBeInTheDocument()
  })

  it('禁用最后一个 admin 的 409 同样透传人话', async () => {
    asAdmin()
    const user = userEvent.setup()
    const msg = '不能禁用或降级最后一个可用 admin'
    installFetch(adminRoute({ users: [root], patchStatus: 409, patchBody: { error: msg } }))
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '编辑用户 root' }))
    const form = screen.getByRole('form', { name: '编辑用户 root' })
    await user.click(within(form).getByRole('checkbox', { name: '禁用该账号' }))
    await user.click(within(form).getByRole('button', { name: '保存用户 root 的修改' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(msg)
  })

  it('创建重名用户 409 → 展示「username 已存在」', async () => {
    asAdmin()
    const user = userEvent.setup()
    installFetch(adminRoute({ createStatus: 409, createBody: { error: 'username 已存在' } }))
    renderWithProviders(<Admin />)

    await user.click(await screen.findByRole('button', { name: '新建用户' }))
    const form = screen.getByRole('form', { name: '新建用户' })
    await user.type(within(form).getByLabelText('用户名'), 'ops')
    await user.type(within(form).getByLabelText('密码'), 'pw-12345')
    await user.click(within(form).getByRole('button', { name: '创建用户' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('username 已存在')
  })
})

describe('TestNonAdminCannotSeeManagement', () => {
  const nonAdminCases: [string, UserView][] = [['viewer', vic], ['operator', ops]]

  it.each(nonAdminCases)('%s 登录 → 管理面零渲染，且不发任何管理请求', async (_role, me) => {
    useAuth.setState({ status: 'in', user: me })
    const http = installFetch(adminRoute())
    renderWithProviders(<Admin />)

    expect(await screen.findByText('需要管理员权限')).toBeInTheDocument()
    expect(screen.queryByRole('list', { name: '用户列表' })).toBeNull()
    expect(screen.queryByRole('list', { name: '服务令牌列表' })).toBeNull()
    expect(screen.queryByRole('button', { name: '新建用户' })).toBeNull()
    expect(screen.queryByRole('button', { name: '新建令牌' })).toBeNull()
    expect(screen.queryByRole('button', { name: /编辑用户/ })).toBeNull()
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.queryByText('root')).toBeNull()
    expect(http.to('/api/users')).toHaveLength(0)
    expect(http.to('/api/tokens')).toHaveLength(0)
  })

  it('开放访问（me 不可用，user=null）同样不渲染管理面', async () => {
    useAuth.setState({ status: 'open', user: null })
    const http = installFetch(adminRoute())
    renderWithProviders(<Admin />)

    expect(await screen.findByText('需要管理员权限')).toBeInTheDocument()
    expect(http.to('/api/users')).toHaveLength(0)
    expect(http.to('/api/tokens')).toHaveLength(0)
  })

  it('登录态探测中不抢先渲染敏感列表', () => {
    useAuth.setState({ status: 'loading', user: null })
    installFetch(adminRoute())
    renderWithProviders(<Admin />)
    expect(screen.queryByRole('list', { name: '用户列表' })).toBeNull()
    expect(screen.queryByRole('button', { name: '新建用户' })).toBeNull()
  })

  it('侧栏「管理」入口只对 admin 出现（viewer 连链接都没有）', async () => {
    useAuth.setState({ status: 'in', user: vic })
    installFetch((url) => (url === '/healthz' ? stubResponse(200, health) : stubResponse(404, {})))
    renderWithProviders(
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Admin />} />
        </Route>
      </Routes>,
    )
    expect(screen.getByRole('heading', { level: 1, name: '管理' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '管理' })).toBeNull()
    // 其余入口照旧（桌面侧栏 + 移动端顶栏各一份）
    expect(screen.getAllByRole('link', { name: '系统' })).toHaveLength(2)
  })

  it('admin 时侧栏与移动端顶栏各出现一个「管理」入口', async () => {
    asAdmin()
    installFetch((url) => (url === '/healthz' ? stubResponse(200, health) : stubResponse(404, {})))
    renderWithProviders(
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Admin />} />
        </Route>
      </Routes>,
    )
    await waitFor(() => expect(screen.getAllByRole('link', { name: '管理' })).toHaveLength(2))
    expect(screen.getAllByRole('link', { name: '管理' })[0]).toHaveAttribute('href', '/admin')
  })
})