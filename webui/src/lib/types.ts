// 与 Go internal/api 契约一一对应 —— 改这里必须同步 types.go / docs/design.md

/** 设备的一个业务槽位（如三格提醒盒的一格） */
export interface Slot {
  index: number
  code: number      // 0=待确认 1=已确认 2=逾期
  label: string
}

/** 设备状态 raw（由适配器定义语义，核心与前端都不做设备特化假设） */
export interface DeviceRaw {
  state?: number          // 0=待机 1=提醒中 2=逾期
  state_label?: string
  hour?: number
  min?: number
  clock?: string          // "HH:MM"
  slots?: Slot[]
  drift_min?: number      // 设备钟 - 参考钟（分钟）
  dump_raw?: string
  [k: string]: unknown
}

export interface DeviceView {
  id: string              // "<edge_id>/<device_id>"
  edge_id: string
  adapter: string
  name?: string
  port?: string
  online: boolean
  state: DeviceRaw
  updated_at: number      // unix 秒
  last_seen: number
}

export interface EdgeView {
  edge_id: string
  online: boolean
  version: string
  devices: string[]
  connected_at: number
}

export interface EventView {
  id: number
  device_id: string
  ts: number
  type: string            // BOOT|REMIND|TAKEN|TAKEN-LATE|MISSED|SYNC-OK
  payload: string
}

export interface CommandView {
  id: number
  device_id: string
  cmd: string
  args: string
  status: string          // pending|sent|ok|failed|timeout
  created_at: number
  acked_at: number
  result: string
}

export interface HealthView {
  ok: boolean
  version: string
  uptime_s: number
  devices_online: number
  devices_total: number
  edges_online: number
}

/** 已注册的设备适配器与其命令白名单（命令面板的唯一事实源） */
export interface AdapterView {
  name: string
  commands: string[]
}

/** 存储与运行统计 */
export interface StatsView {
  devices: number
  events: number
  commands: number
  oldest_event: number
  schema_version: number
  retention_days: number
  auth_enabled: boolean
}

/** WS 消息信封（浏览器视角） */
export type WsType =
  | 'snapshot' | 'state' | 'event' | 'command' | 'command_ack'
  | 'edge_up' | 'edge_down' | 'ping' | 'pong'

export interface Envelope<T = unknown> {
  v: number
  type: WsType
  device?: string
  ts: number
  data?: T
}

export interface StateData {
  online: boolean
  raw: DeviceRaw
  updated_at: number
}

export interface EventData { type: string; label?: string }

export interface AckData {
  command_id: number
  status: string
  detail?: string
}

export interface SnapshotData {
  devices: DeviceView[]
  edges: EdgeView[]
}

export interface EdgeUpData {
  edge_id: string
  devices: string[]
  version: string
}

/** 登录用户（docs/api.md §2.1-2.2：GET /api/auth/me → {user}） */
export type Role = 'admin' | 'operator' | 'viewer'

export interface UserView {
  id: number
  username: string
  name: string
  role: Role
  tenant_id: number
  tenant_slug: string
}

export interface MeResponse {
  user: UserView
}
