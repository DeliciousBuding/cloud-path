// 插件面：三分（目录 / 已安装 / 运行项）、desired≠observed 的分离呈现、
// 稳定错误码驱动的写操作，以及「绝不把期望当实际」的反向断言。
import { act, screen, within } from '@testing-library/react'
import { Route, Routes } from 'react-router'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import Plugins from '@/pages/Plugins'
import PluginInstanceDetail from '@/pages/PluginInstanceDetail'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import type { PluginCatalogView, PluginInstanceView } from '@/lib/types'
import { useAuth } from '@/store/auth'
import { appInstance, appUser } from '@/test/application-plane'

const LOCAL_PATH = 'C:\\Users\\someone\\plugins\\acme-driver'

const CATALOG: PluginCatalogView[] = [{
  id: 'io.github.acme.driver', kind: 'driver', version: 'v1.2.0',
  source: LOCAL_PATH, digest: 'sha256:abcdef0123456789abcdef', verified: true,
  compatibility: '>=0.1', protocol: 1,
  permissions: { hardware: ['uart'], network: ['outbound'], secrets: ['db-password'] },
  contributes: { drivers: [{ id: 'acme.stcb', title: 'STC-B 驱动' }] },
}]

function instance(over: Partial<PluginInstanceView> = {}): PluginInstanceView {
  return {
    id: 'edge-a/inst-1', tenant_id: 1, edge_id: 'edge-a',
    desired: {
      instance_id: 'inst-1', plugin_id: 'io.github.acme.driver', version: 'v1.2.0',
      enabled: true, isolation: 'process',
      config: { mode: 'auto', pass: 'secret://db-password' },
      secret_refs: ['db-password'], revision: 42, updated_at: 1_770_000_000,
    },
    has_observed: true,
    observed: {
      state: 'HEALTHY', health: 'HEALTHY', version: 'v1.1.0',
      restart_count: 0, reported_at: 1_770_000_010,
    },
    edge_online: true, desired_revision: 42, applied_revision: 42,
    drift: false, stale: false, last_ack_at: 1_770_000_011,
    ...over,
  }
}

const EDGES = { edges: [{ edge_id: 'edge-a', online: true, version: 'v1', devices: [], connected_at: 1 }] }

interface Opts { instances?: PluginInstanceView[]; catalog?: PluginCatalogView[]; patch?: unknown }
function route(o: Opts = {}, patchStatus = 200) {
  return installFetch((url, init) => {
    if (url === '/api/plugins') return stubResponse(200, { plugins: o.catalog ?? CATALOG })
    if (url === '/api/plugin-instances') return stubResponse(200, { instances: o.instances ?? [] })
    if (url.startsWith('/api/plugin-instances/')) {
      if (init?.method === 'PATCH' || init?.method === 'DELETE') {
        return o.patch !== undefined
          ? stubResponse(patchStatus, o.patch)
          : stubResponse(200, { id: 'edge-a/inst-1', revision: 43, request_id: 'r1', instance: instance() })
      }
      return stubResponse(200, o.instances?.[0] ?? instance())
    }
    if (url === '/api/edges') return stubResponse(200, EDGES)
    return stubResponse(404, { error: 'not found' })
  })
}

/** 详情页必须挂在路由上（依赖 useParams 取运行项编号） */
function renderDetail(route = '/plugins/edge-a%2Finst-1') {
  return renderWithProviders(
    <Routes>
      <Route path="/plugins/:id" element={<PluginInstanceDetail />} />
    </Routes>,
    route,
  )
}

async function gotoTab(name: RegExp) {
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name }))
  return user
}

beforeEach(() => { resetStores() })

