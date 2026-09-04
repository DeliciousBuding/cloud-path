// 实时层：单例 WebSocket + zustand。快照水合、状态合并、事件环形缓冲、
// 数值观测历史（会话级，按属性名通用采样）、Descriptor 缓存、命令 ack、断线指数退避重连。
//
// 设备无关原则：这一层不认识任何具体字段名。数值型观测一律按属性名进 series；
// Descriptor（若后端随 WS 下发）按设备键缓存，供 hooks/useDescriptor 与 SchemaRenderer 消费。
import { create } from 'zustand'
import { wsUrl } from '@/lib/api'
import { normalizeDescriptor, readInlineDescriptor } from '@/lib/descriptor'
import { authReady, refreshAuth, useAuth } from './auth'
import type {
  AckData, DeviceDescriptor, DeviceRaw, DeviceView, EdgeUpData, EdgeView, Envelope,
  EventData, EventView, SnapshotData, StateData,
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
  if (!authReady(useAuth.getState().status)) return
  if (started) return
  started = true
  dial()
}

/** 断开实时通道并停止自动重连（登出/未登录时调用；重新登录后 connectLive 恢复） */
export function disconnectLive() {
  enabled = false
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
  ws.onopen = () => {
    opened = true
    retry = 0
    useLive.setState({ status: 'open', failures: 0 })
  }
  ws.onclose = () => {
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
    case 'snapshot': {
      const snap = env.data as SnapshotData | undefined
      if (!snap) return
      const devices: Record<string, DeviceView> = {}
      for (const d of snap.devices ?? []) devices[d.id] = d
      const edges: Record<string, EdgeView> = {}
      for (const e of snap.edges ?? []) edges[e.edge_id] = e
      // 快照可能一并携带 Descriptor（宽容：数组或映射都接受）
      const descriptors = { ...st.descriptors }
      const rawList = (snap as { descriptors?: unknown }).descriptors
      const list: unknown[] = Array.isArray(rawList)
        ? rawList
        : rawList ? Object.values(rawList as object) : []
      for (const item of list) {
        const dd = normalizeDescriptor(item)
        if (!dd) continue
        descriptors[dd.device_id] = dd
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
      const descriptors = inline ? { ...st.descriptors, [key]: inline } : st.descriptors
      useLive.setState({ devices: { ...st.devices, [key]: dev }, series, descriptors })
      break
    }
    case 'descriptor': {
      // Wave2：后端可直接推 Descriptor（spec/descriptor.schema.json）
      if (!env.device) return
      const dd = normalizeDescriptor(env.data)
      if (!dd) return
      useLive.setState({ descriptors: { ...st.descriptors, [env.device]: dd } })
      break
    }
    case 'event': {
      const data = env.data as EventData | undefined
      if (!data || !env.device) return
      const ev: EventView = {
        id: liveEventId--, device_id: env.device, ts: env.ts,
        type: data.type, payload: JSON.stringify({ label: data.label ?? '' }),
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