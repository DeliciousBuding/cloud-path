// 实时层：单例 WebSocket + zustand。快照水合、状态合并、事件环形缓冲、
// 漂移历史（会话级）、命令 ack、断线指数退避重连。
import { create } from 'zustand'
import { wsUrl } from '@/lib/api'
import type {
  AckData, DeviceView, EdgeUpData, EdgeView, Envelope, EventData,
  EventView, SnapshotData, StateData,
} from '@/lib/types'

export type WsStatus = 'connecting' | 'open' | 'closed'

export interface DriftPoint { t: number; v: number }

interface LiveState {
  status: WsStatus
  devices: Record<string, DeviceView>
  edges: Record<string, EdgeView>
  /** WS 实时事件（新→旧，本地负 id，与 REST 历史正 id 不冲突） */
  events: EventView[]
  /** 会话内漂移历史（页面打开起），每设备最多 240 点 */
  drift: Record<string, DriftPoint[]>
  /** command_id → 最新 ack */
  acks: Record<number, AckData>
}

export const useLive = create<LiveState>(() => ({
  status: 'closed',
  devices: {},
  edges: {},
  events: [],
  drift: {},
  acks: {},
}))

let ws: WebSocket | null = null
let retry = 0
let liveEventId = -1
let started = false

/** 建立（幂等）WS 连接；断开自动指数退避重连 1s→15s。 */
export function connectLive() {
  if (started) return
  started = true
  dial()
}

function dial() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return
  useLive.setState({ status: 'connecting' })
  try {
    ws = new WebSocket(wsUrl())
  } catch {
    scheduleRetry()
    return
  }
  ws.onopen = () => {
    retry = 0
    useLive.setState({ status: 'open' })
  }
  ws.onclose = () => {
    ws = null
    useLive.setState({ status: 'closed' })
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
  const delay = Math.min(1000 * 2 ** retry, 15000) + Math.random() * 500
  retry++
  setTimeout(dial, delay)
}

/** token 变更后强制重连（Settings 页调用） */
export function reconnectLive() {
  retry = 0
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
      useLive.setState({ devices, edges })
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
      const drift = { ...st.drift }
      const dm = data.raw?.drift_min
      if (data.online && typeof dm === 'number') {
        const arr = [...(drift[key] ?? []), { t: Date.now() / 1000, v: dm }]
        drift[key] = arr.slice(-240)
      }
      useLive.setState({ devices: { ...st.devices, [key]: dev }, drift })
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