import { useState } from 'react'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApplicationPlane } from '@/components/plugin/ApplicationPlane'
import { api } from '@/lib/api'
import { appUser } from '@/test/application-plane'
import { accepted, actionRequests, actionResponse, emptyJob, fieldJob } from '@/test/application-actions'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'
import { useAuth } from '@/store/auth'
import { useLive } from '@/store/ws'

beforeEach(() => {
  resetStores()
  useAuth.setState({ status: 'in', user: { ...appUser, role: 'operator' } })
  useLive.setState({ status: 'open' })
})
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers() })
const execute = () => screen.getByRole('button', { name: '执行「更新计数」' })
const countInput = () => screen.getByRole('combobox', { name: /次数/ })
async function ready() { return screen.findByRole('combobox', { name: /次数/ }) }

// All interactions use the actual REST/query/auth/form paths, not mocked hook success.
describe('应用操作的授权、生命周期与明确用户意图', () => {
  it.each(['operator', 'admin'] as const)('%s 通过真实参数表单提交，未填写字段/default 不出现在请求中', async (role) => {
    useAuth.setState({ user: { ...appUser, role } })
    const http = installFetch((url, init) => init?.method === 'POST' ? accepted() : actionResponse(url))
    const user = userEvent.setup()
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    await ready()
    expect(execute()).toBeDisabled()
    expect(actionRequests(http)).toHaveLength(0)
    expect(screen.getByRole('textbox', { name: '备注' })).toHaveValue('')
    fireEvent.change(countInput(), { target: { value: '1' } })
    await user.click(execute())
    expect(await screen.findByText('操作已受理')).toBeVisible()
    expect(actionRequests(http)).toEqual([{ args_json: '{"count":2}', idempotency_key: expect.any(String) }])
    expect(new TextEncoder().encode(actionRequests(http)[0].idempotency_key).length).toBeLessThanOrEqual(128)
    expect(screen.getByText('执行结果')).toBeVisible()
    expect(screen.getByText(/设备执行结果请在应用记录中核对/)).toBeVisible()
    expect(screen.queryByText('板端成功')).not.toBeInTheDocument()
    await waitFor(() => expect(http.to('/app-a/records')).toHaveLength(2))
  })

  it.each([
    { role: 'viewer' as const }, { role: 'operator' as const, disabled: true },
  ])('只读/禁用身份没有执行或参数编辑入口（%j）', async (identity) => {
    useAuth.setState({ user: { ...appUser, ...identity } })
    const http = installFetch((url) => actionResponse(url))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('更新计数')).toBeVisible()
    expect(screen.getByText(/当前账号只能查看，不能执行操作/)).toBeVisible()
    expect(screen.queryByRole('button', { name: /执行|重试同一/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: /次数/ })).not.toBeInTheDocument()
    expect(actionRequests(http)).toHaveLength(0)
  })

  it('open 可读应用数据，但不提供执行入口', async () => {
    useAuth.setState({ status: 'open', user: null })
    const http = installFetch((url) => actionResponse(url))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('更新计数')).toBeVisible()
    expect(screen.queryByRole('button', { name: /执行/ })).not.toBeInTheDocument()
    expect(actionRequests(http)).toHaveLength(0)
  })

  it('out 不读取也不提供执行入口', () => {
    useAuth.setState({ status: 'out', user: null })
    const http = installFetch((url) => actionResponse(url))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(screen.queryByRole('button', { name: /执行/ })).not.toBeInTheDocument()
    expect(http.calls).toHaveLength(0)
  })

  it.each([{ descriptors: null }, { descriptors: [{ ...fieldJob, manual_only: false }] }])('旧 jobs 或后台 descriptor 不能产生手动按钮', async ({ descriptors }) => {
    const http = installFetch((url) => actionResponse(url, { descriptors }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('暂无应用操作')).toBeVisible()
    expect(screen.queryByRole('button', { name: /执行/ })).not.toBeInTheDocument()
    expect(actionRequests(http)).toHaveLength(0)
  })

  it('用户操作不在挂载、定时刷新、WS 通知、重连或重新渲染时自动执行', async () => {
    vi.useFakeTimers()
    const http = installFetch((url) => actionResponse(url, { descriptors: [emptyJob] }))
    const { queryClient } = renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(20) })
    expect(screen.getByRole('button', { name: '执行「刷新记录」' })).toBeEnabled()
    act(() => useLive.setState({ connectionEpoch: 1, domainRecord: { instanceID: 'app-a', sequence: 1 } }))
    await act(async () => { await vi.advanceTimersByTimeAsync(30_100) })
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['application-plane'] }) })
    expect(http.to('/app-a/jobs').length).toBeGreaterThan(2)
    expect(actionRequests(http)).toHaveLength(0)
  })

  it('无参数操作不显示技术参数输入框，并明确说明可直接执行', async () => {
    installFetch((url) => actionResponse(url, { descriptors: [emptyJob] }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('无需填写参数，点击即可执行。')).toBeVisible()
    expect(screen.queryByText('技术参数')).not.toBeInTheDocument()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '执行「刷新记录」' })).toBeEnabled()
  })

  it.each(['stopped', 'unknown', 'starting', 'degraded'])('控制面状态 %s 即使旧 jobs 说 running 也禁止执行', async (runtimeState) => {
    const http = installFetch((url) => actionResponse(url, { descriptors: [emptyJob] }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" runtimeState={runtimeState} />)
    const button = await screen.findByRole('button', { name: '执行「刷新记录」' })
    expect(button).toBeDisabled()
    fireEvent.click(button)
    expect(actionRequests(http)).toHaveLength(0)
  })

  it('jobs/bindings 未一致确认运行前不可执行，stopped 也不会复用声明放行', async () => {
    let release!: () => void
    const pending = new Promise<void>((resolve) => { release = resolve })
    const http = installFetch(async (url) => {
      if (url.endsWith('/bindings')) { await pending; return actionResponse(url, { running: false }) }
      return actionResponse(url, { descriptors: [emptyJob] })
    })
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    const button = await screen.findByRole('button', { name: '执行「刷新记录」' })
    expect(button).toBeDisabled()
    await act(async () => { release() })
    expect(button).toBeDisabled()
    expect(actionRequests(http)).toHaveLength(0)
  })

  it('不接受指向另一实例的 jobs 响应', async () => {
    const http = installFetch((url) => actionResponse(url, { instanceID: 'app-b', descriptors: [emptyJob] }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    await screen.findByText('暂无应用操作')
    expect(screen.queryByRole('button', { name: /执行/ })).not.toBeInTheDocument()
    expect(actionRequests(http)).toHaveLength(0)
  })
})

describe('参数与不可信执行结果', () => {
  it('字段 required/range 校验阻止 POST，错误不会静默修正用户输入', async () => {
    const http = installFetch((url) => actionResponse(url))
    const user = userEvent.setup()
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    await ready()
    await user.click(screen.getByRole('button', { name: '更新计数 技术人员选项' }))
    const input = screen.getByRole('textbox', { name: '更新计数 技术参数' })
    for (const value of ['{"count":0}', '{"count":9}', '{"count":1.5}']) {
      fireEvent.change(input, { target: { value } })
      expect(execute()).toBeDisabled()
      expect(screen.getByRole('alert')).toBeVisible()
      fireEvent.click(execute())
    }
    expect(actionRequests(http)).toHaveLength(0)
    expect(input).toHaveValue('{"count":1.5}')
  })

  it('JSON 对象可超过 64 字节且保留多行原文；无效/非对象/超限参数不能提交', async () => {
    const http = installFetch((url, init) => init?.method === 'POST' ? accepted() : actionResponse(url))
    const user = userEvent.setup()
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    await ready()
    await user.click(screen.getByRole('button', { name: '更新计数 技术人员选项' }))
    const input = screen.getByRole('textbox', { name: '更新计数 技术参数' })
    for (const value of ['[]', 'null', '{bad', JSON.stringify({ count: 2, note: '字'.repeat(1500) })]) {
      fireEvent.change(input, { target: { value } })
      expect(execute()).toBeDisabled()
      fireEvent.click(execute())
    }
    expect(actionRequests(http)).toHaveLength(0)
    const value = JSON.stringify({ count: 2, note: '长参数'.repeat(24) }, null, 2)
    fireEvent.change(input, { target: { value } })
    await user.click(execute())
    await screen.findByText('操作已受理')
    expect(actionRequests(http)[0].args_json).toBe(value)
  })

  it('坏 schema 禁止执行；未知 schema 约束明确由插件作完整校验', async () => {
    installFetch((url) => actionResponse(url, { descriptors: [
      { ...fieldJob, id: 'invalid', title: '损坏声明', input_schema_json: '{' },
      { ...emptyJob, input_schema_json: '{"type":"object","$ref":"#/$defs/input"}' },
    ] }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    expect(await screen.findByText('操作参数声明无效，暂不能执行。')).toBeVisible()
    expect(screen.queryByRole('button', { name: '执行「损坏声明」' })).not.toBeInTheDocument()
    expect(screen.getByText(/最终结果由插件确认/)).toHaveTextContent('$ref')
  })

  it('结果按结构化纯文本呈现，HTML/脚本/链接不执行，不猜业务成功', async () => {
    const dangerous = '<img src=x onerror="alert(1)"><script>alert(2)</script>'
    const result = { summary: dangerous, status: 'custom-status', payload: { href: 'javascript:alert(3)', enabled: false } }
    installFetch((url, init) => init?.method === 'POST' ? accepted('app-a', emptyJob.id, result)
      : actionResponse(url, { descriptors: [emptyJob] }))
    const user = userEvent.setup()
    const { container } = renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    await user.click(await screen.findByRole('button', { name: '执行「刷新记录」' }))
    await waitFor(() => expect(container.textContent).toContain('custom-status'))
    expect(container.textContent).toContain(dangerous)
    expect(screen.queryByText('javascript:alert(3)')).not.toBeInTheDocument()
    expect(container.querySelector('img, script, a[href^="javascript:"]')).toBeNull()
    await user.click(screen.getByText('查看结果原文'))
    expect(screen.getByRole('group', { name: '执行结果原文' }).textContent).toBe(JSON.stringify(result))
    expect(screen.getByText('操作已受理')).toBeVisible()
    expect(screen.queryByText('板端成功')).not.toBeInTheDocument()
  })

  it('机器字段只在原文展开，首屏显示人话摘要', async () => {
    const result = { ok: true, message: '自检完成', run_count: 3, finished_at: '2026-09-09T07:30:03Z' }
    installFetch((url, init) => init?.method === 'POST' ? accepted('app-a', emptyJob.id, result)
      : actionResponse(url, { descriptors: [emptyJob] }))
    const user = userEvent.setup()
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    await user.click(await screen.findByRole('button', { name: '执行「刷新记录」' }))
    expect(await screen.findByText(/自检完成 · 执行次数 3 · 完成时间/)).toBeVisible()
    expect(screen.queryByText('run_count')).not.toBeInTheDocument()
    expect(screen.queryByText('finished_at')).not.toBeInTheDocument()
    await user.click(screen.getByText('查看结果原文'))
    expect(screen.getByRole('group', { name: '执行结果原文' })).toHaveTextContent('run_count')
    expect(screen.getByRole('group', { name: '执行结果原文' })).toHaveTextContent('finished_at')
  })
  it.each(['', '<svg onload="alert(1)">raw response</svg>'])('空或非 JSON 结果不伪造结构：%s', async (result_json) => {
    installFetch((url, init) => init?.method === 'POST'
      ? stubResponse(200, { instance_id: 'app-a', job_id: emptyJob.id, result_json })
      : actionResponse(url, { descriptors: [emptyJob] }))
    const { container } = renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    fireEvent.click(await screen.findByRole('button', { name: '执行「刷新记录」' }))
    expect(await screen.findByText('操作已受理')).toBeVisible()
    expect(screen.getByText(result_json ? '应用返回的内容无法直接展示，请查看原文。' : '应用未返回结果内容，请查看应用记录。')).toBeVisible()
    if (result_json) {
      fireEvent.click(screen.getByText('查看结果原文'))
      expect(screen.getByRole('group', { name: '执行结果原文' })).toHaveTextContent(result_json)
      expect(container.querySelector('svg[onload]')).toBeNull()
    }
  })

  it.each([
    { instance_id: 'app-b', job_id: emptyJob.id, result_json: '{}' },
    { instance_id: 'app-a', job_id: 'other-job', result_json: '{}' },
    { instance_id: 'app-a', job_id: emptyJob.id, result_json: null },
  ])('HTTP 200 但响应身份/形状错误也不产生假成功（%j）', async (body) => {
    installFetch((url, init) => init?.method === 'POST' ? stubResponse(200, body)
      : actionResponse(url, { descriptors: [emptyJob] }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    fireEvent.click(await screen.findByRole('button', { name: '执行「刷新记录」' }))
    expect(await screen.findByText(/未取得可信的执行结果/)).toBeVisible()
    expect(screen.queryByText('操作已受理')).not.toBeInTheDocument()
  })

  it('运行期删除手动声明后旧按钮不再能发起请求', async () => {
    let present = true
    const post = vi.spyOn(api, 'runAppJob')
    installFetch((url) => actionResponse(url, { descriptors: present ? [emptyJob] : [] }))
    const { queryClient } = renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    const button = await screen.findByRole('button', { name: '执行「刷新记录」' })
    present = false
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['application-plane'] }) })
    await waitFor(() => expect(screen.queryByRole('button', { name: '执行「刷新记录」' })).not.toBeInTheDocument())
    fireEvent.click(button)
    expect(post).not.toHaveBeenCalled()
  })

  it('切换实例清空参数，控制面前缀不进入 POST 目标', async () => {
    const http = installFetch((url, init) => init?.method === 'POST' ? accepted('app-b') : actionResponse(url))
    function Switcher() {
      const [id, setID] = useState('app-a')
      return <><button onClick={() => setID('app-b')}>切换应用</button><ApplicationPlane instanceID={id} /></>
    }
    const user = userEvent.setup()
    renderWithProviders(<Switcher />)
    await ready()
    fireEvent.change(countInput(), { target: { value: '2' } })
    await user.click(screen.getByRole('button', { name: '切换应用' }))
    await ready()
    expect(countInput()).toHaveValue('')
    expect(execute()).toBeDisabled()
    fireEvent.change(countInput(), { target: { value: '1' } })
    await user.click(execute())
    await screen.findByText('操作已受理')
    expect(http.calls.filter((call) => call.method === 'POST')[0].url).toBe('/api/plugin-instances/app-b/jobs/update-count/run')
  })
})
