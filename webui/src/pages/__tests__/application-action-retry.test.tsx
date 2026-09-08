import { useState } from 'react'
import { onlineManager } from '@tanstack/react-query'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApplicationPlane } from '@/components/plugin/ApplicationPlane'
import { APP_ACTION_TIMEOUT_MS } from '@/lib/application-actions'
import { useAuth } from '@/store/auth'
import { useToasts } from '@/store/toast'
import { appUser } from '@/test/application-plane'
import { accepted, actionRequests, actionResponse, emptyJob } from '@/test/application-actions'
import { installFetch, stubResponse } from '@/test/http'
import type { StubResponse } from '@/test/http'
import { renderWithProviders, resetStores } from '@/test/render'

beforeEach(() => {
  resetStores()
  useAuth.setState({ status: 'in', user: { ...appUser, role: 'operator' } })
})
afterEach(() => { onlineManager.setOnline(true); vi.useRealTimers(); vi.restoreAllMocks() })
async function button(name = '执行「刷新记录」') {
  const result = await screen.findByRole('button', { name })
  await waitFor(() => expect(result).toBeEnabled())
  return result
}
async function advance(ms = 20) { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }

describe('应用操作只在明确用户请求时重试', () => {
  it('失败后同一逻辑请求原样重试，确认成功后再次执行才使用新 key', async () => {
    let calls = 0
    const http = installFetch((url, init) => init?.method === 'POST'
      ? ++calls === 1 ? stubResponse(503, { error: 'result uncertain' }) : accepted('app-a', emptyJob.id)
      : actionResponse(url, { descriptors: [emptyJob] }))
    const user = userEvent.setup()
    const { queryClient } = renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    await user.click(await button())
    expect(await screen.findByText('执行结果待确认，请先查看应用记录。')).toBeVisible()
    expect(screen.getByText(/不会自动重试/)).toBeVisible()
    expect(actionRequests(http)).toHaveLength(1)
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['application-plane'] }) })
    expect(actionRequests(http)).toHaveLength(1)
    await user.click(await button('重试「刷新记录」'))
    await screen.findByText('操作已受理')
    expect(actionRequests(http)[1]).toEqual(actionRequests(http)[0])
    await user.click(await button('再次执行「刷新记录」'))
    await waitFor(() => expect(actionRequests(http)).toHaveLength(3))
    expect(actionRequests(http)[2].args_json).toBe('{}')
    expect(actionRequests(http)[2].idempotency_key).not.toBe(actionRequests(http)[0].idempotency_key)
  })

  it('编辑参数即新语义，即使改回原值也不复用失败请求 key', async () => {
    const http = installFetch((url, init) => init?.method === 'POST' ? stubResponse(400, { error: 'plugin rejected count' }) : actionResponse(url))
    const user = userEvent.setup()
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    const input = await screen.findByRole('spinbutton', { name: /次数/ })
    await user.type(input, '2')
    await user.click(await button('执行「更新计数」'))
    await screen.findByText(/参数未被接受/)
    const original = actionRequests(http)[0]
    fireEvent.change(input, { target: { value: '3' } })
    fireEvent.change(input, { target: { value: '2' } })
    expect(screen.queryByText(/参数未被接受/)).not.toBeInTheDocument()
    await user.click(await button('执行「更新计数」'))
    await waitFor(() => expect(actionRequests(http)).toHaveLength(2))
    expect(actionRequests(http)[1].args_json).toBe(original.args_json)
    expect(actionRequests(http)[1].idempotency_key).not.toBe(original.idempotency_key)
  })

  it('仅切换 JSON/字段视图不是新请求；重试保留原始空白与 key', async () => {
    const http = installFetch((url, init) => init?.method === 'POST' ? stubResponse(409, { error: 'conflict' }) : actionResponse(url))
    const user = userEvent.setup()
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    await screen.findByRole('spinbutton', { name: /次数/ })
    await user.click(screen.getByRole('button', { name: '更新计数 编辑 JSON' }))
    const args = JSON.stringify({ count: 2 }, null, 2)
    fireEvent.change(screen.getByRole('textbox', { name: '更新计数 JSON 参数' }), { target: { value: args } })
    await user.click(await button('执行「更新计数」'))
    await screen.findByText(/操作发生冲突/)
    await user.click(screen.getByRole('button', { name: '更新计数 使用字段' }))
    await user.click(await button('重试「更新计数」'))
    await waitFor(() => expect(actionRequests(http)).toHaveLength(2))
    expect(actionRequests(http)[1]).toEqual(actionRequests(http)[0])
    expect(actionRequests(http)[1].args_json).toBe(args)
  })

  it.each([
    [400, '参数未被接受'], [403, '没有执行权限'], [404, '应用操作或目标不存在'],
    [409, '操作发生冲突'], [503, '执行结果待确认'],
  ])('HTTP %s 如实说明失败/不确定，不自动重试也不显示成功', async (status, text) => {
    const http = installFetch((url, init) => init?.method === 'POST' ? stubResponse(Number(status), { error: '<script>plugin failure</script>' })
      : actionResponse(url, { descriptors: [emptyJob] }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    fireEvent.click(await button())
    expect(await screen.findByText(new RegExp(String(text)))).toBeVisible()
    expect(screen.getByText(/不会自动重试/)).toBeVisible()
    expect(screen.queryByText('操作已受理')).not.toBeInTheDocument()
    expect(actionRequests(http)).toHaveLength(1)
    fireEvent.click(screen.getByText('错误详情'))
    expect(screen.getByText('<script>plugin failure</script>')).toBeVisible()
    expect(document.querySelector('script')).toBeNull()
  })

  it('请求超时取消本地等待；不声称失败未执行，后续手动重试仍用同一 key', async () => {
    vi.useFakeTimers()
    let calls = 0
    let signal: AbortSignal | null | undefined
    const http = installFetch((url, init) => {
      if (init?.method !== 'POST') return actionResponse(url, { descriptors: [emptyJob] })
      if (++calls > 1) return accepted('app-a', emptyJob.id)
      signal = init.signal
      return new Promise<StubResponse>((_resolve, reject) => signal?.addEventListener('abort', () => reject(signal?.reason)))
    })
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    await advance()
    fireEvent.click(screen.getByRole('button', { name: '执行「刷新记录」' }))
    await advance()
    expect(screen.getByRole('button', { name: '执行「刷新记录」' })).toBeDisabled()
    await advance(APP_ACTION_TIMEOUT_MS)
    expect(signal?.aborted).toBe(true)
    expect(screen.getByText('请求超时，执行结果待确认。')).toBeVisible()
    expect(screen.getByText(/不会自动重试/)).toBeVisible()
    await advance(30_000)
    expect(actionRequests(http)).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: '重试「刷新记录」' }))
    await advance()
    expect(screen.getByText('操作已受理')).toBeVisible()
    expect(actionRequests(http)[1]).toEqual(actionRequests(http)[0])
  })

  it('离线点击不会排队等待恢复后偷偷执行，网络恢复也不重放失败 mutation', async () => {
    const http = installFetch((url, init) => {
      if (init?.method === 'POST') throw new TypeError('offline')
      return actionResponse(url, { descriptors: [emptyJob] })
    })
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    const run = await button()
    act(() => onlineManager.setOnline(false))
    fireEvent.click(run)
    await screen.findByText(/未取得可信的执行结果/)
    expect(actionRequests(http)).toHaveLength(1)
    act(() => onlineManager.setOnline(true))
    await button('重试「刷新记录」')
    expect(actionRequests(http)).toHaveLength(1)
  })

  it('后台状态读取失败时禁用执行但保留失败 key；恢复读取后仍原样重试', async () => {
    let failReads = false
    const http = installFetch((url, init) => {
      if (init?.method === 'POST') { failReads = true; return stubResponse(503, {}) }
      if (url.endsWith('/jobs') && failReads) return stubResponse(503, {})
      return actionResponse(url, { descriptors: [emptyJob] })
    })
    const { queryClient } = renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    fireEvent.click(await button())
    await screen.findByText('应用操作加载失败')
    expect(screen.getByRole('button', { name: '重试「刷新记录」' })).toBeDisabled()
    expect(screen.getByText(/不会自动重试/)).toBeVisible()
    failReads = false
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['application-plane'] }) })
    fireEvent.click(await button('重试「刷新记录」'))
    await waitFor(() => expect(actionRequests(http)).toHaveLength(2))
    expect(actionRequests(http)[1]).toEqual(actionRequests(http)[0])
  })

  it('快速重复点击只有一次 POST，等待期间不能编辑语义', async () => {
    let release!: (response: StubResponse) => void
    const pending = new Promise<StubResponse>((resolve) => { release = resolve })
    const http = installFetch((url, init) => init?.method === 'POST' ? pending : actionResponse(url))
    const user = userEvent.setup()
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    const input = await screen.findByRole('spinbutton', { name: /次数/ })
    await user.type(input, '2')
    await user.dblClick(await button('执行「更新计数」'))
    expect(input).toBeDisabled()
    expect(actionRequests(http)).toHaveLength(1)
    await act(async () => { release(accepted()) })
    await screen.findByText('操作已受理')
  })
})

