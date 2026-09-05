// 插件面：三分（目录 / 已安装 / 实例）、desired≠observed 的分离呈现、
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

/** 详情页必须挂在路由上（依赖 useParams 取实例 ID） */
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
  it('viewer 在空目录下仍区分中心服务与真实边缘节点，说明不限定 Edge', async () => {
    useAuth.setState({ status: 'in', user: appUser })
    route({ catalog: [], instances: [appInstance('app-a', 'app-a'), appInstance('app-b', 'server/app-b'), instance()] })
    const { container } = renderWithProviders(<Plugins />)
    expect(await screen.findByText('插件目录为空')).toBeInTheDocument()
    expect(screen.getByText('插件声明同步到目录后，这里会显示版本、摘要、权限和贡献。目录为空不代表没有已安装或运行的实例。')).toBeInTheDocument()
    await gotoTab(/实例/)
    const locations = await screen.findAllByText(/^中心服务 · 最后回执/)
    expect(locations).toHaveLength(2)
    locations.forEach((location) => expect(location).toHaveAttribute('title', '中心服务 · 最后回执 尚无回执'))
    expect(screen.getByText(/^边缘节点 edge-a · 最后回执/)).toBeInTheDocument()
    expect(container.textContent).not.toContain('边缘节点 server')
    expect(container.querySelector('[title^="边缘节点 server"]')).toBeNull()
    expect(screen.getByText('每行都分开写「期望态」与「实际态」：期望已启用不等于运行宿主已运行该实例。尚未收到实际态时，明确标注未上报。')).toBeInTheDocument()
  })

  it('viewer 可查看实例，但不显示新建、创建或编辑表单入口', async () => {
    useAuth.setState({ status: 'in', user: appUser })
    const http = route({ instances: [instance()] })
    const { container } = renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    expect(await screen.findByText('期望态')).toBeInTheDocument()
    for (const name of ['新建实例', '创建实例', '编辑', '保存变更']) {
      expect(screen.queryByRole('button', { name })).not.toBeInTheDocument()
    }
    expect(container.querySelector('form')).toBeNull()
    expect(http.calls.every((call) => call.method === 'GET')).toBe(true)
  })

  it.each([
    { entry: '新建实例', submit: '创建实例' },
    { entry: '编辑', submit: '保存变更' },
  ])('$entry表单在切换为只读角色时关闭，恢复角色后不遗留表单', async ({ entry, submit }) => {
    useAuth.setState({ status: 'in', user: { ...appUser, role: 'admin' } })
    const http = route({ instances: [instance()] })
    const { container } = renderWithProviders(<Plugins />)
    const user = await gotoTab(/实例/)
    await user.click(await screen.findByRole('button', { name: entry }))
    expect(await screen.findByRole('button', { name: submit })).toBeInTheDocument()
    act(() => useAuth.setState({ user: appUser }))
    expect(container.querySelector('form')).toBeNull()
    for (const name of ['新建实例', '创建实例', '编辑', '保存变更']) {
      expect(screen.queryByRole('button', { name })).not.toBeInTheDocument()
    }
    act(() => useAuth.setState({ user: { ...appUser, role: 'admin' } }))
    expect(screen.getByRole('button', { name: entry })).toBeInTheDocument()
    expect(container.querySelector('form')).toBeNull()
    expect(http.calls.every((call) => call.method === 'GET')).toBe(true)
  })
})

