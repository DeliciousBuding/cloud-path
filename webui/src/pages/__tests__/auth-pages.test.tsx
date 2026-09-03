// 认证页（Login / Setup）：表单可达性、错误态、令牌卫生、跳转，以及脱离侧栏时的主题切换。
// 这两页是 390px 上的独立全屏壳，也是「后端认证未就绪」时唯一能被用户看到的东西。
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import type { ReactElement } from 'react'
import Login from '@/pages/Login'
import Setup from '@/pages/Setup'
import { getToken, setToken } from '@/lib/api'
import { installFetch, stubResponse } from '@/test/http'
import { resetStores } from '@/test/render'

const health = { ok: true, version: 'v0.1.0', uptime_s: 60, devices_online: 0, devices_total: 0, edges_online: 0 }

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

beforeEach(() => { resetStores() })

describe('Login', () => {
  it('表单结构可达：标题、带标签的令牌输入、可读按钮名、去设置向导的链接', () => {
    installFetch(() => stubResponse(200, health))
    renderPage(<Login />, '/login')
    expect(screen.getByRole('heading', { level: 1, name: '登录 Cloudpath' })).toBeInTheDocument()
    const input = screen.getByLabelText(/访问令牌/)
    expect(input).toHaveAttribute('type', 'password')
    expect(input).toHaveAccessibleDescription(expect.stringContaining('令牌仅保存在本机浏览器'))
    expect(screen.getByRole('button', { name: '登录' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '显示令牌' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '首次部署？运行设置向导' })).toHaveAttribute('href', '/setup')
  })

  it('空令牌提交 → 就地报错并标记 aria-invalid，不发请求', async () => {
    const user = userEvent.setup()
    const http = installFetch(() => stubResponse(200, health))
    renderPage(<Login />, '/login')
    await user.click(screen.getByRole('button', { name: '登录' }))
    const input = screen.getByLabelText(/访问令牌/)
    expect(input).toHaveAccessibleDescription('请输入访问令牌')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input.className).toContain('input-error')
    expect(http.calls).toHaveLength(0)
  })

  it('显示/隐藏令牌切换 input type 与按钮可读名称', async () => {
    const user = userEvent.setup()
    installFetch(() => stubResponse(200, health))
    renderPage(<Login />, '/login')
    await user.click(screen.getByRole('button', { name: '显示令牌' }))
    expect(screen.getByLabelText(/访问令牌/)).toHaveAttribute('type', 'text')
    await user.click(screen.getByRole('button', { name: '隐藏令牌' }))
    expect(screen.getByLabelText(/访问令牌/)).toHaveAttribute('type', 'password')
  })

  it('server 不可达 → 展示原因并回滚令牌（不留无效凭据在本机）', async () => {
    const user = userEvent.setup()
    installFetch(() => { throw new TypeError('Failed to fetch') })
    renderPage(<Login />, '/login')
    await user.type(screen.getByLabelText(/访问令牌/), 'bad-token')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByText('无法连接 server（服务未启动或网络不可达）')).toBeInTheDocument()
    expect(getToken()).toBe('')
    expect(screen.getByRole('button', { name: '登录' })).toBeEnabled()
  })

  it('连通校验通过 → 保存令牌并跳转首页', async () => {
    const user = userEvent.setup()
    installFetch(() => stubResponse(200, health))
    renderPage(<Login />, '/login')
    await user.type(screen.getByLabelText(/访问令牌/), 'tok-1')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByRole('heading', { name: '首页占位' })).toBeInTheDocument()
    expect(getToken()).toBe('tok-1')
  })

  it('已有令牌时预填，仍可改写', () => {
    setToken('saved-token')
    installFetch(() => stubResponse(200, health))
    renderPage(<Login />, '/login')
    expect(screen.getByLabelText(/访问令牌/)).toHaveValue('saved-token')
  })
})

