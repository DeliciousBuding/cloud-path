// 实时层：单例 WebSocket + zustand。快照水合、状态合并、事件环形缓冲、
// 数值观测历史（会话级，按属性名通用采样）、Descriptor 缓存、命令 ack、断线指数退避重连。
//
// 设备无关原则：这一层不认识任何具体字段名。数值型观测一律按属性名进 series；
// Descriptor（若后端随 WS 下发）按设备键缓存，供 hooks/useDescriptor 与 SchemaRenderer 消费。
import { create } from 'zustand'
import { wsUrl } from '@/lib/api'
import { normalizeDescriptor, readInlineDescriptor } from '@/lib/descriptor'
import { authIdentity, authReady, refreshAuth, useAuth } from './auth'
import type {
  AckData, DeviceDescriptor, DeviceRaw, DeviceView, EdgeUpData, EdgeView, Envelope,
  EventData, EventView, Observation, SnapshotData, StateData,
} from '@/lib/types'

export type WsStatus = 'connecting' | 'open' | 'closed'

/** 会话内数值采样点（t=unix 秒，v=观测值） */
export interface SeriesPoint { t: number; v: number }

/** 每设备最多跟踪的数值属性数（防 raw 字段爆炸；STC-B 全板 12 个数值观测需全覆盖） */
const MAX_SERIES_KEYS = 12
/** 每条序列最多保留点数 */
const MAX_SERIES_POINTS = 240

/** 连续握手失败多少次后重新校验一次登录态（账号模式下 /ws 需要会话 cookie） */
const REAUTH_EVERY_FAILURES = 5

interface LiveState {
  status: WsStatus
  /** 连续「未成功 open 就关闭」的次数；open 成功即归零。UI 用它说实话，不假装实时数据正常 */
  failures: number
  /** 单一通知槽；REST 仍是分页/排序权威，不积累第二份记录库。 */
  domainRecord: { instanceID: string; sequence: number } | null
  connectionEpoch: number
  devices: Record<string, DeviceView>
  edges: Record<string, EdgeView>
  /** WS 实时事件（新→旧，本地负 id，与 REST 历史正 id 不冲突） */
  events: EventView[]
  /** 会话内数值观测历史：deviceKey → 属性名 → 采样点 */
  series: Record<string, Record<string, SeriesPoint[]>>
  /** Descriptor 缓存（WS 下发优先于 REST 探测）：deviceKey → DeviceDescriptor */
  descriptors: Record<string, DeviceDescriptor>
  /** command_id → 最新 ack */
  acks: Record<number, AckData>
}

export const useLive = create<LiveState>(() => ({
  status: 'closed',
  failures: 0,
  domainRecord: null,
  connectionEpoch: 0,
  devices: {},
  edges: {},
  events: [],
  series: {},
  descriptors: {},
  acks: {},
}))

/** 通用数值采样：raw 里每个有限 number 都进对应属性序列，不认识任何字段名 */
function pushSeries(
  prev: Record<string, Record<string, SeriesPoint[]>>,
  key: string,
  raw: DeviceRaw | undefined,
): Record<string, Record<string, SeriesPoint[]>> {
  if (!raw) return prev
  const t = Date.now() / 1000
  let next: Record<string, SeriesPoint[]> | null = null
  for (const [prop, value] of Object.entries(raw)) {
    if (typeof value !== 'number' || !Number.isFinite(value)) continue
    if (!next) next = { ...(prev[key] ?? {}) }
    if (!(prop in next) && Object.keys(next).length >= MAX_SERIES_KEYS) continue
    next[prop] = [...(next[prop] ?? []), { t, v: value }].slice(-MAX_SERIES_POINTS)
  }
  return next ? { ...prev, [key]: next } : prev
}

