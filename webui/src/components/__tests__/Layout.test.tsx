// Layout：地标与跳转链接、实时通道状态提示、外观主题控件、轻提示播报区，
// 以及 390px 的溢出收口（移动端导航局部滚动、内容宽度用 max-w 而不是固定宽度）。
import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import Layout from '@/components/Layout'
import { toast } from '@/store/toast'
import { useLive } from '@/store/ws'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'

const health = { ok: true, version: 'v0.1.0', uptime_s: 60, devices_online: 0, devices_total: 0, edges_online: 0 }

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
  installFetch((url) => (url === '/healthz' ? stubResponse(200, health) : stubResponse(404, {})))
})

describe('地标与键盘入口', () => {
  it('有 main 地标、跳转链接与主导航', () => {
    renderLayout()
    const main = screen.getByRole('main')
    expect(main).toHaveAttribute('id', 'main')
    const skip = screen.getByRole('link', { name: '跳到主内容' })
    expect(skip).toHaveAttribute('href', '#main')
    // 桌面侧栏 + 移动端顶栏各一份同名导航；真实视口下 CSS 只暴露其一
    expect(screen.getAllByRole('navigation', { name: '主导航' })).toHaveLength(2)
    expect(screen.getByRole('heading', { level: 1, name: '页面内容' })).toBeInTheDocument()
  })

  it('品牌区是可读名称的链接，导航项带 title', () => {
    renderLayout()
    expect(screen.getAllByRole('link', { name: 'Cloudpath 概览' }).length).toBeGreaterThan(0)
    const nav = screen.getAllByRole('navigation', { name: '主导航' })[0] as HTMLElement
    expect(within(nav).getByRole('link', { name: /设备/ })).toHaveAttribute('title', '设备')
  })

  it('侧栏底部给出 server 版本（运维定位用）', async () => {
    renderLayout()
    expect(await screen.findByText('server v0.1.0')).toBeInTheDocument()
  })
})

describe('实时通道状态提示', () => {
  it('断开时给出系统级提示条并说明会自动重连', () => {
    renderLayout()
    expect(screen.getByText(/实时通道已断开，正在自动重连/)).toBeInTheDocument()
    expect(screen.getAllByText('已断开').length).toBeGreaterThan(0)
  })

  it('连接中与已连接分别有可读文案，连上后提示条消失', () => {
    useLive.setState({ status: 'connecting' })
    renderLayout()
    expect(screen.getByText('正在连接实时通道…')).toBeInTheDocument()

    act(() => { useLive.setState({ status: 'open' }) })
    expect(screen.queryByText(/正在连接实时通道/)).not.toBeInTheDocument()
    expect(screen.queryByText(/实时通道已断开/)).not.toBeInTheDocument()
    expect(screen.getAllByText('已连接').length).toBeGreaterThan(0)
  })
})

describe('外观主题控件', () => {
  it('三分段控件有分组名称、按下态与键盘可达性', async () => {
    const user = userEvent.setup()
    renderLayout()
    const group = screen.getByRole('group', { name: '外观主题' })
    const buttons = within(group).getAllByRole('button')
    expect(buttons.map((b) => b.getAttribute('aria-label'))).toEqual(['浅色外观', '深色外观', '跟随系统'])
    expect(buttons[0]).toHaveAttribute('aria-pressed', 'false')

    await user.click(within(group).getByRole('button', { name: '深色外观' }))
    expect(document.documentElement).toHaveClass('dark')
    expect(within(group).getByRole('button', { name: '深色外观' })).toHaveAttribute('aria-pressed', 'true')

    await user.click(within(group).getByRole('button', { name: '跟随系统' }))
    expect(within(group).getByRole('button', { name: '跟随系统' })).toHaveAttribute('aria-pressed', 'true')
    expect(localStorage.getItem('cloudpath.theme')).toBe('system')
  })
})

describe('轻提示播报区', () => {
  // 通道连上后系统提示条退场，页面上只剩一个 role=status（通知播报区）
  beforeEach(() => { useLive.setState({ status: 'open' }) })

  it('是 role=status + aria-live=polite 的礼貌播报区，关闭是独立的可读按钮', async () => {
    const user = userEvent.setup()
    renderLayout()
    const live = screen.getByRole('status', { name: '通知' })
    expect(live).toHaveAttribute('aria-live', 'polite')

    toast.ok('闭合已执行', '设备已回执')
    expect(await screen.findByText('闭合已执行')).toBeInTheDocument()
    expect(screen.getByText('设备已回执')).toBeInTheDocument()

    const dismiss = screen.getByRole('button', { name: '关闭提示：闭合已执行' })
    await user.click(dismiss)
    expect(screen.queryByText('闭合已执行')).not.toBeInTheDocument()
  })

  it('提示文本不被当成按钮名（正文可读、操作可辨）', async () => {
    renderLayout()
    toast.bad('下发失败', '设备离线')
    expect(await screen.findByText('设备离线')).toBeInTheDocument()
    const buttons = screen.getAllByRole('button').map((b) => b.getAttribute('aria-label'))
    expect(buttons.some((n) => n?.includes('下发失败'))).toBe(true)
    // 正文在无障碍树里是文本，不是按钮名
    expect(screen.getByRole('button', { name: '关闭提示：下发失败' })).not.toHaveTextContent('设备离线')
  })
})

describe('390px 溢出收口', () => {
  it('移动端导航在自身容器内横向滚动，不把溢出推给 body', () => {
    renderLayout()
    const navs = screen.getAllByRole('navigation', { name: '主导航' })
    const mobile = navs[navs.length - 1] as HTMLElement
    expect(mobile.className).toContain('overflow-x-auto')
    expect(mobile.querySelector('span.whitespace-nowrap')).not.toBeNull()
  })

  it('内容宽度用 max-w + 相对内边距，桌面侧栏在窄屏不参与布局', () => {
    renderLayout()
    const main = screen.getByRole('main')
    expect(main.className).toContain('lg:pl-60')
    const box = main.firstElementChild as HTMLElement
    expect(box.className).toContain('max-w-[1360px]')
    expect(box.className).toContain('px-4')
    const aside = document.querySelector('aside') as HTMLElement
    expect(aside.className).toContain('hidden')
    expect(aside.className).toContain('lg:flex')
    expect(aside.className).toContain('w-60')
  })

  it('提示条宽度跟随视口上限，窄屏不会顶出横向滚动', () => {
    renderLayout()
    toast.info('提示')
    const live = screen.getByRole('status', { name: '通知' })
    expect(live.className).toContain('max-w-[calc(100vw-3rem)]')
    expect(live.className).not.toMatch(/\bw-\[\d+px\]/)
  })
})