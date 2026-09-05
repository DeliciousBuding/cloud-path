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
  | 'hello' | 'snapshot' | 'state' | 'event' | 'command' | 'command_ack'
  | 'edge_up' | 'edge_down' | 'descriptor'
  | 'plugin_status' | 'plugin_desired' | 'plugin_ack'
  | 'ping' | 'pong'

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
  /** 后端可随快照下发 Descriptor（宽容消费：形状由 lib/descriptor.ts 归一化） */
  descriptors?: unknown[]
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
  /** 已禁用账号（服务端 omitempty：未禁用时字段缺席） */
  disabled?: boolean
}

export interface MeResponse {
  user: UserView
}

/* ------------------------------------------------------------------ *
 * 管理面（docs/api.md §3.2-3.3）：用户管理 + 租户服务令牌
 * ------------------------------------------------------------------ */

/** POST /api/users 请求体（username 在租户内唯一） */
export interface CreateUserInput {
  username: string
  /** 留空则服务端回落为 username */
  name?: string
  role: Role
  password: string
}

/** PATCH /api/users/{id} 请求体：只带要改的字段 */
export interface UpdateUserInput {
  name?: string
  role?: Role
  disabled?: boolean
  password?: string
}

/** 服务令牌 scope（docs/api.md §3.3：read|write|admin|edge 的非空子集） */
export type TokenScope = 'read' | 'write' | 'admin' | 'edge'

/**
 * 令牌元数据视图：只有短 prefix，**永不携带明文**。
 * 明文仅出现在 POST /api/tokens 的响应里一次（见 CreatedToken）。
 */
export interface TokenView {
  id: number
  name: string
  prefix: string
  scopes: TokenScope[]
  created_at: number
  expires_at?: number
  last_used_at?: number
  revoked_at?: number
}

/** POST /api/tokens 响应：`token` 是明文，只此一次，不得落任何持久化通道 */
export interface CreatedToken extends TokenView {
  token: string
}