describe('插件列表只读权限', () => {
  it('viewer 在空目录下仍区分平台服务与真实网关，说明不限定 Edge', async () => {
    useAuth.setState({ status: 'in', user: appUser })
    route({ catalog: [], instances: [appInstance('app-a', 'app-a'), appInstance('app-b', 'server/app-b'), instance()] })
    const { container } = renderWithProviders(<Plugins />)
    expect(screen.getByRole('tab', { name: /运行项/ })).toHaveAttribute('aria-selected', 'true')
    await gotoTab(/可用插件/)
    expect(await screen.findByText('没有可用插件')).toBeInTheDocument()
    expect(screen.getByText('安装插件后会显示在这里。列表为空不会影响已经添加的运行项。')).toBeInTheDocument()
    await gotoTab(/运行项/)
    expect((await screen.findAllByText('平台服务')).length).toBeGreaterThanOrEqual(2)
    expect(screen.getAllByText('状态待确认').length).toBeGreaterThan(0)
    expect(screen.getAllByText(/网关 edge-a/).length).toBeGreaterThan(0)
    expect(container.textContent).not.toContain('网关 server')
    expect(container.querySelector('[title^="网关 server"]')).toBeNull()
    expect(screen.getByText('需要处理的运行项会排在前面。展开「更多信息」可以查看版本和状态详情。')).toBeInTheDocument()

  })

  it('viewer 可查看运行项，但不显示新建、创建或编辑表单入口', async () => {
    useAuth.setState({ status: 'in', user: appUser })
    const http = route({ instances: [instance()] })
    const { container } = renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    expect(await screen.findByText('保存的设置')).toBeInTheDocument()
    for (const name of ['新建运行项', '创建并保存', '编辑', '保存修改']) {
      expect(screen.queryByRole('button', { name })).not.toBeInTheDocument()
    }
    expect(container.querySelector('form')).toBeNull()
    expect(http.calls.every((call) => call.method === 'GET')).toBe(true)
  })

  it.each([
    { entry: '新建运行项', submit: '创建并保存' },
    { entry: '编辑', submit: '保存修改' },
  ])('$entry表单在切换为只读角色时关闭，恢复角色后不遗留表单', async ({ entry, submit }) => {
    useAuth.setState({ status: 'in', user: { ...appUser, role: 'admin' } })
    const http = route({ instances: [instance()] })
    const { container } = renderWithProviders(<Plugins />)
    const user = await gotoTab(/运行项/)
    await user.click(await screen.findByRole('button', { name: entry }))
    expect(await screen.findByRole('button', { name: submit })).toBeInTheDocument()
    act(() => useAuth.setState({ user: appUser }))
    expect(container.querySelector('form')).toBeNull()
    for (const name of ['新建运行项', '创建并保存', '编辑', '保存修改']) {
      expect(screen.queryByRole('button', { name })).not.toBeInTheDocument()
    }
    act(() => useAuth.setState({ user: { ...appUser, role: 'admin' } }))
    expect(screen.getByRole('button', { name: entry })).toBeInTheDocument()
    expect(container.querySelector('form')).toBeNull()
    expect(http.calls.every((call) => call.method === 'GET')).toBe(true)
  })
})

