import { useState } from 'react'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import { ApplicationPlane } from '@/components/plugin/ApplicationPlane'
import PluginInstanceDetail from '@/pages/PluginInstanceDetail'
import { useAuth } from '@/store/auth'
import { useLive } from '@/store/ws'
import { useToasts } from '@/store/toast'
import { appInstance, appRecord, appResponse, appSchedule, appUser } from '@/test/application-plane'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'

beforeEach(() => {
  resetStores()
  useAuth.setState({ status: 'in', user: appUser })
  useLive.setState({ status: 'open' })
})

function renderDetail(controlID: string) {
  return renderWithProviders(<Routes><Route path="/plugins/:id" element={<PluginInstanceDetail />} /></Routes>,
    '/plugins/' + encodeURIComponent(controlID))
}

describe('应用数据读取和通用展示', () => {
  it('首读有加载状态，三个真实端点按裸实例标识发 GET', async () => {
    let release!: () => void
    const ready = new Promise<void>((resolve) => { release = resolve })
    const http = installFetch(async (url) => { await ready; return appResponse(url) })
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    for (const name of ['应用记录加载中', '设备绑定加载中', '定时任务加载中']) {
      expect(screen.getByRole('status', { name })).toBeInTheDocument()
    }
    await act(async () => { release() })
    expect(await screen.findByText('已保存内容')).toBeVisible()
    expect(http.to('/app-a/records')[0]?.url).toBe('/api/plugin-instances/app-a/records?limit=20&offset=0')
    expect(http.to('/app-a/bindings')).toHaveLength(1)
    expect(http.to('/app-a/jobs')).toHaveLength(1)
    expect(http.calls.every((call) => call.method === 'GET')).toBe(true)
  })

  it('默认显示结构化值与公开名称，原始标识和 JSON 仅按需展开', async () => {
    installFetch((url) => appResponse(url))
    const user = userEvent.setup()
    const { container } = renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('入口信号')).toBeVisible()
    expect(screen.getByText('输入信号')).toBeVisible()
    expect(screen.getByText('计数')).toBeVisible()
    expect(screen.getByText('否')).toBeVisible()
    expect(screen.getByText('每天 08:30')).toBeVisible()
    expect(screen.getByText(/排定时间不代表已经执行成功/)).toBeVisible()
    expect(container.querySelector('pre')).toBeNull()
    expect(screen.getByText('custom_key')).toBeVisible()
    expect(container.textContent).not.toContain('数据项 1')
    for (const machine of ['saved-1', 'input-source', 'minute-check', 'recurring-check']) {
      expect(container.textContent).not.toContain(machine)
    }
    const record = screen.getByRole('heading', { name: '记录 1' }).closest('article')!
    await user.click(within(record).getByRole('button', { name: '查看技术详情' }))
    expect(within(record).getByText('saved-1')).toBeVisible()
    expect(within(record).getByRole('group', { name: '记录原文' })).toHaveTextContent('custom_key')
    await user.click(within(record).getByRole('button', { name: '收起技术详情' }))
    expect(container.querySelector('pre')).toBeNull()
  })

  it('状态与有用值优先，空字段按需展开而不是占据首屏', async () => {
    installFetch((url) => appResponse(url, { records: [appRecord('actual-result', {
      closed_at: '', opened_at: '', a: 'A', b: 'B', c: 'C', d: 'D',
      e: 'E', reminder_state: 'succeeded', state: 'missed',
    })] }))
    const user = userEvent.setup()
    const { container } = renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByRole('heading', { name: '状态 missed' })).toBeVisible()
    expect(screen.getByText('succeeded')).toBeVisible()
    expect(screen.getByText('reminder_state')).toBeVisible()
    expect(screen.getByText('关闭时间')).not.toBeVisible()
    expect(container.querySelector('pre')).toBeNull()
    expect(screen.queryByText('漏服')).not.toBeInTheDocument()
    await user.click(screen.getByText(/^其余字段（/))
    expect(screen.getByText('关闭时间')).toBeVisible()
    expect(screen.getByText('E')).toBeVisible()
    expect(container.querySelector('pre')).toBeNull()
  })

  it('时间字段可读且保留原时区值，普通文本不被猜成时间', async () => {
    const iso = '2026-09-05T22:30:03Z'
    installFetch((url) => appResponse(url, { records: [appRecord('time', { happened_at: iso, note: '06:30' })] }))
    const { container } = renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByRole('heading', { name: /发生时间 / })).toBeVisible()
    await screen.findByText('note')
    expect(screen.getByText('06:30')).toBeVisible()
    expect(container.querySelector(`time[datetime="${iso}"]`)).toBeNull()
  })

  it('200 空列表与停止状态保持真实空态，不伪造运行绑定或任务', async () => {
    installFetch((url) => appResponse(url, { records: [], running: false, scheduled: [] }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    for (const text of ['暂无应用记录', '暂无设备绑定', '暂无定时任务', '应用未运行']) {
      expect(await screen.findByText(text)).toBeVisible()
    }
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it.each([500, 503, 404])('HTTP %s 是错误而不是成功的空记录/空任务', async (status) => {
    installFetch((url) => /\/(records|jobs)(\?|$)/.test(url)
      ? stubResponse(status, { error: 'store unavailable' }) : appResponse(url))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('应用记录加载失败')).toBeVisible()
    expect(await screen.findByText('定时任务加载失败')).toBeVisible()
    expect(screen.queryByText('暂无应用记录')).not.toBeInTheDocument()
    expect(screen.queryByText('暂无定时任务')).not.toBeInTheDocument()
    expect(screen.queryByText('应用运行中')).not.toBeInTheDocument()
    expect(screen.queryByText('store unavailable')).not.toBeInTheDocument()
  })

  it('权限拒绝可重试，单个分区失败不遮住其他已获准的数据', async () => {
    let denied = true
    installFetch((url) => denied && url.includes('/records?') ? stubResponse(403, { error: 'forbidden' }) : appResponse(url))
    const user = userEvent.setup()
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('没有查看权限')
    expect(await screen.findByText('入口信号')).toBeVisible()
    denied = false
    await user.click(within(alert).getByRole('button', { name: '重新加载' }))
    expect(await screen.findByText('已保存内容')).toBeVisible()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('服务端拒绝分类筛选后仍可清除筛选，不被错误状态锁住', async () => {
    const http = installFetch((url) => new URL(url, 'http://localhost').searchParams.get('record_type') === 'sample'
      ? stubResponse(400, { error: 'invalid record_type' }) : appResponse(url))
    const user = userEvent.setup()
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    await screen.findByText('已保存内容')
    await user.click(screen.getByText('筛选记录'))
    await user.selectOptions(screen.getByLabelText('分类'), 'sample')
    await user.click(screen.getByRole('button', { name: '筛选' }))
    expect(await screen.findByText('筛选条件无效')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '清除筛选' }))
    expect(await screen.findByText('已保存内容')).toBeVisible()
    expect(screen.getByLabelText('分类')).toHaveValue('')
    expect(http.to('/records?').some((c) => c.url.includes('record_type=sample'))).toBe(true)
  })

  it('任意 JSON 类型与损坏内容不会使页面崩溃，也不会执行应用文字', async () => {
    const malformed = { ...appRecord('bad'), data_json: '{broken' }
    installFetch((url) => appResponse(url, { records: [appRecord('text', '<script>not-executed</script>'),
      appRecord('array', [0, false, null]), appRecord('empty', {}), malformed] }))
    const { container } = renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('<script>not-executed</script>')).toBeVisible()
    expect(screen.getByText('未填写')).toBeVisible()
    expect(screen.getByText('暂无内容')).toBeVisible()
    expect(screen.getByText(/记录内容无法读取/)).toBeVisible()
    expect(container.querySelector('script')).toBeNull()
    expect(container.querySelector('pre')).toBeNull()
  })

  it('切实例同时复位分类与分页，不能携带旧记录', async () => {
    const http = installFetch((url) => {
      const parsed = new URL(url, 'http://localhost')
      const second = url.includes('/app-b/')
      const rows = second ? [appRecord('b', '只属于乙')] : parsed.searchParams.get('offset') === '20'
        ? [appRecord('a-last', '甲的第二页')] : Array.from({ length: 20 }, (_, i) => appRecord('a-' + i, '甲的第 ' + i + ' 项'))
      return appResponse(url, { records: rows })
    })
    function Switcher() {
      const [id, setID] = useState('app-a')
      return <><button onClick={() => setID('app-b')}>查看另一个实例</button><ApplicationPlane instanceID={id} /></>
    }
    const user = userEvent.setup()
    renderWithProviders(<Switcher />)
    await screen.findByText('甲的第 0 项')
    await user.click(screen.getByText('筛选记录'))
    await user.type(screen.getByLabelText('分类'), 'sample')
    await user.click(screen.getByRole('button', { name: '筛选' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '下一页' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '下一页' }))
    expect(await screen.findByText('甲的第二页')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '查看另一个实例' }))
    expect(await screen.findByText('只属于乙')).toBeVisible()
    expect(screen.queryByText('甲的第二页')).not.toBeInTheDocument()
    expect(screen.getByText('第 1 页')).toBeVisible()
    expect(screen.getByLabelText('分类')).toHaveValue('')
    expect(http.to('/app-b/records')[0]?.url).toBe('/api/plugin-instances/app-b/records?limit=20&offset=0')
  })

  it('期望停用且观察态未知时显示设置已停用，不冒充应用未运行', async () => {
    installFetch((url) => appResponse(url, { running: false }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" desiredEnabled={false} runtimeState="unknown" />)
    expect(await screen.findByText('设置已停用')).toBeVisible()
    expect(screen.getByText(/设置已停用，应用不会运行/)).toBeVisible()
    expect(screen.queryByText('应用未运行')).not.toBeInTheDocument()
  })

  it('应用停止保留历史和计划，重连后重新读取运行态', async () => {
    let running = false
    installFetch((url) => appResponse(url, { running }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('应用未运行')).toBeVisible()
    expect(screen.getByText('已保存内容')).toBeVisible()
    expect(screen.getByText('计划 1')).toBeVisible()
    expect(screen.getByText('暂无临时任务')).toBeVisible()
    act(() => useLive.setState({ status: 'closed' }))
    expect(screen.getByText(/实时更新已断开/)).toBeVisible()
    running = true
    act(() => useLive.setState({ status: 'open', connectionEpoch: 1 }))
    expect(await screen.findByText('应用运行中')).toBeVisible()
    expect(await screen.findByText('入口信号')).toBeVisible()
    expect(screen.getByText('检查窗口')).toBeVisible()
  })

  it('手动操作不会混进临时任务列表', async () => {
    installFetch((url) => {
      const path = new URL(url, 'http://localhost').pathname
      if (path.endsWith('/jobs')) return stubResponse(200, {
        instance_id: 'app-a', running: true, jobs: ['minute-check', 'manual-check'], scheduled: [],
        job_descriptors: [
          { id: 'minute-check', title: '检查窗口', input_schema_json: '{}', manual_only: false },
          { id: 'manual-check', title: '手动检查', input_schema_json: '{}', manual_only: true },
        ],
      })
      return appResponse(url)
    })
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('检查窗口')).toBeVisible()
    const scheduled = screen.getByText('定时任务').closest('section')!
    expect(within(scheduled).queryByText('手动检查')).not.toBeInTheDocument()
    expect(screen.getAllByText('手动检查')).toHaveLength(1)
  })
  it('取消计划不会被渲染成仍要执行的时间，未知规则不被猜成每天', async () => {
    installFetch((url) => appResponse(url, { scheduled: [{ ...appSchedule, state: 'cancelled', cron: '15 8 * * 1-5' }] }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('已取消')).toBeVisible()
    expect(screen.getByText('未安排')).toBeVisible()
    expect(screen.getByText('自定义时间规则')).toBeVisible()
  })

  it('开放访问可读应用数据，但执行入口仍不可用', async () => {
    useAuth.setState({ status: 'open', user: null })
    const http = installFetch((url) => appResponse(url))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('已保存内容')).toBeVisible()
    expect(screen.queryByRole('link', { name: '前往登录' })).not.toBeInTheDocument()
    expect(http.to('/app-a/records')).toHaveLength(1)
    expect(screen.queryByRole('button', { name: /执行/ })).not.toBeInTheDocument()
  })
})

