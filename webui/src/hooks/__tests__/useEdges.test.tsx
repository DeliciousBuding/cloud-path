import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import type { ReactNode } from 'react'
import { useEdges } from '@/hooks/useEdges'
import { useLive } from '@/store/ws'
import type { EdgeView } from '@/lib/types'
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

const REST_ONLINE: EdgeView = {
  edge_id: 'edge-1', online: true, version: 'rest', devices: ['edge-1/dev-9'], connected_at: 20,
}
const LIVE_OFFLINE: EdgeView = {
  edge_id: 'edge-1', online: false, version: 'ws', devices: ['edge-1/dev-9'], connected_at: 10,
}

beforeEach(() => { resetStores() })

describe('useEdges：REST / WS 权威边界', () => {
  it('WS open 时实时状态覆盖 REST', async () => {
    installFetch(() => stubResponse(200, { edges: [REST_ONLINE] }))
    useLive.setState({ status: 'open', edges: { 'edge-1': LIVE_OFFLINE } })
    const { result } = renderHook(() => useEdges(), { wrapper: makeWrapper() })
    await waitFor(() => expect(result.current.list).toHaveLength(1))
    expect(result.current.list[0].online).toBe(false)
    expect(result.current.list[0].version).toBe('ws')
  })

  it('WS closed 后 REST 轮询重新成为权威，旧在线态不能粘住', async () => {
    installFetch(() => stubResponse(200, { edges: [REST_ONLINE] }))
    useLive.setState({ status: 'closed', edges: { 'edge-1': LIVE_OFFLINE } })
    const { result } = renderHook(() => useEdges(), { wrapper: makeWrapper() })
    await waitFor(() => expect(result.current.list[0]?.online).toBe(true))
    expect(result.current.list[0].version).toBe('rest')
  })

  it('WS open 的空快照是权威事实，REST 502 不应伪装成错误或空态', async () => {
    const http = installFetch(() => stubResponse(502, { error: 'bad gateway' }))
    useLive.setState({ status: 'open', edges: {} })
    const { result } = renderHook(() => useEdges(), { wrapper: makeWrapper() })
    await waitFor(() => expect(http.to('/api/edges')).toHaveLength(1))
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)) })
    expect(result.current.list).toEqual([])
    expect(result.current.error).toBeNull()
  })

  it('WS closed 且 REST 成功返回空集合时，旧实时键必须被清除', async () => {
    installFetch(() => stubResponse(200, { edges: [] }))
    useLive.setState({ status: 'closed', edges: { 'edge-1': LIVE_OFFLINE } })
    const { result } = renderHook(() => useEdges(), { wrapper: makeWrapper() })
    await waitFor(() => expect(result.current.list).toEqual([]))
  })

  it('WS closed 且 REST 失败时，最后实时数据仍可兜底', async () => {
    installFetch(() => stubResponse(502, { error: 'bad gateway' }))
    useLive.setState({ status: 'closed', edges: { 'edge-1': LIVE_OFFLINE } })
    const { result } = renderHook(() => useEdges(), { wrapper: makeWrapper() })
    await waitFor(() => expect(result.current.list).toHaveLength(1))
    expect(result.current.list[0].edge_id).toBe('edge-1')
    expect(result.current.error).toBeNull()
  })
})