describe('插件面三分', () => {
  it('三个分区都在，且默认目录呈现插件声明事实', async () => {
    route({ catalog: CATALOG })
    renderWithProviders(<Plugins />)
    for (const n of ['目录', '已安装', '实例']) {
      expect(screen.getByRole('tab', { name: new RegExp(n) })).toBeInTheDocument()
    }
    expect(await screen.findByText('io.github.acme.driver')).toBeInTheDocument()
    expect(screen.getByText('driver')).toBeInTheDocument()
    expect(screen.getByText('v1.2.0')).toBeInTheDocument()
    expect(screen.getByText(/已验证/)).toBeInTheDocument()
    // 权限声明可见；digest 只给短摘要（全长在 title 里）
    expect(screen.getByText('uart')).toBeInTheDocument()
    expect(screen.getByText('db-password')).toBeInTheDocument()
    expect(screen.getByText(/abcdef012345/)).toBeInTheDocument()
    // 贡献
    expect(screen.getByText('STC-B 驱动')).toBeInTheDocument()
  })

  it('安全边界：目录里的 source（可能是本机绝对路径）绝不渲染', async () => {
    route({ catalog: CATALOG })
    const { container } = renderWithProviders(<Plugins />)
    await screen.findByText('io.github.acme.driver')
    expect(container.textContent).not.toContain(LOCAL_PATH)
    expect(container.textContent).not.toContain('someone')
  })

  it('目录明确声明「这不代表正在运行」，不与实际态混淆', async () => {
    route({ catalog: CATALOG })
    renderWithProviders(<Plugins />)
    await screen.findByText(/不代表任何 Edge 上正在运行/)
  })

  it('目录为空 / 加载失败都是设计过的状态', async () => {
    route({ catalog: [] })
    renderWithProviders(<Plugins />)
    expect(await screen.findByText('插件目录为空')).toBeInTheDocument()

    installFetch((url) => (url === '/api/plugins'
      ? stubResponse(500, { error: 'boom' }) : stubResponse(404, {})))
    renderWithProviders(<Plugins />)
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('插件目录加载失败')).toBeInTheDocument()
  })
})

describe('已安装分区：只认 Edge 上报', () => {
  it('按 Edge 归组呈现实际态（版本/状态/健康/重启/上报时间）', async () => {
    route({ instances: [instance()] })
    renderWithProviders(<Plugins />)
    const user = await gotoTab(/已安装/)
    expect(user).toBeDefined()
    expect(await screen.findByText('edge-a')).toBeInTheDocument()
    expect(screen.getByText('边缘节点在线')).toBeInTheDocument()
    expect(screen.getByText('运行中')).toBeInTheDocument()
    // 实际版本 v1.1.0 与期望版本 v1.2.0 同时可见，说明两栏没有互相顶替
    expect(screen.getAllByText('v1.1.0').length).toBeGreaterThan(0)
    expect(screen.getByText(/健康 健康/)).toBeInTheDocument()
    expect(screen.getByText(/重启 0 次/)).toBeInTheDocument()
  })

  it('has_observed=false → 明确「边缘节点未上报」，不拿期望态顶替', async () => {
    route({ instances: [instance({ has_observed: false, observed: undefined })] })
    renderWithProviders(<Plugins />)
    await gotoTab(/已安装/)
    expect(await screen.findByText('边缘节点未上报')).toBeInTheDocument()
    expect(screen.queryByText('运行中')).not.toBeInTheDocument()
  })

  it('边缘节点离线时如实说明「下面是最后一次上报」，且不影响其他节点', async () => {
    route({
      instances: [
        instance({ id: 'edge-a/inst-1', edge_id: 'edge-a', edge_online: false }),
        instance({
          id: 'edge-b/inst-2', edge_id: 'edge-b', edge_online: true,
          desired: { ...instance().desired, instance_id: 'inst-2' },
        }),
      ],
    })
    renderWithProviders(<Plugins />)
    await gotoTab(/已安装/)
    expect(await screen.findByText(/最后一次上报的实际态/)).toBeInTheDocument()
    expect(screen.getByText('边缘节点离线')).toBeInTheDocument()
    // 另一台 Edge 照常呈现在线
    expect(screen.getByText('边缘节点在线')).toBeInTheDocument()
    expect(screen.getByText('edge-b')).toBeInTheDocument()
  })
})