/** Typed samples update only the exact device and already-declared entity/capability. Raw is never inferred. */
function mergeObservations(descriptor: DeviceDescriptor, deviceKey: string, sets: unknown, online: boolean): DeviceDescriptor {
  if (descriptor.device_id !== deviceKey || !Array.isArray(sets)) return descriptor
  let changed = false
  const entities = descriptor.entities.map((entity) => {
    let observations = entity.observations
    const seen = new Set<string>()
    for (const set of sets) {
      if (!set || typeof set !== 'object' || set.entity_id !== entity.entity_id
        || !set.observations || typeof set.observations !== 'object' || Array.isArray(set.observations)) continue
      for (const [property, value] of Object.entries(set.observations)) {
        if (!value || typeof value !== 'object' || Array.isArray(value)) continue
        const sample = value as Record<string, unknown>
        if (!property || sample.property !== property || typeof sample.capability !== 'string'
          || !entity.capabilities.includes(sample.capability) || !Object.hasOwn(sample, 'value') || sample.value == null
          || (typeof sample.value === 'number' && !Number.isFinite(sample.value))
          || (sample.entity_id !== undefined && sample.entity_id !== entity.entity_id)
          || (sample.unit !== undefined && typeof sample.unit !== 'string')
          || (sample.quality !== undefined && !['good', 'uncertain', 'bad', 'unavailable'].includes(String(sample.quality)))
          || [sample.observed_at, sample.received_at].some((time) => time !== undefined && (typeof time !== 'string' || !Number.isFinite(Date.parse(time))))
          || (sample.sequence !== undefined && (typeof sample.sequence !== 'number' || !Number.isInteger(sample.sequence)))) continue
        const identity = sample.capability + '\0' + property
        if (seen.has(identity)) continue
        seen.add(identity)
        // Replace the sample, even when value is unchanged. Missing metadata must not inherit old freshness.
        const observation = { ...sample, ...(online ? {} : { quality: 'unavailable' }) } as unknown as Observation
        observations = { ...observations, [property]: observation }
        changed = true
      }
    }
    return observations === entity.observations ? entity : { ...entity, observations }
  })
  return changed ? { ...descriptor, entities } : descriptor
}

let ws: WebSocket | null = null
let retry = 0
let liveEventId = -1
let started = false
/** 仅在已登录/开放访问时允许拨号：账号模式下 /ws 需要凭据（docs/api.md §1 不变量2） */
let enabled = false

/** 建立（幂等）WS 连接；断开自动指数退避重连 1s→15s。
 * 仅在已登录/开放访问时真正拨号；未登录期间保持关闭（见 disconnectLive）。 */
export function connectLive() {
  enabled = true
  watchNetwork()
  watchIdentity()
  if (!authReady(useAuth.getState().status)) return
  if (started) return
  started = true
  dial()
}

/** 断开实时通道并停止自动重连（登出/未登录时调用；重新登录后 connectLive 恢复） */
export function disconnectLive() {
  enabled = false
  useLive.setState({ domainRecord: null })
  retry = 0
  // 必须复位 started：否则「登出 → 再登录」时 connectLive() 会因为 started 仍为 true 直接返回，
  // 实时通道再也拨不出去（页面看着正常却收不到实时数据 = 假数据）。
  started = false
  if (!ws) {
    if (useLive.getState().status !== 'closed') useLive.setState({ status: 'closed' })
    return
  }
  const old = ws
  ws = null
  old.onclose = null
  old.close()
  useLive.setState({ status: 'closed', failures: 0 })
}

let identityWatched = false
/** Same-status account/tenant switches must not keep the previous session's socket or observations. */
function watchIdentity() {
  if (identityWatched) return
  identityWatched = true
  useAuth.subscribe((next, previous) => {
    if (authIdentity(next) === authIdentity(previous)) return
    const reconnect = enabled
    disconnectLive()
    useLive.setState({ devices: {}, edges: {}, descriptors: {}, series: {}, events: [], acks: {} })
    if (reconnect && authReady(next.status)) connectLive()
  })
}

let netWatched = false
/** 浏览器网络事件联动：已建立的 WS 无 ping/pong，断网短时感知不到，
 * 不收敛会让页面停在「看着正常实则已断」的假实时态；网络恢复则跳过退避立即重连。 */