describe('插件实例详情的应用入口', () => {
  it.each(['server/app-a', 'app-a'])('控制面键 %s 不影响应用裸标识，viewer 无任何写入口', async (controlID) => {
    const instance = appInstance('app-a', controlID)
    const http = installFetch((url) => appResponse(url, { instance }))
    const { container } = renderDetail(controlID)
    expect(await screen.findByText('已保存内容')).toBeVisible()
    expect(screen.getByText(/当前账号只能查看，不能修改这个项目/)).toBeVisible()
    expect(screen.queryByRole('button', { name: /停用|启用|删除|编辑|重新下发/ })).not.toBeInTheDocument()
    expect(screen.queryByText('网关离线')).not.toBeInTheDocument()
    expect(container.querySelector('a[href="/edges/server"]')).toBeNull()
    expect(http.to('/app-a/records')[0]?.url).toBe('/api/plugin-instances/app-a/records?limit=20&offset=0')
    expect(http.calls.every((c) => c.method === 'GET')).toBe(true)
    expect(screen.getByText('app_config')).not.toBeVisible()
    expect(screen.getByText('{"example_input":"input-1"}')).not.toBeVisible()
    await userEvent.setup().click(screen.getByText('查看设置、权限与密钥'))
    expect(screen.getByText('共享运行')).toBeVisible()
    expect(screen.getAllByText('运行位置').length).toBeGreaterThan(0)
    expect(screen.getAllByText('中心服务').length).toBeGreaterThan(0)
  })

  it.each([200, 503])('目录为空或不可用（%s）时，服务端宿主仍有应用读面', async (status) => {
    const instance = { ...appInstance(), has_observed: false, observed: undefined }
    installFetch((url) => url === '/api/plugins' ? stubResponse(status, { plugins: [] }) : appResponse(url, { instance }))
    renderDetail(instance.id)
    expect(await screen.findByText('已保存内容')).toBeVisible()
    expect(screen.queryByText('网关未上报')).not.toBeInTheDocument()
    expect(screen.queryByText('网关离线')).not.toBeInTheDocument()
    expect(screen.getAllByText('还没有运行状态').length).toBeGreaterThan(0)
  })

  it.each(['server/app-a', 'app-a'])('写控制保留服务端键 %s，期望停用不伪造应用已停止', async (controlID) => {
    useAuth.setState({ user: { ...appUser, role: 'operator' } })
    let instance = appInstance('app-a', controlID)
    const http = installFetch((url, init) => {
      if (init?.method === 'PATCH') {
        instance = { ...instance, desired: { ...instance.desired, enabled: false, revision: 2 } }
        return stubResponse(200, { id: controlID, revision: 2, request_id: 'request-1', instance })
      }
      return appResponse(url, { instance })
    })
    const user = userEvent.setup()
    renderDetail(controlID)
    await screen.findByText('应用运行中')
    await user.click(screen.getByRole('button', { name: '停用' }))
    await screen.findByRole('button', { name: '启用' })
    const writes = http.calls.filter((c) => c.method === 'PATCH')
    expect(writes).toHaveLength(1)
    expect(writes[0].url).toBe('/api/plugin-instances/' + encodeURIComponent(controlID))
    expect(writes[0].body).toEqual({ enabled: false })
    expect(useToasts.getState().items).toEqual([expect.objectContaining({
      title: '设置已更新',
      detail: '已保存你的修改；收到运行状态后，这里会显示实际结果。',
      tone: 'ok',
    })])
    expect(screen.getByText('应用运行中')).toBeVisible()
    expect(screen.queryByText('应用未运行')).not.toBeInTheDocument()
    expect(screen.queryByText('应用已停止，不能执行操作。')).not.toBeInTheDocument()
    expect(screen.getByText('设置已停用，不能执行操作。')).toBeVisible()
  })

  it('观察态与实际运行态冲突时显示状态冲突，不把冲突二选一', async () => {
    const base = appInstance('app-a', 'app-a')
    const instance = { ...base, observed: { ...base.observed!, state: 'stopped' } }
    installFetch((url) => url === '/api/plugins' ? stubResponse(200, { plugins: [] }) : appResponse(url, { instance, running: true }))
    renderDetail(instance.id)
    expect(await screen.findByText('状态冲突')).toBeVisible()
    expect(screen.getByText('运行状态来源不一致，暂时无法确认应用是否正在运行。')).toBeVisible()
    expect(screen.getByText('运行状态来源不一致，暂不能执行操作。')).toBeVisible()
    expect(screen.queryByText('应用运行中')).not.toBeInTheDocument()
  })

  it('普通驱动详情不探测应用数据', async () => {
    const instance = { ...appInstance(), edge_id: 'node-a', id: 'node-a/app-a' }
    const http = installFetch((url) => url === '/api/plugins' ? stubResponse(200, { plugins: [] }) : appResponse(url, { instance }))
    renderDetail(instance.id)
    await screen.findByRole('heading', { name: 'app-a' })
    expect(screen.queryByRole('region', { name: '应用数据' })).not.toBeInTheDocument()
    expect(http.calls.filter((c) => /\/(records|bindings|jobs)(\?|$)/.test(c.url))).toHaveLength(0)
  })
})