describe('实例分区：desired 与 observed 永远分别渲染', () => {
  it('期望 v1.2.0/rev42 与实际 v1.1.0/applied41 并列且各自标注', async () => {
    route({
      instances: [instance({
        drift: true, desired_revision: 42, applied_revision: 41,
        observed: { ...instance().observed!, version: 'v1.1.0' },
      })],
    })
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    expect(await screen.findByText('期望态')).toBeInTheDocument()
    expect(screen.getByText('实际态')).toBeInTheDocument()
    expect(screen.getByText('v1.2.0')).toBeInTheDocument()
    expect(screen.getByText('v1.1.0')).toBeInTheDocument()
    expect(screen.getByText(/修订 42/)).toBeInTheDocument()
    expect(screen.getByText(/已应用 41/)).toBeInTheDocument()
    // drift 有独立视觉状态
    expect(screen.getByText('期望与实际不一致')).toBeInTheDocument()
  })

  it('反向断言：desired.enabled=true 且无 observed 时，界面不出现「运行中/健康」', async () => {
    route({ instances: [instance({ has_observed: false, observed: undefined })] })
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    expect((await screen.findAllByText('边缘节点未上报')).length).toBeGreaterThan(0)
    expect(screen.getByText('已启用')).toBeInTheDocument()
    expect(screen.queryByText('运行中')).not.toBeInTheDocument()
    expect(screen.queryByText('已收敛')).not.toBeInTheDocument()
    // 未上报时也要说清是「节点在线但没回」还是「节点离线」
    expect(screen.getByText('节点在线但还没回过')).toBeInTheDocument()
  })

  it('stale=true → 实际态标 stale，并说明是历史事实', async () => {
    route({ instances: [instance({ stale: true })] })
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    expect(await screen.findByText('实际态已过期')).toBeInTheDocument()
    expect(screen.getAllByText(/stale/).length).toBeGreaterThan(0)
  })

  it('详情页的完整分离视图把 stale 说清是历史事实（含绝对上报时间）', async () => {
    route({ instances: [instance({ stale: true })] })
    renderDetail()
    expect(await screen.findByText(/已超出新鲜期/)).toBeInTheDocument()
    expect(screen.getByText(/stale · 上报于/)).toBeInTheDocument()
    // 期望态与实际态两栏都在，且各自标注来源
    expect(screen.getByText('期望态')).toBeInTheDocument()
    expect(screen.getByText('实际态')).toBeInTheDocument()
    expect(screen.getByText('服务器权威')).toBeInTheDocument()
    expect(screen.getByText('边缘节点上报')).toBeInTheDocument()
  })

  it('详情页同时给出 Version/Edge/Trust/Permissions/Health/Revision/Last ACK', async () => {
    route({ instances: [instance()] })
    renderDetail()
    expect(await screen.findByText('事实一览')).toBeInTheDocument()
    // 'Edge 在线' 在头部徽标与事实一览里各出现一次，故按「至少一处」断言
    for (const label of ['期望版本', '实际版本', '边缘节点', '边缘节点在线', '期望修订版', '已应用修订版', '最后回执', '信任目录']) {
      expect(screen.getAllByText(label).length, `${label} 缺席`).toBeGreaterThan(0)
    }
    expect(screen.getByText('已验证')).toBeInTheDocument()
    expect(screen.getByText('uart')).toBeInTheDocument()
    // secret 只有 handle 名，配置里的 secret:// 值也被折叠成名字
    expect(screen.getAllByText('db-password').length).toBeGreaterThan(0)
    expect(screen.queryByText('secret://db-password')).not.toBeInTheDocument()
    expect(screen.getByText(/明文只在运行宿主/)).toBeInTheDocument()
  })

  it('详情页不泄漏本机绝对路径与插件 stdout 原文', async () => {
    route({ instances: [instance()] })
    const { container } = renderDetail()
    await screen.findByText('事实一览')
    expect(container.textContent).not.toContain(LOCAL_PATH)
  })
})

