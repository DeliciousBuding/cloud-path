// Layout：地标与跳转链接、实时通道状态提示、外观主题控件与轻提示播报区。
import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import Layout from '@/components/Layout'
import { i18n } from '@/i18n'
import { useAuth } from '@/store/auth'
import { toast } from '@/store/toast'
import { useLive } from '@/store/ws'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'

const health = { ok: true, version: 'v0.1.0', uptime_s: 60, devices_online: 0, devices_total: 0, edges_online: 0 }
const admin = { id: 1, username: 'admin', name: '管理员', role: 'admin' as const, tenant_id: 1, tenant_slug: 'default' }

function renderLayout() {
  return renderWithProviders(
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<h1>页面内容</h1>} />
      </Route>
    </Routes>,
  )
}

beforeEach(async () => {
  await i18n.changeLanguage('zh-CN')
  resetStores()
  useAuth.setState({ status: 'in', user: admin })
  installFetch((url) => (url === '/healthz' ? stubResponse(200, health) : stubResponse(404, {})))
})
afterEach(async () => { await i18n.changeLanguage('zh-CN') })

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
    expect(screen.getAllByRole('link', { name: 'CloudPath 概览' }).length).toBeGreaterThan(0)
    const nav = screen.getAllByRole('navigation', { name: '主导航' })[0] as HTMLElement
    expect(within(nav).getByRole('link', { name: /设备/ })).toHaveAttribute('title', '设备')
  })

  it('侧栏底部给出可读版本号', async () => {
    renderLayout()
    expect(await screen.findByText('版本 v0.1.0')).toBeInTheDocument()
  })
})