/** POST /api/tokens 请求体 */
export interface CreateTokenInput {
  name: string
  scopes: TokenScope[]
  /** 缺省 = 永不过期（字段不出现在 body 里） */
  expires_at?: number
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
/* ================================================================== *
 * v0.1 收口：Overview 聚合读面 + 插件控制面（逐字镜像 internal/api/types.go）
 *
 * 映射约定：
 *   - Go int/int64/uint64 → TS number（JSON 数字；revision 由 server 保证在安全整数范围内）
 *   - Go `json:"x,omitempty"` → TS 可选字段 `x?`
 *   - Go *T（指针，可空）→ TS `T | undefined`（可选）
 *   - Go map[string]string → TS Record<string, string>
 * 字段名一律取 JSON tag 的 snake_case，与 Go 侧逐字一致（见 lib/__tests__/contract.test.ts）。
 * ================================================================== */

/** GET /api/overview → api.OverviewView（一屏产品级概览的唯一数据源，禁止前端自算假数据） */
export interface OverviewView {
  devices_online: number
  devices_total: number
  edges_online: number
  edges_total: number
  plugins_active: number
  plugins_desired: number
  commands_failed: number
  recent_events: EventView[]
  offline_devices: DeviceView[]
  failed_commands: CommandView[]
  server_time: number
}

/* ------------------------------------------------------------------ *
 * 插件控制面同步（docs/architecture/control-plane-sync.md）
 * ------------------------------------------------------------------ */

/** 插件公开权限声明：只有权限名，永不携带凭据值 */
export interface PluginPermissionsData {
  hardware?: string[]
  network?: string[]
  filesystem?: string[]
  secrets?: string[]
}

export interface PluginDriverContributionData {
  id: string
  title?: string
  discovery?: string
}

export interface PluginApplicationContributionData {
  id: string
  title?: string
}

export interface PluginConnectorContributionData {
  id: string
  title?: string
  direction?: string
  host?: string
}

export interface PluginContributionsData {
  drivers?: PluginDriverContributionData[]
  applications?: PluginApplicationContributionData[]
  connectors?: PluginConnectorContributionData[]
}

/** Edge 上报的已安装插件公开事实（无本地路径 / 启动参数 / 环境变量 / secret 值） */
export interface PluginInstallationStatusData {
  plugin_id: string
  version: string
  kind: string
  protocol: number
  digest: string
  trust_mode: string
  verified: boolean
  verified_publisher?: string
  permissions: PluginPermissionsData
  contributions: PluginContributionsData
  capabilities?: string[]
}

/** Edge Plugin Host 的实际态。desired 字段不得混进本结构（不变量 5） */
export interface PluginObservedInstanceData {
  instance_id: string
  plugin_id: string
  version: string
  host_online: boolean
  state: string
  health: string
  detail?: string
  restart_count: number
  last_healthy?: number
  message_rate?: number
}

/** Edge→Server 全量插件实际态快照 */
export interface PluginStatusData {
  boot_id: string
  sequence: number
  applied_revision: number
  installations: PluginInstallationStatusData[]
  instances: PluginObservedInstanceData[]
}

/** Server 权威期望态中的单个实例（config 值只含非敏感标量或 secret://<name> handle） */
export interface PluginDesiredInstanceData {
  instance_id: string
  plugin_id: string
  version: string
  enabled: boolean
  isolation: string
  config?: Record<string, string>
}

/** Server→Edge 声明式全量期望态快照 */
export interface PluginDesiredData {
  revision: number
  snapshot_digest: string
  instances: PluginDesiredInstanceData[]
}

/** 单实例 reconcile 结果（detail 已由 server 限长并脱敏） */
export interface PluginApplyResultData {
  instance_id: string
  status: string
  detail?: string
}

/** plugin_ack 稳定状态值（api.PluginAckApplied / Rejected / Failed） */
export const PLUGIN_ACK_APPLIED = 'applied'
export const PLUGIN_ACK_REJECTED = 'rejected'
export const PLUGIN_ACK_FAILED = 'failed'

/** Edge→Server 的 revision 应用结果 */
export interface PluginAckData {
  revision: number
  snapshot_digest: string
  status: string
  results?: PluginApplyResultData[]
}

/* ------------------------------------------------------------------ *
 * 插件实例管理：读面视图 + 写面请求/响应
 * ------------------------------------------------------------------ */

/** 实例的**期望态**（Server 权威） */
export interface PluginInstanceDesiredView {
  instance_id: string
  plugin_id: string
  version: string
  enabled: boolean
  isolation: string
  config?: Record<string, string>
  /** 只含 secret handle 名（如 `db-password`），永不含明文 */
  secret_refs?: string[]
  revision: number
  updated_at: number
}

/** 实例的**实际态**（Edge 上报投影）；缺席即「Edge 未上报」 */
export interface PluginInstanceObservedView {
  state: string
  health: string
  version?: string
  detail?: string
  restart_count: number
  last_healthy?: number
  reported_at?: number
}

/**
 * 单个插件实例。desired 与 observed **永远分别渲染**（control-plane-sync.md 不变量 5）：
 * `has_observed=false` → 必须显式呈现「Edge 未上报」，不得把 desired.enabled 当成运行中；
 * `stale=true` / `drift=true` → 必须有清晰视觉状态。
 */
export interface PluginInstanceView {
  id: string
  tenant_id: number
  edge_id: string
  desired: PluginInstanceDesiredView
  has_observed: boolean
  observed?: PluginInstanceObservedView
  edge_online: boolean
  desired_revision: number
  applied_revision: number
  drift: boolean
  stale: boolean
  last_ack_at?: number
}

/** GET /api/plugin-instances */
export interface PluginInstanceListResponse {
  instances: PluginInstanceView[]
}

/** POST /api/plugin-instances */
export interface PluginInstanceCreateRequest {
  edge_id: string
  instance_id: string
  plugin_id: string
  version: string
  enabled?: boolean
  isolation?: string
  config?: Record<string, string>
  secret_refs?: string[]
  /** 权限扩大必须显式确认；未确认时 server 回 PluginErrPermissionConfirm */
  confirm_permissions?: boolean
}

/** PATCH /api/plugin-instances/{id}：只带要改的字段 */
export interface PluginInstanceUpdateRequest {
  version?: string
  enabled?: boolean
  isolation?: string
  config?: Record<string, string>
  secret_refs?: string[]
  confirm_permissions?: boolean
}

/** DELETE /api/plugin-instances/{id} */
export interface PluginInstanceDeleteRequest {
  purge?: boolean
}

/** 写操作统一响应 */
export interface PluginInstanceWriteResponse {
  id: string
  revision: number
  request_id: string
  instance: PluginInstanceView
}

/** POST /api/plugin-instances/{id}/reconcile */
export interface PluginInstanceActionRequest {
  force?: boolean
}

/**
 * 插件实例写操作的**稳定错误码**（api.PluginErr*）。
 * 前端按码呈现文案，绝不解析错误文本 —— server 的 message 可能变，码不会。
 */
export const PluginErr = {
  NotFound: 'plugin_instance_not_found',
  Conflict: 'plugin_instance_conflict',
  Quota: 'plugin_quota_exceeded',
  PermissionConfirm: 'plugin_permission_confirmation_required',
  EdgeOffline: 'plugin_edge_offline',
  SecretForbidden: 'plugin_secret_forbidden',
  InvalidConfig: 'plugin_invalid_config',
} as const

export type PluginErrCode = typeof PluginErr[keyof typeof PluginErr]

/** 全部稳定错误码（用于把 server 的 error 文本回落到码：只匹配闭合枚举，不做自然语言解析） */
export const PLUGIN_ERR_CODES: readonly string[] = Object.values(PluginErr)

/* ------------------------------------------------------------------ *
 * GET /api/plugins（插件目录）
 * 注意：此视图镜像 internal/plugincatalog/model.go 的 PluginView，**不在**
 * internal/api/types.go 冻结契约内；字段变动需由 Server lane 同步到这里。
 * ------------------------------------------------------------------ */

export interface PluginCatalogDriverView {
  id: string
  title?: string
  descriptor?: string
  configSchema?: string
  discovery?: string
  capabilityCatalog?: string
}

export interface PluginCatalogContributesView {
  drivers?: PluginCatalogDriverView[]
  applications?: PluginApplicationContributionData[]
  connectors?: PluginConnectorContributionData[]
}

export interface PluginCatalogView {
  id: string
  kind: string
  version: string
  /** 安装来源标识：可能含本机路径，UI 不得直接呈现（安全边界） */
  source: string
  digest: string
  verified: boolean
  compatibility?: string
  protocol: number
  permissions: PluginPermissionsData
  contributes: PluginCatalogContributesView
}

/** GET /api/plugins */
export interface PluginCatalogListResponse {
  plugins: PluginCatalogView[]
}
// Application Plane: internal/api/types.go。数据面 ID 不含部署节点前缀。
export interface AppDomainRecordView {
  record_type: string
  record_id: string
  data_json: string
  version?: string
  updated_at: number
}
export interface DomainRecordData extends AppDomainRecordView {
  instance_id: string
  created: boolean
}
export interface AppDomainRecordsView {
  instance_id: string
  records: AppDomainRecordView[]
  record_type?: string
  limit: number
  offset: number
}
export interface AppBindingView {
  requirement_id: string
  capability: string
  entity_id: string
}
export interface AppBindingsView {
  instance_id: string
  running: boolean
  bindings: AppBindingView[]
}
export interface AppScheduledJobView {
  schedule_id: string
  cron: string
  timezone: string
  missed_policy: string
  next_run_at?: number
  last_run_at?: number
  state: string
  revision: number
}
export interface AppJobsView {
  instance_id: string
  running: boolean
  jobs: string[]
  scheduled: AppScheduledJobView[]
}
