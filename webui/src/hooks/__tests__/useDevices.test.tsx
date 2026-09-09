import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import type { ReactNode } from 'react'
import { useDevices } from '@/hooks/useDevices'
import { useLive } from '@/store/ws'
import { useAuth } from '@/store/auth'
import { makeDeviceView } from '@/test/fixtures'
import { installFetch, stubResponse } from '@/test/http'
import { resetStores } from '@/test/render'

function makeWrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0, refetchInterval: false } },
  })
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  }
}

const KEY = 'edge-1/dev-9'
const REST_ONLINE = makeDeviceView({ id: KEY, edge_id: 'edge-1', online: true, updated_at: 20, last_seen: 20 })
const LIVE_OFFLINE = makeDeviceView({ id: KEY, edge_id: 'edge-1', online: false, updated_at: 10, last_seen: 10 })

beforeEach(() => { resetStores() })

describe('useDevices：REST / WS 权威边界', () => {
  it('WS open 时实时状态覆盖 REST', async () => {
    installFetch(() => stubResponse(200, { devices: [REST_ONLINE] }))
    useLive.setState({ status: 'open', devices: { [KEY]: LIVE_OFFLINE } })
    const { result } = renderHook(() => useDevices(), { wrapper: makeWrapper() })
    await waitFor(() => expect(result.current.list).toHaveLength(1))
    expect(result.current.list[0].online).toBe(false)
    expect(result.current.loading).toBe(false)
  })

  it('WS closed 后 REST 轮询重新成为权威，旧实时态不能粘住', async () => {
    installFetch(() => stubResponse(200, { devices: [REST_ONLINE] }))
    useLive.setState({ status: 'closed', devices: { [KEY]: LIVE_OFFLINE } })
    const { result } = renderHook(() => useDevices(), { wrapper: makeWrapper() })
    await waitFor(() => expect(result.current.list[0]?.online).toBe(true))
  })

  it('WS snapshot 是权威集合：REST 里多出的旧键不得复活', async () => {
    installFetch(() => stubResponse(200, { devices: [REST_ONLINE] }))
    useLive.setState({ status: 'open', connectionEpoch: 1, snapshotEpoch: 1, devices: {} })
    const { result } = renderHook(() => useDevices(), { wrapper: makeWrapper() })
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.list).toEqual([])
    expect(result.current.error).toBeNull()
  })

  it('账号/租户切换时使用独立查询缓存，不展示上一身份的 REST 数据', async () => {
    const a = makeDeviceView({ id: 'edge-a/dev-a', edge_id: 'edge-a' })
    const b = makeDeviceView({ id: 'edge-b/dev-b', edge_id: 'edge-b' })
    const user = { id: 1, username: 'a', name: 'A', role: 'operator' as const, tenant_id: 1, tenant_slug: 'one' }
    installFetch(() => stubResponse(200, { devices: [useAuth.getState().user?.tenant_id === 2 ? b : a] }))
    useAuth.setState({ status: 'in', user })
    const { result } = renderHook(() => useDevices(), { wrapper: makeWrapper() })
    await waitFor(() => expect(result.current.list[0]?.id).toBe('edge-a/dev-a'))

    act(() => useAuth.setState({
      status: 'in',
      user: { ...user, id: 2, username: 'b', tenant_id: 2, tenant_slug: 'two' },
    }))
    await waitFor(() => expect(result.current.list[0]?.id).toBe('edge-b/dev-b'))
  })

  it('WS open 但 snapshot 未到时不吞 REST 错误；空 snapshot 落地后才成为权威事实', async () => {
    const http = installFetch(() => stubResponse(502, { error: 'bad gateway' }))
    useLive.setState({ status: 'open', connectionEpoch: 1, snapshotEpoch: 0, devices: {} })
    const { result } = renderHook(() => useDevices(), { wrapper: makeWrapper() })
    await waitFor(() => expect(http.to('/api/devices')).toHaveLength(1))
    await waitFor(() => expect(result.current.error).not.toBeNull())
    act(() => useLive.setState({ snapshotEpoch: 1 }))
    await waitFor(() => expect(result.current.error).toBeNull())
    expect(result.current.list).toEqual([])
  })

  it('WS closed 且 REST 成功返回空集合时，旧实时键必须被清除', async () => {
    installFetch(() => stubResponse(200, { devices: [] }))
    useLive.setState({ status: 'closed', devices: { [KEY]: LIVE_OFFLINE } })
    const { result } = renderHook(() => useDevices(), { wrapper: makeWrapper() })
    await waitFor(() => expect(result.current.list).toEqual([]))
  })

  it('WS closed 且 REST 失败时，最后实时数据仍可兜底并保留错误边界', async () => {
    installFetch(() => stubResponse(502, { error: 'bad gateway' }))
    useLive.setState({ status: 'closed', devices: { [KEY]: LIVE_OFFLINE } })
    const { result } = renderHook(() => useDevices(), { wrapper: makeWrapper() })
    await waitFor(() => expect(result.current.list).toHaveLength(1))
    expect(result.current.list[0].id).toBe(KEY)
    expect(result.current.error).toBeNull()
  })
})