describe('写操作按稳定错误码呈现', () => {
  it('quota 超限 → 说明未生效（不留成功假象）', async () => {
    const http = route({ instances: [instance()], patch: { error: 'plugin_quota_exceeded' } }, 400)
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    await user.click(await screen.findByRole('button', { name: /停用/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent('超出租户配额')
    expect(screen.getByText(/未生效/)).toBeInTheDocument()
    expect(screen.getByText(/plugin_quota_exceeded/)).toBeInTheDocument()
    expect(http.to('/api/plugin-instances/').length).toBeGreaterThan(0)
  })

  it('edge offline → 说明重连后会自动收敛，不当成失败', async () => {
    route({ instances: [instance()], patch: { error: 'plugin_edge_offline' } }, 409)
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    await user.click(await screen.findByRole('button', { name: /停用/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent('目标边缘节点离线')
    expect(screen.getByText(/重连后会自动收敛/)).toBeInTheDocument()
  })

  it('权限扩大 → 列出权限清单要求显式勾选，确认后带 confirm_permissions 重发', async () => {
    const http = route({ instances: [instance()], patch: { error: 'plugin_permission_confirmation_required' } }, 400)
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    await user.click(await screen.findByRole('button', { name: /停用/ }))

    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('这次变更会扩大插件权限')
    // 权限清单来自目录声明，不是前端编的
    expect(within(dialog).getByText('uart')).toBeInTheDocument()
    expect(within(dialog).getByText('outbound')).toBeInTheDocument()
    const go = within(dialog).getByRole('button', { name: '确认并重新提交' })
    expect(go).toBeDisabled()
    await user.click(within(dialog).getByRole('checkbox'))
    expect(go).toBeEnabled()

    const before = http.to('/api/plugin-instances/').length
    await user.click(go)
    const calls = http.to('/api/plugin-instances/')
    expect(calls.length).toBeGreaterThan(before)
    expect(calls[calls.length - 1]?.body).toMatchObject({ enabled: false, confirm_permissions: true })
  })

  it('启停只发期望态字段，且不做乐观更新（实际态仍由服务端投影决定）', async () => {
    // PATCH 成功但服务端回的实际态仍未上报：界面必须继续显示「Edge 未上报」
    const http = route({
      instances: [instance({ has_observed: false, observed: undefined })],
      patch: {
        id: 'edge-a/inst-1', revision: 43, request_id: 'r2',
        instance: instance({ has_observed: false, observed: undefined }),
      },
    })
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    await user.click(await screen.findByRole('button', { name: /停用/ }))
    const patch = http.to('/api/plugin-instances/').filter((c) => c.method === 'PATCH')
    expect(patch).toHaveLength(1)
    expect(patch[0]?.body).toEqual({ enabled: false })
    expect(screen.getAllByText('边缘节点未上报').length).toBeGreaterThan(0)
    expect(screen.queryByText('运行中')).not.toBeInTheDocument()
  })

  it('删除必须二次确认 + 勾选；purge 选项进 body', async () => {
    const http = route({ instances: [instance()] })
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    await user.click(await screen.findByRole('button', { name: /删除/ }))

    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('删除实例 inst-1')
    const go = within(dialog).getByRole('button', { name: '删除实例' })
    expect(go).toBeDisabled()
    await user.click(within(dialog).getByRole('checkbox', { name: /我确认要删除这个插件实例/ }))
    await user.click(within(dialog).getByRole('checkbox', { name: /purge/ }))
    expect(go).toBeEnabled()
    await user.click(go)

    const del = http.to('/api/plugin-instances/').filter((c) => c.method === 'DELETE')
    expect(del).toHaveLength(1)
    expect(del[0]?.body).toEqual({ purge: true })
  })

  it('取消删除 → 不发任何 DELETE', async () => {
    const http = route({ instances: [instance()] })
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    await user.click(await screen.findByRole('button', { name: /删除/ }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '取消' }))
    expect(http.to('/api/plugin-instances/').filter((c) => c.method === 'DELETE')).toHaveLength(0)
  })

  it('reconcile 在 drift 时先确认，并把 force 写进 body', async () => {
    const http = route({ instances: [instance({ drift: true, applied_revision: 41 })] })
    const user = userEvent.setup()
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    await user.click(await screen.findByRole('button', { name: /重新下发/ }))
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('期望修订版 42')
    expect(dialog).toHaveTextContent('已应用 41')
    await user.click(within(dialog).getByRole('checkbox', { name: /强制/ }))
    await user.click(within(dialog).getByRole('button', { name: '下发' }))
    const post = http.to('/reconcile')
    expect(post).toHaveLength(1)
    expect(post[0]?.body).toEqual({ force: true })
  })
})

describe('实例分区的空态与错误态', () => {
  it('没有实例 → 说明怎么建，而不是空白', async () => {
    route({ instances: [] })
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    expect(await screen.findByText('还没有插件实例')).toBeInTheDocument()
  })

  it('实例端点失败 → 错误态 + 重试', async () => {
    installFetch((url) => (url === '/api/plugin-instances'
      ? stubResponse(503, { error: 'store unavailable' }) : stubResponse(200, { plugins: [] })))
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('插件实例加载失败')).toBeInTheDocument()
  })

  it('畸形响应（instances 非数组 / 元素缺 id）不白屏', async () => {
    installFetch((url) => {
      if (url === '/api/plugin-instances') return stubResponse(200, { instances: [null, { id: '' }, 'x'] })
      if (url === '/api/plugins') return stubResponse(200, { plugins: null })
      return stubResponse(404, {})
    })
    renderWithProviders(<Plugins />)
    await gotoTab(/实例/)
    expect(await screen.findByText('还没有插件实例')).toBeInTheDocument()
  })
})