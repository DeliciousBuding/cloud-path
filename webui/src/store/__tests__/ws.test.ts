// 实时通道（store/ws.ts）契约守卫。
//
// 三件事必须成立，否则验收场景「登录一个账号 → 看到真实设备 → 下发命令」会静默失真：
//   ① 账号模式下浏览器给不了 WS 自定义 header，`/ws` 靠**会话 cookie** 鉴权，
//      所以没有本机令牌时 wsUrl() 必须是干净的 `/ws`（不拼 ?token=）；
//   ② 登出 → 再登录必须真的重新拨号（旧实现 started 永不复位，第二次登录收不到实时数据）；
//   ③ 握手连续失败要如实计数，并定期用 me 复核登录态；未知/畸形 WS 帧一律忽略，不得崩。
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { connectLive, disconnectLive, reconnectLive, useLive } from '@/store/ws'
import { useAuth } from '@/store/auth'
import { setToken } from '@/lib/api'
import { installFetch, stubResponse } from '@/test/http'
import { resetStores } from '@/test/render'

class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  readonly url: string
  readyState = FakeWebSocket.CONNECTING
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  closed = false

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  close(): void { this.closed = true; this.readyState = FakeWebSocket.CLOSED }

  /** 测试驱动：握手成功 */
  simulateOpen(): void { this.readyState = FakeWebSocket.OPEN; this.onopen?.() }
  /** 测试驱动：握手失败/连接断开（没有 open 过就算失败） */
  simulateClose(): void { this.readyState = FakeWebSocket.CLOSED; this.onclose?.() }
  /** 测试驱动：收到一帧 */
  simulateMessage(data: unknown): void {
    this.onmessage?.({ data: typeof data === 'string' ? data : JSON.stringify(data) })
  }
}

function lastSocket(): FakeWebSocket {
  const s = FakeWebSocket.instances[FakeWebSocket.instances.length - 1]
  if (!s) throw new Error('没有拨号：connectLive 未创建 WebSocket')
  return s
}

function meCalls(): number {
  return installFetchCount('/api/auth/me')
}

// installFetch 每次调用会替换全局 fetch；这里保留最近一次的 stub 以便计数
let lastStub: ReturnType<typeof installFetch> | null = null
function installFetchCount(fragment: string): number {
  return lastStub ? lastStub.to(fragment).length : 0
}

beforeEach(() => {
  resetStores()
  disconnectLive()
  FakeWebSocket.instances = []
  Object.defineProperty(globalThis, 'WebSocket', {
    writable: true, configurable: true, value: FakeWebSocket,
  })
  lastStub = installFetch((url) => (url === '/api/auth/me'
    ? stubResponse(200, { user: { id: 1, username: 'admin', name: 'A', role: 'admin', tenant_id: 1, tenant_slug: 'default' } })
    : stubResponse(404, {})))
  useAuth.setState({ status: 'in', user: null })
})

afterEach(() => { disconnectLive() })

describe('wsUrl：会话 cookie 模式', () => {
  it('没有本机令牌时是干净的 /ws（浏览器同源自动带会话 cookie）', () => {
    connectLive()
    expect(lastSocket().url).toMatch(/\/ws$/)
    expect(lastSocket().url).not.toContain('token=')
  })

  it('有本机令牌时才拼 ?token=（legacy 共享令牌走 query 是后端允许的）', () => {
    setToken('tok-abc')
    connectLive()
    expect(lastSocket().url).toContain('token=tok-abc')
  })
})

describe('拨号生命周期', () => {
  it('未登录（out）时不拨号，避免 401 重连风暴', () => {
    useAuth.setState({ status: 'out', user: null })
    connectLive()
    expect(FakeWebSocket.instances).toHaveLength(0)
    expect(useLive.getState().status).toBe('closed')
  })

  it('登出 → 再登录必须真的重新拨号（started 复位）', () => {
    connectLive()
    expect(FakeWebSocket.instances).toHaveLength(1)
    const first = lastSocket()
    first.simulateOpen()
    expect(useLive.getState().status).toBe('open')

    disconnectLive()
    expect(first.closed).toBe(true)
    expect(useLive.getState().status).toBe('closed')

    // 重新登录后 App 会再调 connectLive：这里必须再拨一次
    connectLive()
    expect(FakeWebSocket.instances).toHaveLength(2)
    expect(useLive.getState().status).toBe('connecting')
  })

  it('open 成功后 failures 归零，status=open', () => {
    connectLive()
    lastSocket().simulateClose()
    expect(useLive.getState().failures).toBe(1)
    connectLive()
    reconnectLive()
    lastSocket().simulateOpen()
    expect(useLive.getState().failures).toBe(0)
    expect(useLive.getState().status).toBe('open')
  })
})

describe('握手连续失败要说实话并复核登录态', () => {
  it('每次「没 open 就关闭」failures +1', () => {
    connectLive()
    for (let i = 1; i <= 3; i++) {
      lastSocket().simulateClose()
      expect(useLive.getState().failures).toBe(i)
      reconnectLive()
    }
  })

  it('连续失败满 5 次 → 调 me 复核登录态（会话失效时守卫才能收敛到 /login）', () => {
    connectLive()
    const before = meCalls()
    for (let i = 0; i < 5; i++) {
      lastSocket().simulateClose()
      if (i < 4) reconnectLive()
    }
    expect(useLive.getState().failures).toBe(5)
    expect(meCalls()).toBeGreaterThan(before)
  })

  it('不到 5 次不打 me（不做无谓的复核风暴）', () => {
    connectLive()
    const before = meCalls()
    for (let i = 0; i < 4; i++) {
      lastSocket().simulateClose()
      reconnectLive()
    }
    expect(meCalls()).toBe(before)
  })
})