describe('应用操作的身份、实例、生命周期隔离', () => {
  it('点击后立即撤销身份，尚未进入 fetch 的 mutation 不会借用新身份发送', async () => {
    const http = installFetch((url) => actionResponse(url, { descriptors: [emptyJob] }))
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    const run = await button()
    act(() => {
      fireEvent.click(run)
      useAuth.setState({ user: { ...appUser, role: 'viewer' } })
    })
    await act(async () => { await Promise.resolve() })
    expect(actionRequests(http)).toHaveLength(0)
    expect(screen.queryByRole('button', { name: /执行|重试同一/ })).not.toBeInTheDocument()
  })

  it.each([
    { id: 8, tenant_id: 1, role: 'operator' as const },
    { id: 7, tenant_id: 2, role: 'operator' as const },
    { id: 7, tenant_id: 1, role: 'viewer' as const },
  ])('切身份取消旧请求，迟到 401 不登出新身份或复活旧执行入口（%j）', async (identity) => {
    let release!: (response: StubResponse) => void
    let signal: AbortSignal | null | undefined
    const pending = new Promise<StubResponse>((resolve) => { release = resolve })
    const http = installFetch((url, init) => {
      if (init?.method === 'POST') { signal = init.signal; return pending }
      return actionResponse(url, { descriptors: [emptyJob] })
    })
    renderWithProviders(<ApplicationPlane instanceID="app-a" />)
    const oldButton = await button()
    fireEvent.click(oldButton)
    await waitFor(() => expect(signal).toBeDefined())
    act(() => useAuth.setState({ user: { ...appUser, ...identity } }))
    expect(signal?.aborted).toBe(true)
    await act(async () => { release(stubResponse(401, { error: 'late authentication failure' })) })
    expect(useAuth.getState()).toMatchObject({ status: 'in', user: identity })
    expect(screen.queryByText('操作已受理')).not.toBeInTheDocument()
    expect(screen.queryByText('本次请求详情')).not.toBeInTheDocument()
    fireEvent.click(oldButton)
    expect(actionRequests(http)).toHaveLength(1)
    expect(useToasts.getState().items).toEqual([])
  })

  it.each(['instance', 'lifecycle', 'descriptor'] as const)('%s 切换取消旧 POST 并清除旧结果/key，不接收迟到成功', async (change) => {
    let release!: (response: StubResponse) => void
    let signal: AbortSignal | null | undefined
    let posts = 0
    let revision = 1
    const pending = new Promise<StubResponse>((resolve) => { release = resolve })
    const http = installFetch((url, init) => {
      if (init?.method === 'POST') {
        if (++posts === 1) { signal = init.signal; return pending }
        return accepted(change === 'instance' ? 'app-b' : 'app-a', emptyJob.id)
      }
      return actionResponse(url, { descriptors: [{ ...emptyJob, title: revision === 1 ? emptyJob.title : '新版刷新' }] })
    })
    function Switcher() {
      const [next, setNext] = useState(false)
      return <><button onClick={() => { revision = 2; setNext(true) }}>更换上下文</button>
        <ApplicationPlane instanceID={next && change === 'instance' ? 'app-b' : 'app-a'}
          lifecycleKey={next && change === 'lifecycle' ? 'run-2' : 'run-1'} /></>
    }
    const { queryClient } = renderWithProviders(<Switcher />)
    fireEvent.click(await button())
    await waitFor(() => expect(signal).toBeDefined())
    fireEvent.click(screen.getByRole('button', { name: '更换上下文' }))
    if (change === 'descriptor') await act(async () => { await queryClient.invalidateQueries({ queryKey: ['application-plane'] }) })
    await button('执行「新版刷新」')
    expect(signal?.aborted).toBe(true)
    await act(async () => { release(accepted('app-a', emptyJob.id, { summary: '上一个上下文的迟到结果' })) })
    expect(screen.queryByText('上一个上下文的迟到结果')).not.toBeInTheDocument()
    expect(screen.queryByText('本次请求详情')).not.toBeInTheDocument()
    fireEvent.click(await button('执行「新版刷新」'))
    await screen.findByText('操作已受理')
    expect(actionRequests(http)[1].idempotency_key).not.toBe(actionRequests(http)[0].idempotency_key)
    expect(useToasts.getState().items).toEqual([])
  })

  it('生命周期变化后新 GET 未完成时不能拿旧 running/descriptor 放行', async () => {
    let revision = 1
    let release!: () => void
    const pending = new Promise<void>((resolve) => { release = resolve })
    const http = installFetch(async (url) => {
      if (revision === 2 && url.endsWith('/jobs')) await pending
      return actionResponse(url, { descriptors: [emptyJob], running: revision === 1 })
    })
    function Switcher() {
      const [life, setLife] = useState('run-1')
      return <><button onClick={() => { revision = 2; setLife('run-2') }}>切换生命周期</button>
        <ApplicationPlane instanceID="app-a" lifecycleKey={life} /></>
    }
    renderWithProviders(<Switcher />)
    await button()
    fireEvent.click(screen.getByRole('button', { name: '切换生命周期' }))
    expect(screen.queryByRole('button', { name: '执行「刷新记录」' })).not.toBeInTheDocument()
    await act(async () => { release() })
    expect((await screen.findByRole('button', { name: '执行「刷新记录」' }))).toBeDisabled()
    expect(actionRequests(http)).toHaveLength(0)
  })
})