describe('Setup 向导', () => {
  it('步骤指示器有可读名称与当前步标记（移动端只留圆点也不丢语义）', async () => {
    installFetch(() => stubResponse(200, health))
    renderPage(<Setup />, '/setup')
    const steps = screen.getByRole('list', { name: '设置进度' })
    expect(steps.querySelectorAll('[aria-current="step"]')).toHaveLength(1)
    expect(await screen.findByText('server 已连接')).toBeInTheDocument()
    expect(screen.getByText('版本 v0.1.0')).toBeInTheDocument()
  })

  it('探测失败 → 明确原因 + 重试；「下一步」在连通前禁用', async () => {
    const user = userEvent.setup()
    installFetch(() => { throw new TypeError('Failed to fetch') })
    renderPage(<Setup />, '/setup')
    expect(await screen.findByText('无法连接 server')).toBeInTheDocument()
    expect(screen.getByText('请确认中心服务已启动，且本页面与 server 同源。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /下一步/ })).toBeDisabled()

    installFetch(() => stubResponse(200, health))
    await user.click(screen.getByRole('button', { name: /重试/ }))
    expect(await screen.findByText('server 已连接')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /下一步/ })).toBeEnabled()
  })

  it('三步走完：连通 → 令牌（可跳过）→ 完成并进入管理台', async () => {
    const user = userEvent.setup()
    installFetch(() => stubResponse(200, health))
    renderPage(<Setup />, '/setup')
    await user.click(await screen.findByRole('button', { name: /下一步/ }))

    const token = await screen.findByLabelText(/管理令牌/)
    expect(token).toHaveAccessibleDescription(expect.stringContaining('可跳过'))
    await user.type(token, 'setup-token')
    await user.click(screen.getByRole('button', { name: /保存并继续/ }))

    expect(await screen.findByText('设置完成')).toBeInTheDocument()
    expect(screen.getByText('管理令牌已保存到本机浏览器。')).toBeInTheDocument()
    expect(getToken()).toBe('setup-token')

    await user.click(screen.getByRole('button', { name: '进入管理台' }))
    expect(await screen.findByRole('heading', { name: '首页占位' })).toBeInTheDocument()
  })

  it('跳过令牌时完成页给出诚实提示（不假装已配置）', async () => {
    const user = userEvent.setup()
    installFetch(() => stubResponse(200, health))
    renderPage(<Setup />, '/setup')
    await user.click(await screen.findByRole('button', { name: /下一步/ }))
    await user.click(await screen.findByRole('button', { name: /保存并继续/ }))
    expect(await screen.findByText('尚未配置令牌，可随时在登录页补充。')).toBeInTheDocument()
    expect(getToken()).toBe('')
  })

  it('上一步可回退且不丢连通状态', async () => {
    const user = userEvent.setup()
    installFetch(() => stubResponse(200, health))
    renderPage(<Setup />, '/setup')
    await user.click(await screen.findByRole('button', { name: /下一步/ }))
    await user.click(await screen.findByRole('button', { name: /上一步/ }))
    expect(await screen.findByText('server 已连接')).toBeInTheDocument()
  })
})

describe('认证页主题切换（脱离侧栏时仍可达）', () => {
  it('切换按钮有可读名称，点击后 html 上挂 .dark 且颜色仍走 token', async () => {
    const user = userEvent.setup()
    installFetch(() => stubResponse(200, health))
    renderPage(<Login />, '/login')
    const toggle = screen.getByRole('button', { name: '切换为深色外观' })
    expect(document.documentElement).not.toHaveClass('dark')
    await user.click(toggle)
    expect(document.documentElement).toHaveClass('dark')
    expect(screen.getByRole('button', { name: '切换为浅色外观' })).toBeInTheDocument()
    // 组件内不出现内联色值：主题切换只改 class，颜色由 CSS 变量决定
    const card = screen.getByRole('heading', { level: 1, name: '登录 Cloudpath' }).closest('.card, div')
    expect(card?.getAttribute('style')).toBeNull()
  })
})