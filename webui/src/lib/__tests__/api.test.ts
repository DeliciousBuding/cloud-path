// REST 客户端契约测试：冻结路径、鉴权头、401 全局同步、Schema 面缺席（404/405/501）→ null。
// 网关/网络故障必须保持错误态；只有「端点不存在」才能走通用回落。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, getToken, setToken, wsUrl } from '@/lib/api'
import { useAuth } from '@/store/auth'
import { installFetch, stubResponse } from '@/test/http'
import { resetStores } from '@/test/render'

beforeEach(() => { resetStores() })

describe('鉴权接缝', () => {
  it('令牌写入 localStorage 并作为 Bearer 头发出，凭据同源', async () => {
    const http = installFetch(() => stubResponse(200, { devices: [] }))
    setToken('tok-123')
    await api.devices()
    expect(getToken()).toBe('tok-123')
    expect(http.last()?.headers.Authorization).toBe('Bearer tok-123')
    expect(http.last()?.url).toBe('/api/devices')
  })

  it('受保护端点 401 → 全局置未登录（路由守卫据此跳 /login），并抛出 ApiError', async () => {
    installFetch(() => stubResponse(401, { error: '未授权' }))
    useAuth.setState({ status: 'in', user: null })
    await expect(api.devices()).rejects.toMatchObject({ status: 401, message: '未授权' })
    expect(useAuth.getState().status).toBe('out')
  })

  it('公开端点（/api/auth/me）401 是页面语义，不触发全局同步', async () => {
    installFetch(() => stubResponse(401, { error: '未登录' }))
    useAuth.setState({ status: 'loading', user: null })
    await expect(api.me()).rejects.toBeInstanceOf(ApiError)
    expect(useAuth.getState().status).toBe('loading')
  })

  it('429 保留 Retry-After 供限流提示', async () => {
    installFetch(() => stubResponse(429, { error: 'too many' }, { 'Retry-After': '7' }))
    const err = await api.stats().catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).retryAfter).toBe(7)
  })

  it('fetch 抛错（server 不可达）→ 可读的中文错误，不泄漏堆栈', async () => {
    installFetch(() => { throw new TypeError('Failed to fetch') })
    await expect(api.devices()).rejects.toThrow('无法连接服务（服务未启动或网络不可达）')
  })
})

describe('Wave2 Schema 面：端点缺席时返回 null（通用回落）', () => {
  it.each([404, 405, 501])('%i → descriptors/capabilities/deviceDescriptor 全部 null', async (status) => {
    installFetch(() => stubResponse(status, { error: 'nope' }))
    expect(await api.descriptors()).toBeNull()
    expect(await api.capabilities()).toBeNull()
    expect(await api.deviceDescriptor('edge-1', 'dev-9')).toBeNull()
  })

  it.each([502, 503, 504])('%i 是网关故障，必须抛出 ApiError（不得伪装端点缺席）', async (status) => {
    installFetch(() => stubResponse(status, { error: 'gateway down' }))
    await expect(api.descriptors()).rejects.toMatchObject({ status })
  })

  it('网络不可达是真实连接错误，必须抛出而不是返回 null', async () => {
    installFetch(() => { throw new TypeError('Failed to fetch') })
    await expect(api.descriptors()).rejects.toThrow('无法连接服务（服务未启动或网络不可达）')
  })

  it('Schema 面 500 是真故障，必须抛出（与「缺席」区分）', async () => {
    installFetch(() => stubResponse(500, { error: 'boom' }))
    await expect(api.descriptors()).rejects.toMatchObject({ status: 500 })
  })

  it('冻结路径与 URL 编码（不得改动）', async () => {
    const http = installFetch(() => stubResponse(200, {}))
    await api.descriptors()
    await api.capabilities()
    await api.deviceDescriptor('edge 1', 'dev/9')
    expect(http.calls.map((c) => c.url)).toEqual([
      '/api/descriptors',
      '/api/capabilities',
      '/api/devices/edge%201/dev%2F9/descriptor',
    ])
  })
})

describe('命令下发（冻结契约 POST /api/devices/{edge}/{dev}/commands）', () => {
  it('body 为 {cmd,args}，args 缺省为空串', async () => {
    const http = installFetch(() => stubResponse(200, { id: 1, status: 'sent' }))
    await api.sendCommand('edge-1', 'dev-9', 'relay_on', '{"ms":200}')
    expect(http.last()).toMatchObject({
      url: '/api/devices/edge-1/dev-9/commands', method: 'POST',
      body: { cmd: 'relay_on', args: '{"ms":200}' },
    })
    await api.sendCommand('edge-1', 'dev-9', 'identify')
    expect(http.last()?.body).toEqual({ cmd: 'identify', args: '' })
  })

  it('认证族端点路径固定（docs/api.md §2.2）', async () => {
    const http = installFetch(() => stubResponse(200, { user: {} }))
    await api.me(); await api.login('a', 'b'); await api.setup('a', 'b')
    installFetch(() => stubResponse(204))
    await api.logout()
    expect(http.calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      'GET /api/auth/me', 'POST /api/auth/login', 'POST /api/auth/setup',
    ])
  })
})

describe('wsUrl', () => {
  it('始终使用干净的 /ws；本机 legacy token 只走 REST Authorization，不进 URL', () => {
    setToken('tok/1')
    expect(wsUrl()).toBe(`ws://${location.host}/ws`)
    expect(wsUrl()).not.toContain('tok')
    setToken('')
    expect(wsUrl()).toBe(`ws://${location.host}/ws`)
  })

  it('https 页面 → wss（协议跟随，混合内容不会被浏览器拦）', () => {
    vi.stubGlobal('location', { protocol: 'https:', host: 'cp.example.com' })
    setToken('')
    expect(wsUrl()).toBe('wss://cp.example.com/ws')
    vi.unstubAllGlobals()
  })
})