describe('WS 消费必须宽容（不得让整个 UI 崩）', () => {
  function openSocket(): FakeWebSocket {
    connectLive()
    const s = lastSocket()
    s.simulateOpen()
    return s
  }

  it('未知消息类型直接忽略，连接与已有状态都不受影响', () => {
    const s = openSocket()
    s.simulateMessage({ v: 1, type: 'some_future_type', ts: 1, data: { anything: true } })
    expect(useLive.getState().status).toBe('open')
  })

  it('插件控制面消息（plugin_status / plugin_ack / plugin_desired）不会把浏览器端搞崩', () => {
    const s = openSocket()
    s.simulateMessage({
      v: 1, type: 'plugin_status', ts: 1,
      data: { boot_id: 'b1', sequence: 1, applied_revision: 3, installations: [], instances: [] },
    })
    s.simulateMessage({
      v: 1, type: 'plugin_ack', ts: 1,
      data: { revision: 3, snapshot_digest: 'd', status: 'applied', results: [] },
    })
    s.simulateMessage({ v: 1, type: 'plugin_desired', ts: 1, data: { revision: 4, snapshot_digest: 'x', instances: [] } })
    expect(useLive.getState().status).toBe('open')
  })

  it('畸形帧（坏 JSON / 缺字段 / 非对象）一律忽略', () => {
    const s = openSocket()
    s.simulateMessage('{ not json')
    s.simulateMessage(null)
    s.simulateMessage({ v: 1 })
    s.simulateMessage({ v: 1, type: 'state' })
    s.simulateMessage({ v: 1, type: 'event', device: 'e/d', ts: 5 })
    expect(useLive.getState().status).toBe('open')
  })

  it('正常快照与状态帧仍被采纳（宽容不等于什么都不收）', () => {
    const s = openSocket()
    s.simulateMessage({
      v: 1, type: 'snapshot', ts: 10,
      data: {
        devices: [{ id: 'e1/d1', edge_id: 'e1', adapter: 'demo', online: true, state: { a: 1 }, updated_at: 10, last_seen: 10 }],
        edges: [{ edge_id: 'e1', online: true, version: 'v1', devices: ['e1/d1'], connected_at: 9 }],
      },
    })
    expect(Object.keys(useLive.getState().devices)).toEqual(['e1/d1'])
    expect(useLive.getState().edges.e1?.online).toBe(true)

    s.simulateMessage({ v: 1, type: 'state', device: 'e1/d1', ts: 12, data: { online: false, raw: { a: 2 }, updated_at: 12 } })
    expect(useLive.getState().devices['e1/d1']?.online).toBe(false)
  })
})

function domainFrame(instanceID = 'app-a') {
  return { v: 1, type: 'domain_record', ts: 1_800_000_000, data: {
    instance_id: instanceID, record_type: 'sample', record_id: 'record-1', data_json: '{"count":1}',
    updated_at: 1_800_000_000, created: true,
  } }
}

describe('Application Plane 实时通知', () => {
  it('新增与同键覆盖都发失效通知，不在实时 store 再存一份领域记录', () => {
    connectLive()
    lastSocket().simulateOpen()
    lastSocket().simulateMessage(domainFrame())
    expect(useLive.getState().domainRecord).toEqual({ instanceID: 'app-a', sequence: 1 })
    const update = domainFrame()
    update.data.created = false
    update.data.data_json = '{"count":2}'
    lastSocket().simulateMessage(update)
    expect(useLive.getState().domainRecord).toEqual({ instanceID: 'app-a', sequence: 2 })
    expect(useLive.getState().events).toEqual([])
    expect(useLive.getState().devices).toEqual({})
  })

  it('快速交错实例通知都能被订阅者观察，不只剩最后一个实例', () => {
    connectLive()
    const seen: string[] = []
    const stop = useLive.subscribe((state, previous) => {
      if (state.domainRecord !== previous.domainRecord && state.domainRecord) seen.push(state.domainRecord.instanceID)
    })
    lastSocket().simulateMessage(domainFrame('app-a'))
    lastSocket().simulateMessage(domainFrame('app-b'))
    stop()
    expect(seen).toEqual(['app-a', 'app-b'])
  })

  it('畸形载荷与不支持的信封版本不触发补读', () => {
    connectLive()
    const original = domainFrame()
    for (const invalid of [
      { instance_id: '' }, { record_type: null }, { record_id: '' }, { data_json: {} },
      { created: undefined }, { created: 'false' }, { updated_at: 'bad' }, { version: 1 },
    ]) lastSocket().simulateMessage({ ...original, data: { ...original.data, ...invalid } })
    lastSocket().simulateMessage({ ...original, v: 2 })
    lastSocket().simulateMessage({ ...original, data: null })
    expect(useLive.getState().domainRecord).toBeNull()
  })

  it('连接代次只在当前连接成功时推进；旧连接的迟到通知/握手不能污染新会话', () => {
    connectLive()
    const first = lastSocket()
    first.simulateOpen()
    expect(useLive.getState().connectionEpoch).toBe(1)
    first.simulateMessage(domainFrame())
    disconnectLive()
    expect(useLive.getState().domainRecord).toBeNull()
    first.simulateOpen()
    first.simulateMessage(domainFrame())
    expect(useLive.getState().status).toBe('closed')
    expect(useLive.getState().domainRecord).toBeNull()
    expect(useLive.getState().connectionEpoch).toBe(1)
    connectLive()
    const second = lastSocket()
    second.simulateOpen()
    expect(useLive.getState().connectionEpoch).toBe(2)
    first.simulateMessage(domainFrame('old-account'))
    expect(useLive.getState().domainRecord).toBeNull()
    second.simulateMessage(domainFrame('new-account'))
    expect(useLive.getState().domainRecord?.instanceID).toBe('new-account')
  })
})
