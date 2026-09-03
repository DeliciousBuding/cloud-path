// REST 客户端契约测试：冻结路径、鉴权头、401 全局收敛、Schema 面缺席（404/405/501/网络断）→ null。
// 「端点不存在」不是错误而是常态（后端 A1 未就绪），UI 必须走通用回落而不是弹错误。
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

  it('公开端点（/api/auth/me）401 是页面语义，不触发全局收敛', async () => {
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
    await expect(api.devices()).rejects.toThrow('无法连接 server（服务未启动或网络不可达）')
  })
})

describe('Wave2 Schema 面：端点缺席时返回 null（通用回落）', () => {
  it.each([404, 405, 501, 502])('%i → descriptors/capabilities/deviceDescriptor 全部 null', async (status) => {
    installFetch(() => stubResponse(status, { error: 'nope' }))
    expect(await api.descriptors()).toBeNull()
    expect(await api.capabilities()).toBeNull()
    expect(await api.deviceDescriptor('edge-1', 'dev-9')).toBeNull()
  })

  it('网络不可达同样按缺席处理（返回 null，不抛错刷屏）', async () => {
    installFetch(() => { throw new TypeError('Failed to fetch') })
    expect(await api.descriptors()).toBeNull()
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
  it('按当前页面协议选择 ws/wss 并带上令牌', () => {
    setToken('tok/1')
    expect(wsUrl()).toBe(`ws://${location.host}/ws?token=${encodeURIComponent('tok/1')}`)
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