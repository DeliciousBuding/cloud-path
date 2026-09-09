// 实时通道（store/ws.ts）契约守卫。
//
// 三件事必须成立，否则验收场景「登录一个账号 → 看到真实设备 → 下发命令」会静默失真：
//   ① 账号模式下浏览器给不了 WS 自定义 header，`/ws` 靠**会话 cookie** 鉴权，
//      所以 wsUrl() 必须始终是干净的 `/ws`（本机 token 也绝不拼进 query）；
//   ② 登出 → 再登录必须真的重新拨号（旧实现 started 永不复位，第二次登录收不到实时数据）；
//   ③ 握手连续失败要如实计数，并定期用 me 复核登录态；未知/畸形 WS 帧一律忽略，不得崩。
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { connectLive, disconnectLive, reconnectLive, useLive } from '@/store/ws'
import { useAuth } from '@/store/auth'
import { setToken } from '@/lib/api'
import { payloadLabel } from '@/lib/format'
import type { DeviceDescriptor, EventView, Observation } from '@/lib/types'
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

  it('有本机 legacy token 时仍不拼 query，token 只留在 REST Authorization', () => {
    setToken('tok-abc')
    connectLive()
    expect(lastSocket().url).toMatch(/\/ws$/)
    expect(lastSocket().url).not.toContain('token=')
    expect(lastSocket().url).not.toContain('tok-abc')
    expect(document.body.textContent).not.toContain('tok-abc')
    setToken('')
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

  it('连续失败满 5 次 → 调 me 复核登录态（会话失效时守卫才能同步到 /login）', () => {
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

  it('正常配置与状态帧仍被采纳（宽容不等于什么都不收）', () => {
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

  it('快照重建 descriptor 缓存，删除不在 devices 里的旧键与快照外 Descriptor', () => {
    const s = openSocket()
    useLive.setState({ descriptors: {
      'e1/old': sampleDescriptor('e1/old'),
      'e1/d1': sampleDescriptor('e1/d1'),
    } })
    s.simulateMessage({
      v: 1, type: 'snapshot', ts: 11,
      data: {
        devices: [{ id: 'e1/d1', edge_id: 'e1', adapter: 'demo', online: true, state: {}, updated_at: 11, last_seen: 11 }],
        edges: [{ edge_id: 'e1', online: true, version: 'v1', devices: ['e1/d1'], connected_at: 11 }],
        descriptors: [sampleDescriptor('e1/d1'), sampleDescriptor('e1/ghost')],
      },
    })
    expect(Object.keys(useLive.getState().descriptors)).toEqual(['e1/d1'])
    expect(useLive.getState().descriptors['e1/d1'].device_id).toBe('e1/d1')
  })
})

describe('event 载荷契约：实时与落库同形（design.md 硬约束 4）', () => {
  // server 对同一条 event 只做一次 json.Marshal(EventData)：既落库又广播。
  // 浏览器端不得重建载荷形状，否则实时列表与历史里的同一条事件会有两种样子。
  function openSocket(): FakeWebSocket {
    connectLive()
    const s = lastSocket()
    s.simulateOpen()
    return s
  }

  /** 最新一条实时事件（store 里新事件在前） */
  function newestEvent(): EventView {
    const ev = useLive.getState().events[0]
    if (!ev) throw new Error('实时事件没进 store')
    return ev
  }

  it('实体级事件原样透传：与 server 落库、REST 历史返回的形状一致', () => {
    const s = openSocket()
    s.simulateMessage({ v: 1, type: 'event', device: 'e1/d1', ts: 100, data: { type: 'press', entity_id: 'key1' } })
    const ev = newestEvent()
    expect({ type: ev.type, device_id: ev.device_id, ts: ev.ts }).toEqual({ type: 'press', device_id: 'e1/d1', ts: 100 })
    expect(JSON.parse(ev.payload)).toEqual({ type: 'press', entity_id: 'key1' })
    // 旧实现把载荷重建成 {"label":""}：详情面板只剩空壳，绑定路由依据 entity_id 被丢弃
    expect(ev.payload).not.toContain('label')
  })

  it('设备级事件不凭空造键：后端 omitempty 的 entity_id/label 不该出现在实时载荷里', () => {
    const s = openSocket()
    s.simulateMessage({ v: 1, type: 'event', device: 'e1/d1', ts: 101, data: { type: 'BOOT' } })
    const payload = JSON.parse(newestEvent().payload) as Record<string, unknown>
    expect(payload).toEqual({ type: 'BOOT' })
    expect('entity_id' in payload).toBe(false)
    expect('label' in payload).toBe(false)
  })

  it('后端给了 label 就照原样保留（展示层 payloadLabel 依赖它，前端不得吞掉）', () => {
    const s = openSocket()
    s.simulateMessage({
      v: 1, type: 'event', device: 'e1/d1', ts: 102,
      data: { type: 'REMIND', entity_id: 'slot1', label: '第 1 槽提醒' },
    })
    expect(JSON.parse(newestEvent().payload)).toEqual({ type: 'REMIND', entity_id: 'slot1', label: '第 1 槽提醒' })
    expect(payloadLabel(newestEvent().payload)).toBe('第 1 槽提醒')
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


const sampleCapability = 'example.dev/capability/counter@1'
function sample(overrides: Record<string, unknown> = {}) {
  return { capability: sampleCapability, property: 'value', value: 7,
    quality: 'good', observed_at: '2026-09-08T00:00:00Z', received_at: '2026-09-08T00:00:01Z', sequence: 1, ...overrides }
}
function sampleDescriptor(deviceID: string): DeviceDescriptor {
  return { device_id: deviceID, external_id: deviceID, status: 'online', entities: [
    { entity_id: 'shared', unique_key: 'shared', category: 'sensor', capabilities: [sampleCapability], observations: { value: sample() as Observation } },
    { entity_id: 'sibling', unique_key: 'sibling', category: 'sensor', capabilities: [sampleCapability], observations: { value: sample({ value: 9 }) as Observation } },
  ] }
}
function observationSocket() {
  connectLive()
  const socket = lastSocket()
  socket.simulateOpen()
  for (const device of ['e1/d1', 'e1/d2']) socket.simulateMessage({ v: 1, type: 'descriptor', device, data: sampleDescriptor(device) })
  return socket
}
function stateFrame(observations: unknown, device = 'e1/d1', online = true) {
  return { v: 1, type: 'state', device, ts: 1_800_000_001, data: { online, updated_at: 1_800_000_001,
    raw: { counter: 7, diagnostic: 'raw preserved' }, observations } }
}

describe('typed StateData observations refresh with strict descriptor scope', () => {
  it('相同值也替换时间/quality/sequence，只改变本设备目标实体并保留 Raw', () => {
    const socket = observationSocket()
    const before = useLive.getState().descriptors['e1/d1']
    const other = useLive.getState().descriptors['e1/d2']
    const update = sample({ quality: 'uncertain', observed_at: '2026-09-08T00:01:00Z', received_at: '2026-09-08T00:01:01Z', sequence: 2 })
    socket.simulateMessage(stateFrame([{ entity_id: 'shared', observations: { value: update } }]))
    const state = useLive.getState()
    expect(state.descriptors['e1/d1'].entities[0].observations?.value).toEqual(update)
    expect(state.descriptors['e1/d1']).not.toBe(before)
    expect(before.entities[0].observations?.value.sequence).toBe(1)
    expect(state.descriptors['e1/d1'].entities[1]).toBe(before.entities[1])
    expect(state.descriptors['e1/d2']).toBe(other)
    expect(state.devices['e1/d1'].state).toEqual({ counter: 7, diagnostic: 'raw preserved' })
    expect(state.series['e1/d1'].counter[0].v).toBe(7)
  })

  it('不创建未声明实体，不串到其他设备同名实体，也不接受跨 capability/property 的样本', () => {
    const socket = observationSocket()
    const before = useLive.getState().descriptors
    socket.simulateMessage(stateFrame([
      { entity_id: 'foreign-entity', observations: { value: sample() } },
      { entity_id: 'shared', observations: { value: sample({ capability: 'example.dev/capability/other@1' }) } },
      { entity_id: 'shared', observations: { wrong: sample() } },
      { entity_id: 'shared', observations: { value: sample({ entity_id: 'sibling' }) } },
    ]))
    expect(useLive.getState().descriptors).toBe(before)
    socket.simulateMessage(stateFrame([{ entity_id: 'shared', observations: { value: sample() } }], 'e2/d1'))
    expect(useLive.getState().descriptors['e2/d1']).toBeUndefined()
    expect(useLive.getState().descriptors).toBe(before)
  })

  it('descriptor frame 与 state 内联 descriptor 的 device_id 必须严格匹配消息设备', () => {
    const socket = observationSocket()
    const before = useLive.getState().descriptors
    socket.simulateMessage({ v: 1, type: 'descriptor', device: 'e1/d1', data: sampleDescriptor('e1/d2') })
    expect(useLive.getState().descriptors).toBe(before)
    const frame = stateFrame([{ entity_id: 'foreign', observations: { value: sample() } }])
    socket.simulateMessage({ ...frame, data: { ...frame.data, descriptor: sampleDescriptor('e1/d2') } })
    expect(useLive.getState().descriptors).toBe(before)
  })

  it('旧短 ID descriptor 仍可展示，但新 typed 增量不会靠短 ID 推断归属', () => {
    connectLive()
    const socket = lastSocket()
    socket.simulateMessage({ v: 1, type: 'descriptor', device: 'e1/d1', data: sampleDescriptor('d1') })
    const before = useLive.getState().descriptors['e1/d1']
    expect(before.device_id).toBe('d1')
    socket.simulateMessage(stateFrame([{ entity_id: 'shared', observations: { value: sample({ sequence: 9 }) } }]))
    expect(useLive.getState().descriptors['e1/d1']).toBe(before)
    expect(useLive.getState().devices['e1/d1'].state.diagnostic).toBe('raw preserved')
  })

  it('同帧合法内联 descriptor 可先声明再接收观测，不靠 raw 猜实体', () => {
    connectLive()
    const frame = stateFrame([{ entity_id: 'shared', observations: { value: sample({ sequence: 3 }) } }])
    lastSocket().simulateMessage({ ...frame, data: { ...frame.data, descriptor: sampleDescriptor('e1/d1') } })
    expect(useLive.getState().descriptors['e1/d1'].entities[0].observations?.value.sequence).toBe(3)
  })

  it('畸形集合/样本不使 UI 崩溃或覆盖已知观测，其他合法实体仍可更新', () => {
    const socket = observationSocket()
    const before = useLive.getState().descriptors
    for (const observations of [null, {}, 'bad', [null], [{ entity_id: 'shared', observations: [] }]]) {
      socket.simulateMessage(stateFrame(observations))
    }
    for (const invalid of [null, 8, [], sample({ value: null }), sample({ quality: 'invented' }),
      sample({ observed_at: 'not-a-time' }), sample({ received_at: 7 }), sample({ unit: {} }), sample({ sequence: '4' })]) {
      socket.simulateMessage(stateFrame([{ entity_id: 'shared', observations: { value: invalid } }]))
    }
    expect(useLive.getState().descriptors).toBe(before)
    socket.simulateMessage(stateFrame([
      { entity_id: 'shared', observations: { value: null } },
      { entity_id: 'sibling', observations: { value: sample({ sequence: 8 }) } },
    ]))
    expect(useLive.getState().descriptors['e1/d1'].entities[0]).toBe(before['e1/d1'].entities[0])
    expect(useLive.getState().descriptors['e1/d1'].entities[1].observations?.value.sequence).toBe(8)
  })

  it('离线样本不能标成 good；重新联机后的同值新观测恢复明确 quality', () => {
    const socket = observationSocket()
    const sets = [{ entity_id: 'shared', observations: { value: sample({ sequence: 2 }) } }]
    socket.simulateMessage(stateFrame(sets, 'e1/d1', false))
    expect(useLive.getState().descriptors['e1/d1'].entities[0].observations?.value.quality).toBe('unavailable')
    socket.simulateMessage(stateFrame(sets, 'e1/d1', true))
    expect(useLive.getState().descriptors['e1/d1'].entities[0].observations?.value.quality).toBe('good')
  })

  it('旧包/空增量不刷新观测时间；新样本缺失 metadata 不继承旧的时间与 quality', () => {
    const socket = observationSocket()
    const before = useLive.getState().descriptors
    socket.simulateMessage(stateFrame(undefined))
    socket.simulateMessage(stateFrame([]))
    socket.simulateMessage({ ...stateFrame([{ entity_id: 'shared', observations: { value: sample({ sequence: 9 }) } }]), v: 2 })
    expect(useLive.getState().descriptors).toBe(before)
    const partial = { capability: sampleCapability, property: 'value', value: 7 }
    socket.simulateMessage(stateFrame([{ entity_id: 'shared', observations: { value: partial } }]))
    expect(useLive.getState().descriptors['e1/d1'].entities[0].observations?.value).toEqual(partial)
    expect(useLive.getState().devices['e1/d1'].state.diagnostic).toBe('raw preserved')
  })

  it('重复实体/属性只取本帧第一个合法样本，与服务端去重一致', () => {
    const socket = observationSocket()
    socket.simulateMessage(stateFrame([
      { entity_id: 'shared', observations: { value: sample({ sequence: 2 }) } },
      { entity_id: 'shared', observations: { value: sample({ sequence: 3 }) } },
    ]))
    expect(useLive.getState().descriptors['e1/d1'].entities[0].observations?.value.sequence).toBe(2)
  })
})


describe('typed observation session isolation', () => {
  it('同为 in 的账号/租户切换也清理实时缓存并重新拨号，旧 socket 不能恢复旧观测', () => {
    const user = { id: 1, username: 'a', name: 'A', role: 'operator' as const, tenant_id: 1, tenant_slug: 'one' }
    useAuth.setState({ status: 'in', user })
    const old = observationSocket()
    old.simulateMessage(stateFrame([{ entity_id: 'shared', observations: { value: sample() } }]))
    expect(useLive.getState().descriptors['e1/d1']).toBeDefined()
    useAuth.setState({ status: 'in', user: { ...user, id: 2, tenant_id: 2, username: 'b', tenant_slug: 'two' } })
    expect(useLive.getState().descriptors).toEqual({})
    expect(useLive.getState().devices).toEqual({})
    expect(old.closed).toBe(true)
    expect(lastSocket()).not.toBe(old)
    old.simulateMessage(stateFrame([{ entity_id: 'shared', observations: { value: sample({ sequence: 99 }) } }]))
    expect(useLive.getState().descriptors).toEqual({})
    const current = lastSocket()
    current.simulateOpen()
    current.simulateMessage({ v: 1, type: 'descriptor', device: 'e1/d1', data: sampleDescriptor('e1/d1') })
    current.simulateMessage(stateFrame([{ entity_id: 'shared', observations: { value: sample({ sequence: 2 }) } }]))
    expect(useLive.getState().descriptors['e1/d1'].entities[0].observations?.value.sequence).toBe(2)
  })
})
