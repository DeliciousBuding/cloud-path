import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useApplicationPlane } from '@/hooks/useApplicationPlane'
import { useAuth } from '@/store/auth'
import { useLive } from '@/store/ws'
import { appRecord, appResponse, appUser } from '@/test/application-plane'
import { installFetch, stubResponse } from '@/test/http'
import type { StubResponse } from '@/test/http'
import { resetStores } from '@/test/render'

const clients: QueryClient[] = []
function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { gcTime: 0, retry: false } } })
  clients.push(client)
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
}
async function advance(ms = 20) { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }

beforeEach(() => {
  vi.useFakeTimers()
  resetStores()
  useAuth.setState({ status: 'in', user: appUser })
  useLive.setState({ status: 'open' })
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  vi.useRealTimers()
})

describe('应用查询与单例实时通道的接线', () => {
  it('紧邻的不同实例通知不会吞掉当前实例的更新；突发推送只补读一次', async () => {
    let record = appRecord('same-record', { count: 1 })
    const http = installFetch((url) => appResponse(url, { records: [record] }))
    const { result } = renderHook(() => useApplicationPlane('app-a'), { wrapper: wrapper() })
    await advance()
    expect(result.current.records.data?.records).toHaveLength(1)
    record = appRecord('same-record', { count: 2 })
    act(() => {
      for (let sequence = 1; sequence <= 25; sequence++) useLive.setState({ domainRecord: { instanceID: 'app-a', sequence } })
      useLive.setState({ domainRecord: { instanceID: 'app-b', sequence: 26 } })
    })
    await advance(100)
    expect(http.to('/app-a/records')).toHaveLength(2)
    expect(result.current.records.data?.records).toEqual([record])
    act(() => useLive.setState({ domainRecord: { instanceID: 'app-b', sequence: 27 } }))
    await advance(100)
    expect(http.to('/app-a/records')).toHaveLength(2)
  })

  it('连接建立和重建都补读 REST，断线窗口的同键覆盖不积累重复记录', async () => {
    let record = appRecord('same-record', { count: 1 })
    const http = installFetch((url) => appResponse(url, { records: [record] }))
    const { result } = renderHook(() => useApplicationPlane('app-a', 20, 'sample'), { wrapper: wrapper() })
    await advance()
    act(() => useLive.setState({ connectionEpoch: 1 }))
    await advance(100)
    expect(http.to('/app-a/records')).toHaveLength(2)
    act(() => useLive.setState({ status: 'closed' }))
    record = appRecord('same-record', { count: 3 })
    await advance(1000)
    expect(result.current.records.data?.records[0].data_json).toContain('1')
    act(() => useLive.setState({ status: 'open', connectionEpoch: 2 }))
    await advance(100)
    expect(result.current.records.data?.records).toEqual([record])
    expect(http.to('/app-a/records')).toHaveLength(3)
    expect(http.to('/app-a/records').every((c) => c.url.includes('offset=20') && c.url.includes('record_type=sample'))).toBe(true)
  })

  it('实时连接正常时仍轮询，停止清空运行态但保留记录/计划，重新启动可恢复', async () => {
    let running = true
    const http = installFetch((url) => appResponse(url, { running }))
    const { result } = renderHook(() => useApplicationPlane('app-a'), { wrapper: wrapper() })
    await advance()
    expect(result.current.running).toBe(true)
    running = false
    await advance(10_020)
    expect(result.current.running).toBe(false)
    expect(result.current.bindings.data?.bindings).toEqual([])
    expect(result.current.jobs.data?.jobs).toEqual([])
    expect(result.current.records.data?.records).toHaveLength(1)
    expect(result.current.jobs.data?.scheduled).toHaveLength(1)
    running = true
    await advance(10_020)
    expect(result.current.running).toBe(true)
    expect(result.current.bindings.data?.bindings).toHaveLength(1)
    expect(http.to('/app-a/jobs')).toHaveLength(3)
  })

  it('控制投影发生启停变化时立即补读，不等待下一次轮询', async () => {
    let running = true
    installFetch((url) => appResponse(url, { running }))
    const { result, rerender } = renderHook(({ life }) => useApplicationPlane('app-a', 0, '', life), {
      wrapper: wrapper(), initialProps: { life: 'running' },
    })
    await advance()
    running = false
    rerender({ life: 'stopped' })
    await advance()
    expect(result.current.running).toBe(false)
  })

  it('后台读取失败后不继续用另一端点冒充已确认的运行状态', async () => {
    let failed = false
    installFetch((url) => failed && url.endsWith('/jobs') ? stubResponse(500, {}) : appResponse(url))
    const { result } = renderHook(() => useApplicationPlane('app-a'), { wrapper: wrapper() })
    await advance()
    expect(result.current.running).toBe(true)
    failed = true
    await advance(10_020)
    expect(result.current.jobs.isError).toBe(true)
    expect(result.current.running).toBeUndefined()
  })

  it('403 不持续轮询被拒绝的数据，但允许显式重试', async () => {
    let denied = true
    const http = installFetch((url) => denied && url.includes('/records?') ? stubResponse(403, {}) : appResponse(url))
    const { result } = renderHook(() => useApplicationPlane('app-a'), { wrapper: wrapper() })
    await advance()
    expect(result.current.records.isError).toBe(true)
    await advance(30_020)
    expect(http.to('/app-a/records')).toHaveLength(1)
    denied = false
    await act(async () => { await result.current.records.refetch() })
    await advance()
    expect(result.current.records.isSuccess).toBe(true)
    expect(http.to('/app-a/records')).toHaveLength(2)
  })

  it.each([200, 401])('切实例取消请求，旧实例的迟到 %s 不覆盖新数据或登出新会话', async (lateStatus) => {
    let release!: (value: StubResponse) => void
    let signal: AbortSignal | null | undefined
    const pending = new Promise<StubResponse>((resolve) => { release = resolve })
    installFetch((url, init) => {
      if (url.includes('/app-a/records?')) { signal = init?.signal; return pending }
      return appResponse(url, { records: [appRecord('only-b', '乙实例的记录')] })
    })
    const { result, rerender } = renderHook(({ id }) => useApplicationPlane(id), {
      wrapper: wrapper(), initialProps: { id: 'app-a' },
    })
    await advance()
    expect(result.current.records.isPending).toBe(true)
    rerender({ id: 'app-b' })
    await advance()
    expect(signal?.aborted).toBe(true)
    expect(result.current.records.data?.instance_id).toBe('app-b')
    release(lateStatus === 200 ? appResponse('/api/plugin-instances/app-a/records', { records: [appRecord('old', '甲的迟到记录')] })
      : stubResponse(401, { error: 'authentication required' }))
    await advance()
    expect(result.current.records.data?.records[0].record_id).toBe('only-b')
    expect(useAuth.getState().status).toBe('in')
  })

  it.each([{ id: 8, tenant_id: 1 }, { id: 8, tenant_id: 2 }])('账号/租户变更不复用旧记录缓存（%j）', async (identity) => {
    let failed = false
    installFetch((url) => failed && url.includes('/records?') ? stubResponse(403, {}) : appResponse(url))
    const { result } = renderHook(() => useApplicationPlane('app-a'), { wrapper: wrapper() })
    await advance()
    expect(result.current.records.data?.records).toHaveLength(1)
    failed = true
    act(() => useAuth.setState({ user: { ...appUser, ...identity } }))
    await advance()
    expect(result.current.records.isError).toBe(true)
    expect(result.current.records.data).toBeUndefined()
  })

  it('未登录不拉取，退出后移除订阅与待刷新的定时器', async () => {
    useAuth.setState({ status: 'out', user: null })
    const http = installFetch((url) => appResponse(url))
    const { result } = renderHook(() => useApplicationPlane('app-a'), { wrapper: wrapper() })
    await advance()
    expect(http.calls).toHaveLength(0)
    act(() => useAuth.setState({ status: 'in', user: appUser }))
    await advance()
    expect(result.current.canRead).toBe(true)
    const before = http.calls.length
    act(() => {
      useLive.setState({ domainRecord: { instanceID: 'app-a', sequence: 1 } })
      useAuth.setState({ status: 'out', user: null })
    })
    await advance(20_000)
    expect(result.current.canRead).toBe(false)
    expect(result.current.records.data).toBeUndefined()
    expect(http.calls).toHaveLength(before)
  })
})
