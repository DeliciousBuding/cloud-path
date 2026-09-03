// 与 Go internal/api 契约一一对应 —— 改 DTO 必须同步 types.go / docs/design.md
//
// Wave2 起本文件分两段：
//   1) 传输层 DTO（DeviceView/EventView/... ↔ internal/api/types.go）
//   2) Schema 模型（Descriptor / Capability ↔ 冻结契约 spec/*.schema.json，只读不改）
// 设备语义（时钟、分格、提醒…）不属于前端：一律由 Descriptor + Capability 声明驱动渲染。

/** 设备状态 raw：兼容与诊断面（docs/architecture/capability-model.md §8）。
 *  自由 JSON，前端不做任何设备特化假设，按通用值渲染（标量→键值行，数组→表格/胶囊）。 */
export type DeviceRaw = Record<string, unknown>

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
  /** 事件类型属于 Capability/Application 命名空间，前端不维护平台级枚举 */
  type: string
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

/** 已注册的设备适配器与其命令白名单（Descriptor 缺席时的命令集回落事实源） */
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

/** WS 消息信封（浏览器视角）。后端可能新增类型（如 descriptor），故消费侧对未知 type 宽容。 */
export type WsType =
  | 'snapshot' | 'state' | 'event' | 'command' | 'command_ack'
  | 'edge_up' | 'edge_down' | 'ping' | 'pong'

export interface Envelope<T = unknown> {
  v: number
  type: WsType | string
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

/* ------------------------------------------------------------------ *
 * Schema 模型 —— 冻结契约，前端只消费
 *   spec/descriptor.schema.json  ($id cloudpath.dev/descriptor/v1)
 *   spec/capability.schema.json  ($id cloudpath.dev/capability/v1alpha1)
 * ------------------------------------------------------------------ */

/** 任意 JSON 值（Observation.value / inputSchema 等开放字段） */
export type Json =
  | string | number | boolean | null
  | Json[]
  | { [k: string]: Json }

/** Descriptor: device status 枚举 */
export type DeviceStatus = 'online' | 'offline' | 'unavailable' | 'degraded'

/** Descriptor: entity category 枚举 */
export type EntityCategory = 'sensor' | 'actuator' | 'diagnostic' | 'config'

/** 观测质量（Descriptor 与 Capability 共用同一枚举） */
export type ObservationQuality = 'good' | 'uncertain' | 'bad' | 'unavailable'

/** Observation：可形成当前状态的观测值（descriptor.schema.json #/definitions/Observation） */
export interface Observation {
  /** Capability 引用，形如 `cloudpath.dev/capability/temperature@1` */
  capability: string
  property: string
  value: unknown
  /** 统一单位代码（如 Cel / min），不得本地化 */
  unit?: string
  quality?: ObservationQuality
  /** 设备侧时间（不可信时以 received_at 为准） */
  observed_at?: string
  /** Edge/Core 生成的可信接收时间 */
  received_at?: string
  sequence?: number
}

/** Entity：Device 下可独立观察/控制/命名/授权的逻辑单元 */
export interface DescriptorEntity {
  entity_id: string
  /** Driver 范围内用户不可改的唯一键 */
  unique_key: string
  name?: string
  category: EntityCategory
  /** Capability 引用列表（字符串，允许带或不带 @version） */
  capabilities: string[]
  observations?: Record<string, Observation>
}

/** DeviceDescriptor：Edge/Adapter 上报、A2 前端渲染的唯一设备描述 */
export interface DeviceDescriptor {
  /** Core 内稳定 ID（不得用串口名） */
  device_id: string
  /** Driver 范围内不可变 ID */
  external_id: string
  manufacturer?: string
  model?: string
  status: DeviceStatus
  entities: DescriptorEntity[]
}

/** Capability spec.properties.* */
export interface CapabilityProperty {
  type?: string
  unit?: string
  access?: 'read' | 'write' | 'readwrite'
  quality?: ObservationQuality[]
  /** 开放扩展（title/description/enum/min/max 等 UI 与校验提示） */
  [k: string]: unknown
}

/** Capability spec.events.* */
export interface CapabilityEventDecl {
  title?: string
  description?: string
  payloadSchema?: Record<string, unknown>
  [k: string]: unknown
}

/** Capability spec.actions.* —— 命令面板的事实源（前端不再维护命令白名单/文案表） */
export interface CapabilityActionDecl {
  title?: string
  description?: string
  inputSchema?: Record<string, unknown>
  /** 下发到 POST commands 的 cmd 字段（缺省用 action key） */
  command?: string
  /** UI Hint（非语义真相）：primary / destructive / confirmation 文案 */
  primary?: boolean
  destructive?: boolean
  confirmation?: string
  [k: string]: unknown
}

/** Capability spec.presentation —— UI Hint，不是语义真相（capability-model.md §2） */
export interface CapabilityPresentation {
  primaryProperty?: string
  defaultWidget?: string
  [k: string]: unknown
}

export interface CapabilitySpec {
  properties?: Record<string, CapabilityProperty>
  events?: Record<string, CapabilityEventDecl>
  actions?: Record<string, CapabilityActionDecl>
  presentation?: CapabilityPresentation
}

export interface CapabilityMetadata {
  /** `^.+/capability/.+@\d+$` */
  id: string
  version: number
  title?: string
}

/** 一份 Capability 文档（catalog 下发或随 Descriptor 一并返回） */
export interface CapabilityDoc {
  apiVersion?: string
  kind?: string
  metadata: CapabilityMetadata
  spec?: CapabilitySpec
}

/** Descriptor 接口的宽容载荷：裸 Descriptor / {descriptor} / {descriptor,capabilities} / 列表 / 映射 */
export interface DescriptorEnvelope {
  descriptor?: DeviceDescriptor
  descriptors?: DeviceDescriptor[]
  capabilities?: CapabilityDoc[]
  [k: string]: unknown
}