describe('插件面分区', () => {
  it('两个分区都在，运行项优先且目录按需呈现插件声明事实', async () => {
    route({ catalog: CATALOG })
    renderWithProviders(<Plugins />)
    await gotoTab(/可用插件/)
    for (const n of ['可用插件', '运行项']) {
      expect(screen.getByRole('tab', { name: new RegExp(n) })).toBeInTheDocument()
    }
    expect(await screen.findByText('io.github.acme.driver')).toBeInTheDocument()
    expect(screen.getByText('driver')).toBeInTheDocument()
    expect(screen.getByText('v1.2.0')).toBeInTheDocument()
    expect(screen.getByText(/已验证/)).toBeInTheDocument()
    // 权限声明可见；digest 只给短摘要（全长在 title 里）
    expect(screen.getByText('访问串口')).toBeInTheDocument()
    expect(screen.getByText('使用密钥 db-password')).toBeInTheDocument()
    expect(screen.getByText(/abcdef012345/)).toBeInTheDocument()
    // 贡献
    expect(screen.getAllByText('STC-B 驱动').length).toBeGreaterThan(0)
  })

  it('安全边界：目录里的 source（可能是本机绝对路径）绝不渲染', async () => {
    route({ catalog: CATALOG })
    const { container } = renderWithProviders(<Plugins />)
    await gotoTab(/可用插件/)
    await screen.findByText('io.github.acme.driver')
    expect(container.textContent).not.toContain(LOCAL_PATH)
    expect(container.textContent).not.toContain('someone')
  })

  it('目录明确声明「这不代表正在运行」，不与实际状态混淆', async () => {
    route({ catalog: CATALOG })
    renderWithProviders(<Plugins />)
    await gotoTab(/可用插件/)
    await screen.findByText(/不代表.*运行/)
  })

  it('可用插件卡片直接进入创建并预选；连接器明确不能创建', async () => {
    const application: PluginCatalogView = {
      ...CATALOG[0], id: 'io.github.acme.application', kind: 'application',
      contributes: { applications: [{ id: 'acme.app', title: '示例应用' }] },
    }
    const connector: PluginCatalogView = {
      ...CATALOG[0], id: 'io.github.acme.connector', kind: 'connector',
      permissions: { network: ['outbound'] }, contributes: { connectors: [{ id: 'acme.mqtt', title: 'MQTT 连接器' }] },
    }
    route({ catalog: [application, CATALOG[0], connector] })
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/可用插件/)

    expect(await screen.findByText('连接器不能创建运行项')).toBeVisible()
    expect(screen.queryByRole('button', { name: /创建.*MQTT/ })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /创建运行项：示例应用/ }))
    expect(screen.getByRole('tab', { name: /运行项/ })).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByRole('combobox', { name: '要运行什么' })).toHaveValue(application.id)
  })

  it('新建运行项优先选择已验证的可创建插件，不让未验证长名称成为默认', async () => {
    const unverified: PluginCatalogView = {
      ...CATALOG[0], id: 'io.github.acme.unverified', verified: false,
      contributes: { drivers: [{ id: 'acme.long', title: '未验证且名称很长的驱动（不应该默认选中）' }] },
    }
    route({ catalog: [unverified, CATALOG[0]] })
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await user.click(await screen.findByRole('button', { name: '新建运行项' }))
    expect(await screen.findByRole('combobox', { name: '要运行什么' })).toHaveValue(CATALOG[0].id)
  })

  it('目录为空 / 加载失败都是设计过的状态', async () => {
    route({ catalog: [] })
    const first = renderWithProviders(<Plugins />)
    await gotoTab(/可用插件/)
    expect(await screen.findByText('没有可用插件')).toBeInTheDocument()

    first.unmount()
    installFetch((url) => (url === '/api/plugins'
      ? stubResponse(500, { error: 'boom' }) : stubResponse(404, {})))
    renderWithProviders(<Plugins />)
    await gotoTab(/可用插件/)
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('无法加载可用插件')).toBeInTheDocument()
  })
})