function watchNetwork() {
  if (netWatched || typeof window === 'undefined') return
  netWatched = true
  window.addEventListener('offline', () => {
    if (ws) {
      const old = ws
      ws = null
      old.onclose = null
      try { old.close() } catch { /* 已死 */ }
    }
    useLive.setState({ status: 'closed' })
  })
  window.addEventListener('online', () => {
    retry = 0
    dial()
  })
}

function dial() {
  if (!enabled) return
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return
  useLive.setState({ status: 'connecting' })
  try {
    ws = new WebSocket(wsUrl())
  } catch {
    scheduleRetry()
    return
  }
  // 本次握手有没有真的 open 过：只有「没 open 就关闭」才算失败（正常关闭不计入）
  let opened = false
  const socket = ws
  ws.onopen = () => {
    if (ws !== socket || !enabled || !authReady(useAuth.getState().status)) return
    opened = true
    retry = 0
    useLive.setState({ status: 'open', failures: 0, connectionEpoch: useLive.getState().connectionEpoch + 1 })
  }
  ws.onclose = () => {
    if (ws !== socket) return
    ws = null
    const failures = opened ? 0 : useLive.getState().failures + 1
    useLive.setState({ status: 'closed', failures })
    // 连续握手失败在账号模式下多半是会话已失效（/ws 靠会话 cookie 鉴权，浏览器无法给它加 header）。
    // 定期用 me 复核：会话真失效就把登录态收敛成 out，由路由守卫送回 /login，
    // 而不是让页面停在「已登录但没有实时数据」的假正常状态。
    if (!opened && failures % REAUTH_EVERY_FAILURES === 0) void refreshAuth()
    scheduleRetry()
  }
  ws.onerror = () => { /* onclose 会跟上，统一在那里重连 */ }
  ws.onmessage = (ev) => {
    if (ws !== socket || !enabled || !authReady(useAuth.getState().status)) return
    try {
      handle(JSON.parse(ev.data as string) as Envelope)
    } catch { /* 坏帧忽略 */ }
  }
}

function scheduleRetry() {
  if (!enabled) return // 登出/未登录后不再敲门（避免 401 重连风暴）
  const delay = Math.min(1000 * 2 ** retry, 15000) + Math.random() * 500
  retry++
  setTimeout(dial, delay)
}

/** token 变更后强制重连（Settings 页调用）；未启用（未登录）时只复位不拨号 */
export function reconnectLive() {
  retry = 0
  if (!enabled) return
  if (ws) {
    const old = ws
    ws = null
    old.onclose = null
    old.close()
  }
  dial()
}

