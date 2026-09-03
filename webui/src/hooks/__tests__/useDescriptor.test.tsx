// 数据层契约测试：Descriptor 的四条来源通道（ws / inline / rest / bulk）优先级、
// Schema 端点 404 时的通用回落、以及命令集随声明出现/消失。
// 这一层是「后端未就绪也不白屏」的关键接缝，必须有可重复的回归保护。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import type { ReactNode } from 'react'
import { useCapabilityIndex, useDeviceDescriptor } from '@/hooks/useDescriptor'
import { EMPTY_INDEX } from '@/lib/descriptor'
import { useLive } from '@/store/ws'
import { capRelay, capTemperature, makeDescriptor, makeDeviceView } from '@/test/fixtures'
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

beforeEach(() => { resetStores() })

describe('useDeviceDescriptor：来源优先级与回落', () => {
  it('Schema 端点全 404（后端未就绪）→ descriptor null / source=none / 命令回落适配器白名单', async () => {
    installFetch(() => stubResponse(404, { error: 'not found' }))
    const { result } = renderHook(
      () => useDeviceDescriptor(KEY, 'edge-1', 'dev-9', { adapterCommands: ['raw'] }),
      { wrapper: makeWrapper() },
    )
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.descriptor).toBeNull()
    expect(result.current.source).toBe('none')
    expect(result.current.capabilities).toBe(EMPTY_INDEX)
    expect(result.current.commands).toEqual({ actions: [{ cmd: 'raw', label: 'Raw' }], source: 'adapter' })
  })

  it('设备载荷内联 Descriptor → source=inline（无需额外请求）', async () => {
    const http = installFetch(() => stubResponse(404, { error: 'not found' }))
    const device = makeDeviceView({ state: { descriptor: makeDescriptor() } })
    const { result } = renderHook(
      () => useDeviceDescriptor(KEY, 'edge-1', 'dev-9', { device }),
      { wrapper: makeWrapper() },
    )
    await waitFor(() => expect(result.current.source).toBe('inline'))
    expect(result.current.descriptor?.device_id).toBe(KEY)
    expect(http.to('/api/descriptors')).toHaveLength(1)
  })

  it('单设备 REST 命中 → source=rest，随行 capabilities 进索引，命令集来自 Capability actions', async () => {
    installFetch((url) => {
      if (url === '/api/capabilities') return stubResponse(200, { capabilities: [capTemperature] })
      if (url.endsWith('/descriptor')) {
        return stubResponse(200, { descriptor: makeDescriptor(), capabilities: [capRelay] })
      }
      return stubResponse(404, { error: 'not found' })
    })
    const { result } = renderHook(
      () => useDeviceDescriptor(KEY, 'edge-1', 'dev-9'),
      { wrapper: makeWrapper() },
    )
    await waitFor(() => expect(result.current.source).toBe('rest'))
    expect(result.current.capabilities.docs.map((d) => d.metadata.id).sort()).toEqual([
      'cloudpath.dev/capability/temperature@1', 'example.dev/capability/relay@2',
    ])
    expect(result.current.commands.source).toBe('descriptor')
    expect(result.current.commands.actions.map((a) => a.cmd)).toContain('relay_on')
  })

  it('批量端点命中 → source=bulk，且不再单设备探测（省一次请求）', async () => {
    const http = installFetch((url) => (url === '/api/descriptors'
      ? stubResponse(200, { descriptors: [makeDescriptor()], capabilities: [capTemperature] })
      : stubResponse(404, { error: 'not found' })))
    const { result } = renderHook(
      () => useDeviceDescriptor(KEY, 'edge-1', 'dev-9'),
      { wrapper: makeWrapper() },
    )
    await waitFor(() => expect(result.current.source).toBe('bulk'))
    expect(http.to(`${KEY}/descriptor`)).toHaveLength(0)
  })

  it('WS 实时 Descriptor 最高优先，并跳过 REST 探测', async () => {
    useLive.setState({ descriptors: { [KEY]: makeDescriptor({ status: 'degraded' }) } })
    const http = installFetch(() => stubResponse(404, { error: 'not found' }))
    const { result } = renderHook(
      () => useDeviceDescriptor(KEY, 'edge-1', 'dev-9'),
      { wrapper: makeWrapper() },
    )
    await waitFor(() => expect(result.current.source).toBe('ws'))
    expect(result.current.descriptor?.status).toBe('degraded')
    expect(http.to('dev-9/descriptor')).toHaveLength(0)
  })

  it('skipBulk 时不打批量端点（详情页已单独探测）', async () => {
    const http = installFetch(() => stubResponse(404, { error: 'not found' }))
    renderHook(
      () => useDeviceDescriptor(KEY, 'edge-1', 'dev-9', { skipBulk: true }),
      { wrapper: makeWrapper() },
    )
    await waitFor(() => expect(http.to('/api/capabilities').length).toBeGreaterThan(0))
    expect(http.to('/api/descriptors')).toHaveLength(0)
  })
})

describe('useCapabilityIndex：无设备上下文的 catalog', () => {
  it('catalog 200 → 索引可用；404 → 空索引（事件/命令标签回落 humanize）', async () => {
    installFetch((url) => (url === '/api/capabilities'
      ? stubResponse(200, { capabilities: [capTemperature] })
      : stubResponse(404, {})))
    const ok = renderHook(() => useCapabilityIndex(), { wrapper: makeWrapper() })
    await waitFor(() => expect(ok.result.current.docs).toHaveLength(1))

    resetStores()
    installFetch(() => stubResponse(404, {}))
    const absent = renderHook(() => useCapabilityIndex(), { wrapper: makeWrapper() })
    await waitFor(() => expect(absent.result.current).toBe(EMPTY_INDEX))
  })
})