describe('运行项分区：desired 与 observed 永远分别渲染', () => {
  it('期望 v1.2.0/rev42 与实际 v1.1.0/applied41 并列且各自标注', async () => {
    route({
      instances: [instance({
        drift: true, desired_revision: 42, applied_revision: 41,
        observed: { ...instance().observed!, version: 'v1.1.0' },
      })],
    })
    renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    const user = userEvent.setup()
    expect(await screen.findByText('保存的设置')).toBeInTheDocument()
    expect(screen.getByText('当前运行情况')).toBeInTheDocument()
    expect(screen.getByText('v1.2.0')).toBeInTheDocument()
    // 当前版本和版本号属于更多信息，默认折叠
    await user.click(screen.getByText('更多信息'))
    expect(screen.getByText('v1.1.0')).toBeInTheDocument()
    expect(screen.getByText(/42/)).toBeInTheDocument()
    expect(screen.getByText(/41/)).toBeInTheDocument()
    // drift 有独立视觉状态
    expect(screen.getAllByText('最新设置尚未生效').length).toBeGreaterThan(0)
  })

  it('反向断言：desired.enabled=true 且无 observed 时，界面不出现「运行中/健康」', async () => {
    route({ instances: [instance({ has_observed: false, observed: undefined })] })
    renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    expect((await screen.findAllByText('状态待确认')).length).toBeGreaterThan(0)
    expect(screen.getByText('已启用')).toBeInTheDocument()
    expect(screen.queryByText('运行中')).not.toBeInTheDocument()
    expect(screen.queryByText('已同步')).not.toBeInTheDocument()
    // 未上报时也要说清是「节点在线但没回」还是「节点离线」
    expect(screen.getByText('网关在线，还没有收到运行状态')).toBeInTheDocument()
  })

  it('stale=true → 实际状态明确标记过期，而不是当前在线事实', async () => {
    route({ instances: [instance({ stale: true })] })
    renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    expect((await screen.findAllByText('状态可能已过期')).length).toBeGreaterThan(0)
  })

  it('详情页的完整分离视图把 stale 说清是历史事实（含绝对上报时间）', async () => {
    route({ instances: [instance({ stale: true })] })
    const user = userEvent.setup()
    renderDetail()
    await user.click(await screen.findByText('查看设置、权限与密钥'))
    expect(await screen.findByText(/已超过有效期/)).toBeInTheDocument()
    expect(screen.getByText(/运行状态已过期 · 更新于/)).toBeInTheDocument()
    // 保存的设置与当前运行情况两栏都在，且各自标注来源
    expect(screen.getByText('保存的设置')).toBeInTheDocument()
    expect(screen.getByText('当前运行情况')).toBeInTheDocument()
    expect(screen.getByText('网关会按它应用')).toBeInTheDocument()
    expect(screen.getByText(/网关最近一次收到/)).toBeInTheDocument()
  })

  it('详情页同时给出 Version/Edge/Trust/Permissions/Health/Revision/Last ACK', async () => {
    route({ instances: [instance()] })
    const user = userEvent.setup()
    renderDetail()
    await user.click(await screen.findByText('查看设置、权限与密钥'))
    expect(await screen.findByText('基本信息')).toBeInTheDocument()
    // 'Edge 在线' 在头部徽标与事实一览里各出现一次，故按「至少一处」断言
    for (const label of ['期望版本', '实际版本', '运行位置', '网关状态', '来源验证', '安装信息']) {
      expect(screen.getAllByText(label).length, `${label} 缺席`).toBeGreaterThan(0)
    }
    expect(screen.getByText('已验证')).toBeInTheDocument()
    expect(screen.getByText('访问串口')).toBeInTheDocument()
    // secret 只有 handle 名，配置里的 secret:// 值也被折叠成名字
    expect(screen.getAllByText('db-password').length).toBeGreaterThan(0)
    expect(screen.queryByText('secret://db-password')).not.toBeInTheDocument()
    expect(screen.getByText(/密钥只会显示名称/)).toBeInTheDocument()
  })

  it('同步异常详情直接给出重新应用动作，不再提示用户打开详情', async () => {
    route({ instances: [instance({ drift: true, applied_revision: 41 })] })
    renderDetail()
    expect(await screen.findByText(/确认保存的设置无误后/)).toBeVisible()
    expect(screen.queryByText(/打开详情核对设置/)).not.toBeInTheDocument()
  })

  it('平台服务详情说明由平台服务应用，不误写成网关', async () => {
    route({ instances: [instance({ id: 'server/inst-1', edge_id: 'server' })] })
    const user = userEvent.setup()
    renderDetail('/plugins/server%2Finst-1')
    await user.click(await screen.findByText('查看设置、权限与密钥'))
    expect(await screen.findByText('平台服务会按它应用')).toBeVisible()
    expect(screen.queryByText('网关会按它应用')).not.toBeInTheDocument()
  })

  it('详情页不泄漏本机绝对路径与插件 stdout 原文', async () => {
    route({ instances: [instance()] })
    const user = userEvent.setup()
    const { container } = renderDetail()
    await user.click(await screen.findByText('查看设置、权限与密钥'))
    await screen.findByText('基本信息')
    expect(container.textContent).not.toContain(LOCAL_PATH)
  })
})