function handle(env: Envelope) {
  const st = useLive.getState()
  switch (env.type) {
    case 'domain_record': {
      const data = env.data as Record<string, unknown> | undefined
      if (env.v !== 1 || !data || typeof data.instance_id !== 'string' || !data.instance_id ||
          typeof data.record_type !== 'string' || !data.record_type ||
          typeof data.record_id !== 'string' || !data.record_id || typeof data.created !== 'boolean' ||
          (data.version !== undefined && typeof data.version !== 'string') ||
          typeof data.data_json !== 'string' || typeof data.updated_at !== 'number' ||
          !Number.isFinite(data.updated_at)) return
      useLive.setState({ domainRecord: {
        instanceID: data.instance_id, sequence: (st.domainRecord?.sequence ?? 0) + 1,
      } })
      break
    }
    case 'snapshot': {
      const snap = env.data as SnapshotData | undefined
      if (!snap) return
      const devices: Record<string, DeviceView> = {}
      for (const d of snap.devices ?? []) devices[d.id] = d
      const edges: Record<string, EdgeView> = {}
      for (const e of snap.edges ?? []) edges[e.edge_id] = e
      // 快照是设备集合的权威：Descriptor 缓存也按本次快照重建，不能留下已删设备的旧操作。
      const descriptors: Record<string, DeviceDescriptor> = {}
      const rawList = (snap as { descriptors?: unknown }).descriptors
      const entries: Array<[string | undefined, unknown]> = Array.isArray(rawList)
        ? rawList.map((item) => [undefined, item])
        : rawList && typeof rawList === 'object'
          ? Object.entries(rawList as Record<string, unknown>)
          : []
      for (const [mappedKey, item] of entries) {
        const dd = normalizeDescriptor(item)
        if (!dd) continue
        let key = mappedKey && devices[mappedKey] ? mappedKey
          : devices[dd.device_id] ? dd.device_id : undefined
        if (!key) {
          // 兼容旧短 ID Descriptor：只有唯一命中设备时才归属，绝不复活不在快照里的键。
          const matches = Object.keys(devices).filter((candidate) => candidate.split('/').at(-1) === dd.device_id)
          if (matches.length === 1) key = matches[0]
        }
        if (key) descriptors[key] = dd
      }
      useLive.setState({ devices, edges, descriptors })
      break
    }
    case 'state': {
      const data = env.data as StateData | undefined
      if (!data || !env.device) return
      const key = env.device
      const prev = st.devices[key]
      const dev: DeviceView = {
        ...(prev ?? {
          id: key, edge_id: key.split('/')[0] ?? '', adapter: '',
          name: '', port: '', last_seen: 0,
        }),
        online: data.online,
        state: data.raw ?? {},
        updated_at: data.updated_at || env.ts,
        last_seen: env.ts,
      }
      const series = pushSeries(st.series, key, data.online ? data.raw : undefined)
      // 过渡形态：Descriptor 内联在 state 载荷里也接受
      const inline = readInlineDescriptor(data)
      // Keep legacy short-id descriptors readable; typed increments below still require the full device key.
      const inlineMatches = inline && (inline.device_id === key || inline.device_id === key.split('/').at(-1))
      const descriptor = inlineMatches ? inline : st.descriptors[key]
      const updated = descriptor && env.v === 1 ? mergeObservations(descriptor, key, data.observations, data.online) : descriptor
      const descriptors = updated && updated !== st.descriptors[key] ? { ...st.descriptors, [key]: updated } : st.descriptors
      useLive.setState({ devices: { ...st.devices, [key]: dev }, series, descriptors })
      break
    }
    case 'descriptor': {
      // Wave2：后端可直接推 Descriptor（spec/descriptor.schema.json）
      if (!env.device) return
      const dd = normalizeDescriptor(env.data)
      if (!dd || (dd.device_id !== env.device && dd.device_id !== env.device.split('/').at(-1))) return
      useLive.setState({ descriptors: { ...st.descriptors, [env.device]: dd } })
      break
    }
    case 'event': {
      const data = env.data as EventData | undefined
      if (!data || !env.device) return
      const ev: EventView = {
        id: liveEventId--, device_id: env.device, ts: env.ts,
        // 载荷必须与后端落库的形状一致：server 对同一条事件直接 json.Marshal(EventData)
        // 后既入库又广播。此前这里重建为 {label: data.label ?? ''}，而后端从不发送 label，
        // 于是实时事件载荷恒为 {"label":""}（详情面板显示空壳 JSON），而历史里的同一条
        // 事件是 {"type":…,"entity_id":…}——同一事件两种来源不同形，且 entity_id 被丢弃。
        type: data.type, payload: JSON.stringify(data),
      }
      useLive.setState({ events: [ev, ...st.events].slice(0, 300) })
      break
    }
    case 'command_ack': {
      const data = env.data as AckData | undefined
      if (!data) return
      useLive.setState({ acks: { ...st.acks, [data.command_id]: data } })
      break
    }
    case 'edge_up': {
      const data = env.data as EdgeUpData | undefined
      if (!data) return
      useLive.setState({
        edges: {
          ...st.edges,
          [data.edge_id]: {
            edge_id: data.edge_id, online: true, version: data.version,
            devices: data.devices ?? [], connected_at: env.ts,
          },
        },
      })
      break
    }
    case 'edge_down': {
      const id = env.device
      if (!id || !st.edges[id]) return
      useLive.setState({ edges: { ...st.edges, [id]: { ...st.edges[id], online: false } } })
      break
    }
    default:
      break
  }
}