describe('任务导向导航', () => {
  it('核心导航顺序稳定，设备先于网关，运行记录留在主入口', () => {
    renderLayout()
    const nav = screen.getAllByRole('navigation', { name: '主导航' })[0] as HTMLElement
    expect(within(nav).getAllByRole('link').map((a) => a.textContent?.trim())).toEqual([
      '概览', '设备', '网关', '运行记录', '应用中心', '管理', '设置',
    ])
  })

  it('设备调试和运行实例各有单一入口，不再把通用观测叫药盒控制', () => {
    renderLayout()
    const nav = screen.getAllByRole('navigation', { name: '主导航' })[0] as HTMLElement
    expect(within(nav).getByRole('link', { name: '设备' })).toHaveAttribute('href', '/devices')
    expect(within(nav).getByRole('link', { name: '应用中心' })).toHaveAttribute('href', '/plugins')
    expect(within(nav).queryByRole('link', { name: '药盒控制' })).not.toBeInTheDocument()
  })

  it('移动端“更多”固定收纳应用、管理和设置，不随主导航切片漂移', async () => {
    const user = userEvent.setup()
    renderLayout()
    await user.click(screen.getByLabelText('更多导航与账号设置'))
    const more = screen.getByRole('navigation', { name: '更多导航' })
    expect(within(more).getAllByRole('link').map((a) => a.textContent?.trim())).toEqual([
      '应用中心', '管理', '设置',
    ])
  })

  it('已启用且已验证的 Application 插件生成动态业务入口，Driver 不进入主导航', async () => {
    installFetch((url) => {
      if (url === '/healthz') return stubResponse(200, health)
      if (url === '/api/plugins') return stubResponse(200, { plugins: [
        {
          id: 'example.pillbox', kind: 'application', version: 'v1', source: '', digest: '', verified: true,
          protocol: 1, permissions: {}, contributes: { applications: [{ id: 'pillbox', title: '药盒', ui: {
            apiVersion: 1, navigation: { title: '药盒提醒', route: 'pillbox' }, pages: [{ id: 'home', title: '药盒提醒', sections: [{ type: 'status' }] }],
          } }] },
        },
        {
          id: 'example.driver', kind: 'driver', version: 'v1', source: '', digest: '', verified: true,
          protocol: 1, permissions: {}, contributes: { drivers: [{ id: 'driver', title: '驱动', ui: {
            apiVersion: 1, navigation: { title: '驱动页面', route: 'driver' },
          } }] },
        },
      ] })
      if (url === '/api/plugin-instances') return stubResponse(200, { instances: [{
        id: 'server/pillbox-a', tenant_id: 1, edge_id: 'server', desired: {
          instance_id: 'pillbox-a', plugin_id: 'example.pillbox', version: 'v1', enabled: true,
          isolation: 'shared', revision: 1, updated_at: 1,
        }, has_observed: true, observed: { state: 'running', health: 'HEALTHY', restart_count: 0 },
        edge_online: false, desired_revision: 1, applied_revision: 1, drift: false, stale: false,
      }] })
      return stubResponse(404, {})
    })
    renderLayout()
    const nav = screen.getAllByRole('navigation', { name: '主导航' })[0] as HTMLElement
    expect(await within(nav).findByRole('link', { name: '药盒提醒' })).toHaveAttribute('href', '/apps/pillbox')
    expect(within(nav).queryByRole('link', { name: '驱动页面' })).not.toBeInTheDocument()
  })

  it('插件导航标题跟随语言切换即时更新', async () => {
    installFetch((url) => {
      if (url === '/healthz') return stubResponse(200, health)
      if (url === '/api/plugins') return stubResponse(200, { plugins: [{
        id: 'example.pillbox', kind: 'application', version: 'v1', source: '', digest: '', verified: true,
        protocol: 1, permissions: {}, contributes: { applications: [{ id: 'pillbox', title: '药盒', ui: {
          apiVersion: 1, navigation: { title: '药盒提醒', i18n: { 'zh-CN': '药盒提醒', 'en-US': 'Pillbox reminders' }, route: 'pillbox' },
          pages: [{ id: 'home', title: '药盒提醒', sections: [{ type: 'status' }] }],
        } }] },
      }] })
      if (url === '/api/plugin-instances') return stubResponse(200, { instances: [{
        id: 'server/pillbox-a', tenant_id: 1, edge_id: 'server', desired: {
          instance_id: 'pillbox-a', plugin_id: 'example.pillbox', version: 'v1', enabled: true,
          isolation: 'shared', revision: 1, updated_at: 1,
        }, has_observed: true, observed: { state: 'running', health: 'HEALTHY', restart_count: 0 },
        edge_online: false, desired_revision: 1, applied_revision: 1, drift: false, stale: false,
      }] })
      return stubResponse(404, {})
    })
    renderLayout()
    const nav = screen.getAllByRole('navigation', { name: '主导航' })[0] as HTMLElement
    expect(await within(nav).findByRole('link', { name: '药盒提醒' })).toHaveAttribute('href', '/apps/pillbox')

    await act(async () => { await i18n.changeLanguage('en-US') })

    expect(within(nav).getByRole('link', { name: 'Pillbox reminders' })).toHaveAttribute('href', '/apps/pillbox')
    expect(within(nav).queryByRole('link', { name: '药盒提醒' })).not.toBeInTheDocument()
  })

  it('移动端“更多”支持 Escape 和点击外部关闭', async () => {
    const user = userEvent.setup()
    renderLayout()
    const trigger = screen.getByLabelText('更多导航与账号设置')

    await user.click(trigger)
    expect(screen.getByRole('navigation', { name: '更多导航' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('navigation', { name: '更多导航' })).not.toBeInTheDocument()

    await user.click(trigger)
    await user.click(screen.getByRole('heading', { name: '页面内容' }))
    expect(screen.queryByRole('navigation', { name: '更多导航' })).not.toBeInTheDocument()
  })
})

describe('实时通道状态提示', () => {
  it('断开时给出系统级提示条并说明会自动重连', () => {
    renderLayout()
    expect(screen.getByText(/数据连接已断开，正在自动恢复/)).toBeInTheDocument()
    expect(screen.getAllByText('已断开').length).toBeGreaterThan(0)
  })

  it('连接中与已连接分别有可读文案，连上后提示条消失', () => {
    useLive.setState({ status: 'connecting' })
    renderLayout()
    expect(screen.getByText(/正在恢复数据连接/)).toBeInTheDocument()

    act(() => { useLive.setState({ status: 'open' }) })
    expect(screen.queryByText(/正在恢复数据连接/)).not.toBeInTheDocument()
    expect(screen.queryByText(/数据连接已断开/)).not.toBeInTheDocument()
    expect(screen.getAllByText('已连接').length).toBeGreaterThan(0)
  })
})

describe('外观主题控件', () => {
  it('三分段控件有分组名称、按下态与键盘可达性', async () => {
    const user = userEvent.setup()
    renderLayout()
    const group = screen.getAllByRole('group', { name: '外观主题' })[0] as HTMLElement
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

    toast.ok('闭合已执行', '设备已确认结果')
    expect(await screen.findByText('闭合已执行')).toBeInTheDocument()
    expect(screen.getByText('设备已确认结果')).toBeInTheDocument()

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