describe('写操作按稳定错误码呈现', () => {
  it('quota 超限 → 说明未生效（不留成功假象）', async () => {
    const http = route({ instances: [instance()], patch: { error: 'plugin_quota_exceeded' } }, 400)
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    await user.click(await screen.findByRole('button', { name: /停用/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent('已经达到数量上限')
    expect(screen.getByText(/本次保存未生效/)).toBeInTheDocument()
    expect(screen.getByText(/plugin_quota_exceeded/)).toBeInTheDocument()
    expect(http.to('/api/plugin-instances/').length).toBeGreaterThan(0)
  })

  it('edge offline → 说明重新连接后会自动同步，不当成失败', async () => {
    route({ instances: [instance()], patch: { error: 'plugin_edge_offline' } }, 409)
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    await user.click(await screen.findByRole('button', { name: /停用/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent('目标网关当前离线')
    expect(screen.getByText(/重新连接后会自动同步最新设置/)).toBeInTheDocument()
  })

  it('权限扩大 → 列出权限清单要求显式勾选，确认后带 confirm_permissions 重发', async () => {
    const http = route({ instances: [instance()], patch: { error: 'plugin_permission_confirmation_required' } }, 400)
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    await user.click(await screen.findByRole('button', { name: /停用/ }))

    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('这次修改会增加插件权限')
    // 权限清单来自目录声明，不是前端编的
    expect(within(dialog).getByText('访问串口')).toBeInTheDocument()
    expect(within(dialog).getByText('访问网络')).toBeInTheDocument()
    const go = within(dialog).getByRole('button', { name: '确认并保存' })
    expect(go).toBeDisabled()
    await user.click(within(dialog).getByRole('checkbox'))
    expect(go).toBeEnabled()

    const before = http.to('/api/plugin-instances/').length
    await user.click(go)
    const calls = http.to('/api/plugin-instances/')
    expect(calls.length).toBeGreaterThan(before)
    expect(calls[calls.length - 1]?.body).toMatchObject({ enabled: false, confirm_permissions: true })
  })

  it('启停只发期望状态字段，且不做乐观更新（实际状态仍由服务端投影决定）', async () => {
    // PATCH 成功但服务端回的实际状态仍未上报：界面必须继续显示「Edge 未上报」
    const http = route({
      instances: [instance({ has_observed: false, observed: undefined })],
      patch: {
        id: 'edge-a/inst-1', revision: 43, request_id: 'r2',
        instance: instance({ has_observed: false, observed: undefined }),
      },
    })
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    await user.click(await screen.findByRole('button', { name: /停用/ }))
    const patch = http.to('/api/plugin-instances/').filter((c) => c.method === 'PATCH')
    expect(patch).toHaveLength(1)
    expect(patch[0]?.body).toEqual({ enabled: false })
    expect(screen.getAllByText('状态待确认').length).toBeGreaterThan(0)
    expect(screen.queryByText('运行中')).not.toBeInTheDocument()
  })

  it('删除必须二次确认 + 勾选；purge 选项进 body', async () => {
    const http = route({ instances: [instance()] })
    const user = userEvent.setup()
    renderDetail()
    await user.click(await screen.findByText('更多操作'))
    await user.click(await screen.findByRole('button', { name: /删除运行项/ }))

    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('删除运行项 inst-1')
    const go = within(dialog).getByRole('button', { name: '删除运行项' })
    expect(go).toBeDisabled()
    await user.click(within(dialog).getByRole('checkbox', { name: /我确认要删除这个运行项/ }))
    await user.click(within(dialog).getByRole('checkbox', { name: /同时删除应用数据/ }))
    expect(go).toBeEnabled()
    await user.click(go)

    const del = http.to('/api/plugin-instances/').filter((c) => c.method === 'DELETE')
    expect(del).toHaveLength(1)
    expect(del[0]?.body).toEqual({ purge: true })
  })

  it('取消删除 → 不发任何 DELETE', async () => {
    const http = route({ instances: [instance()] })
    const user = userEvent.setup()
    renderDetail()
    await user.click(await screen.findByText('更多操作'))
    await user.click(await screen.findByRole('button', { name: /删除运行项/ }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '取消' }))
    expect(http.to('/api/plugin-instances/').filter((c) => c.method === 'DELETE')).toHaveLength(0)
  })

  it('reconcile 在 drift 时先确认，并把 force 写进 body', async () => {
    const http = route({ instances: [instance({ drift: true, applied_revision: 41 })] })
    const user = userEvent.setup()
    renderDetail()
    await user.click(await screen.findByRole('button', { name: /重新应用设置/ }))
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('重新应用最新设置？')
    expect(dialog).toHaveTextContent('还没有应用最新设置')
    await user.click(within(dialog).getByRole('checkbox', { name: /强制重新应用/ }))
    await user.click(within(dialog).getByRole('button', { name: '重新应用' }))
    const post = http.to('/reconcile')
    expect(post).toHaveLength(1)
    expect(post[0]?.body).toEqual({ force: true })
  })
})

describe('运行项分区的空态与错误态', () => {
  it('没有运行项 → 说明怎么建，而不是空白', async () => {
    route({ instances: [] })
    renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    expect(await screen.findByText('还没有运行项')).toBeInTheDocument()
  })

  it('没有可用插件 → 不提供无法完成的创建入口', async () => {
    route({ catalog: [], instances: [] })
    renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    expect(await screen.findByText('还没有运行项')).toBeInTheDocument()
    expect(screen.getByText(/先安装.*插件/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /新建/ })).not.toBeInTheDocument()
  })

  it('运行项端点失败 → 错误态 + 重试', async () => {
    installFetch((url) => (url === '/api/plugin-instances'
      ? stubResponse(503, { error: 'store unavailable' }) : stubResponse(200, { plugins: [] })))
    renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('无法加载运行项')).toBeInTheDocument()
  })

  it('畸形响应（instances 非数组 / 元素缺 id）不白屏', async () => {
    installFetch((url) => {
      if (url === '/api/plugin-instances') return stubResponse(200, { instances: [null, { id: '' }, 'x'] })
      if (url === '/api/plugins') return stubResponse(200, { plugins: null })
      return stubResponse(404, {})
    })
    renderWithProviders(<Plugins />)
    await gotoTab(/运行项/)
    expect(await screen.findByText('还没有运行项')).toBeInTheDocument()
  })
})
it('未曾上报的平台服务运行项不被解释成离线网关，停用期望也不冒充实际停止', async () => {
  useAuth.setState({ status: 'in', user: appUser })
  const app = appInstance('never-started')
  route({ catalog: [], instances: [{ ...app, edge_online: false, has_observed: false, observed: undefined, desired: { ...app.desired, enabled: false } }] })
  renderWithProviders(<Plugins />)
  expect(await screen.findByText('还没有收到平台服务的运行状态')).toBeInTheDocument()
  expect(screen.getAllByText('状态待确认').length).toBeGreaterThan(0)
  expect(screen.getByText('已停用')).toBeInTheDocument()
  for (const text of ['网关尚未上报', '网关离线', '网关在线，尚未同步运行状态', '运行中']) {
    expect(screen.queryByText(text)).not.toBeInTheDocument()
  }
})
