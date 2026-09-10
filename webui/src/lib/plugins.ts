// 插件控制面的展示逻辑（纯函数、无副作用、无 React）。
//
// 三条硬约束（docs/architecture/control-plane-sync.md 不变量 5/6，任务书 §6.5）：
//   ① desired 与 observed **永远分别呈现**：desired.enabled 绝不可渲染成「运行中/健康」。
//   ② `has_observed=false` → 显式呈现对应运行宿主未上报；`stale` / `drift` 各有独立视觉状态。
//   ③ 错误一律按 api.PluginErr* **稳定码**呈现文案，不解析服务端错误文本；
//      secret 只显示 handle 名，不显示明文；不呈现本机绝对路径与插件 stdout/stderr 原文。
import { i18n } from '@/i18n'
import { resolveLocalizedText } from '@/i18n/pluginText'
import { ApiError } from './api'
import type { Tone } from '@/components/ui'
import { PluginErr } from './types'
import { normalizePluginUI } from './plugin-ui'
import type {
  PluginApplicationContributionData, PluginCatalogContributesView, PluginCatalogDriverView,
  PluginCatalogView, PluginConnectorContributionData, PluginErrCode, PluginInstanceView,
  PluginPermissionsData,
} from './types'

function pluginText(key: string, options?: Record<string, unknown>): string {
  return i18n.t(key, { ns: 'plugin', ...options }) as string
}

/* ------------------------------------------------------------------ *
 * ① 稳定错误码 → 文案
 * ------------------------------------------------------------------ */

type MappedPluginErrCode = PluginErrCode

export interface PluginErrorCopy {
  /** 一行标题（说清「发生了什么」） */
  title: string
  /** 一句可执行的下一步（说清「用户能做什么」） */
  hint: string
  tone: Tone
  /** 命中的稳定码；undefined = 服务端未给码（只报状态，不猜业务规则） */
  code?: MappedPluginErrCode
  /** 该错误是否要求「显式确认权限扩大」后重试（对应 confirm_permissions） */
  needsPermissionConfirm: boolean
  /** 是否可原样重试（同一 payload） */
  retryable: boolean
}

const ERR_META: Record<MappedPluginErrCode, Omit<PluginErrorCopy, 'title' | 'hint' | 'code'>> = {
  [PluginErr.NotFound]: { tone: 'idle', needsPermissionConfirm: false, retryable: false },
  [PluginErr.Conflict]: { tone: 'warn', needsPermissionConfirm: false, retryable: false },
  [PluginErr.Quota]: { tone: 'warn', needsPermissionConfirm: false, retryable: false },
  [PluginErr.PermissionConfirm]: { tone: 'warn', needsPermissionConfirm: true, retryable: true },
  [PluginErr.EdgeOffline]: { tone: 'warn', needsPermissionConfirm: false, retryable: true },
  [PluginErr.SecretForbidden]: { tone: 'bad', needsPermissionConfirm: false, retryable: false },
  [PluginErr.InvalidConfig]: { tone: 'bad', needsPermissionConfirm: false, retryable: false },
  [PluginErr.StoreUnavailable]: { tone: 'bad', needsPermissionConfirm: false, retryable: true },
  [PluginErr.HostMismatch]: { tone: 'warn', needsPermissionConfirm: false, retryable: false },
  [PluginErr.KindUnsupported]: { tone: 'warn', needsPermissionConfirm: false, retryable: false },
  [PluginErr.KindUnavailable]: { tone: 'warn', needsPermissionConfirm: false, retryable: true },
}

const KNOWN = new Set<string>(Object.keys(ERR_META))

function errorCopy(code: MappedPluginErrCode): PluginErrorCopy {
  return {
    ...ERR_META[code],
    code,
    title: pluginText(`errors.codes.${code}.title`),
    hint: pluginText(`errors.codes.${code}.hint`),
  }
}

/**
 * 把任意写操作异常映射成可呈现文案。
 * 只认 `ApiError.code`（服务端稳定码）；无码时按 HTTP 状态给**通用**说明，
 * 绝不把服务端 message 当规则复述，也不做自然语言解析。
 */
export function pluginErrorCopy(e: unknown): PluginErrorCopy {
  if (e instanceof ApiError && e.code && KNOWN.has(e.code)) return errorCopy(e.code as MappedPluginErrCode)
  if (e instanceof ApiError) {
    if (e.status === 401) {
      return {
        title: pluginText('errors.http401.title'), hint: pluginText('errors.http401.hint'),
        tone: 'warn', needsPermissionConfirm: false, retryable: true,
      }
    }
    if (e.status === 403) {
      return {
        title: pluginText('errors.http403.title'), hint: pluginText('errors.http403.hint'),
        tone: 'warn', needsPermissionConfirm: false, retryable: false,
      }
    }
    if (e.status === 429) {
      return {
        title: pluginText('errors.http429.title'),
        hint: e.retryAfter
          ? pluginText('errors.http429After.hint', { seconds: e.retryAfter })
          : pluginText('errors.http429.hint'),
        tone: 'warn', needsPermissionConfirm: false, retryable: true,
      }
    }
    return {
      title: pluginText('errors.httpStatus.title', { status: e.status }),
      hint: pluginText('errors.httpStatus.hint'),
      tone: 'bad', needsPermissionConfirm: false, retryable: true,
    }
  }
  return {
    title: pluginText('errors.network.title'), hint: pluginText('errors.network.hint'),
    tone: 'bad', needsPermissionConfirm: false, retryable: true,
  }
}

/* ------------------------------------------------------------------ *
 * ② desired ↔ observed 同步状态
 * ------------------------------------------------------------------ */

/** 同步状态判定顺序即优先级：先「有没有上报」，再「新不新」，最后「一不一致」 */
export type SyncKey = 'unreported' | 'stale' | 'drift' | 'pending' | 'synced'

export interface SyncState {
  key: SyncKey
  label: string
  tone: Tone
  hint: string
}

function hostLabel(serverHosted: boolean): string {
  return serverHosted ? pluginText('host.server') : pluginText('host.edge')
}

/**
 * 由服务端投影字段推导同步状态 —— 前端**不自己算**是否 drift/stale，
 * 只把 `drift` / `stale` / `has_observed` / revision 说成人话。
 */
export function syncState(v: PluginInstanceView): SyncState {
  const serverHosted = v.edge_id === 'server'
  const host = hostLabel(serverHosted)
  if (!v.has_observed) {
    return {
      key: 'unreported',
      label: pluginText('sync.unreported.label'),
      tone: 'idle',
      hint: serverHosted
        ? pluginText('sync.unreported.hintServer')
        : v.edge_online
          ? pluginText('sync.unreported.hintEdgeOnline')
          : pluginText('sync.unreported.hintEdgeOffline'),
    }
  }
  if (v.stale) {
    return { key: 'stale', label: pluginText('sync.stale.label'), tone: 'warn', hint: pluginText('sync.stale.hint', { host }) }
  }
  if (v.drift) {
    return { key: 'drift', label: pluginText('sync.drift.label'), tone: 'warn', hint: pluginText('sync.drift.hint', { host }) }
  }
  if (v.applied_revision < v.desired_revision) {
    return { key: 'pending', label: pluginText('sync.pending.label'), tone: 'accent', hint: pluginText('sync.pending.hint', { host }) }
  }
  return { key: 'synced', label: pluginText('sync.synced.label'), tone: 'ok', hint: pluginText('sync.synced.hint', { host }) }
}

/** 实例状态 → 展示语义。两套后端事实源：
 *  - edge pluginhost.State：规范大写（STOPPED/HEALTHY…）；
 *  - server AppHost（appruntime.InstanceState）：小写（running/stopping…，见 internal/appruntime/types.go）。
 *  两套都在同一 observed 投影里，词汇必须都覆盖，否则服务器托管实例会露出机器串。
 *  未知值原样呈现，不猜含义。 */
const STATE_KEYS: Record<string, { key: string; tone: Tone }> = {
  STOPPED: { key: 'stopped', tone: 'idle' }, STARTING: { key: 'starting', tone: 'accent' },
  HEALTHY: { key: 'running', tone: 'ok' }, DEGRADED: { key: 'degraded', tone: 'warn' },
  CRASHED: { key: 'crashed', tone: 'bad' }, BACKOFF: { key: 'backoff', tone: 'warn' },
  DISABLED: { key: 'disabled', tone: 'idle' }, created: { key: 'created', tone: 'idle' },
  starting: { key: 'starting', tone: 'accent' }, running: { key: 'running', tone: 'ok' },
  stopping: { key: 'stopping', tone: 'accent' }, stopped: { key: 'stopped', tone: 'idle' },
  degraded: { key: 'degraded', tone: 'warn' }, crashed: { key: 'crashed', tone: 'bad' },
  backoff: { key: 'backoff', tone: 'warn' }, disabled: { key: 'disabled', tone: 'idle' },
  failed: { key: 'failed', tone: 'bad' },
}

/** observed.detail 的已知机器标记 → 人话（其余是 server 脱敏摘要，原样呈现） */
export function hostDetailLabel(detail?: string): string | undefined {
  if (!detail) return undefined
  return detail === 'server-apphost' ? pluginText('host.server') : detail
}

const HEALTH_KEYS: Record<string, { key: string; tone: Tone }> = {
  HEALTHY: { key: 'healthy', tone: 'ok' }, DEGRADED: { key: 'degraded', tone: 'warn' },
  UNHEALTHY: { key: 'unhealthy', tone: 'bad' }, UNKNOWN: { key: 'unknown', tone: 'idle' },
  healthy: { key: 'healthy', tone: 'ok' }, degraded: { key: 'degraded', tone: 'warn' },
  unhealthy: { key: 'unhealthy', tone: 'bad' }, unknown: { key: 'unknown', tone: 'idle' },
}

export function stateMeta(state: string | undefined): { label: string; tone: Tone } {
  if (!state) return { label: pluginText('state.notReported'), tone: 'idle' }
  const meta = STATE_KEYS[state]
  return meta ? { label: pluginText(`state.${meta.key}`), tone: meta.tone } : { label: pluginText('state.unknown'), tone: 'idle' }
}

export function healthMeta(health: string | undefined): { label: string; tone: Tone } {
  if (!health) return { label: pluginText('health.notReported'), tone: 'idle' }
  const meta = HEALTH_KEYS[health]
  return meta ? { label: pluginText(`health.${meta.key}`), tone: meta.tone } : { label: pluginText('health.unknown'), tone: 'idle' }
}

export function instanceLocationLabel(v: PluginInstanceView): string {
  if (v.edge_id === 'server') return pluginText('location.server')
  const id = v.edge_id || pluginText('location.unknownEdge')
  return v.edge_online ? pluginText('location.edge', { id }) : pluginText('location.edgeOffline', { id })
}

export type InstanceStatusKey = 'normal' | 'attention' | 'unknown' | 'stopped'

export interface InstanceStatus {
  key: InstanceStatusKey
  label: string
  tone: Tone
  summary: string
  next?: string
  needsAttention: boolean
  /** 列表默认排序优先级：需要处理 > 状态待确认 > 已停止 > 运行正常 */
  priority: number
}

/** 把实际运行事实说成普通用户能理解的一句话，并给出下一步；机器原值只留给技术详情。 */
export function instanceStatus(v: PluginInstanceView): InstanceStatus {
  const serverHosted = v.edge_id === 'server'
  const host = hostLabel(serverHosted)
  const state = stateMeta(v.observed?.state)
  const health = healthMeta(v.observed?.health)

  // 设置已停用且宿主未上报：用户主动关掉了应用，这里就是「已停止」，
  // 不能报成「状态待确认 / 还没有收到运行状态」——那会让关掉的应用看起来像故障。
  if (!v.desired.enabled && !v.has_observed) {
    return {
      key: 'stopped', label: pluginText('status.stopped.label'), tone: 'idle',
      summary: pluginText('status.disabledSummary'), needsAttention: false, priority: 2,
    }
  }

  if (!v.has_observed) {
    const hostOffline = !serverHosted && v.desired.enabled && !v.edge_online
    if (hostOffline) {
      return {
        key: 'attention', label: pluginText('status.unreportedOffline.label'), tone: 'warn',
        summary: pluginText('status.unreportedOffline.summary'), next: pluginText('status.unreportedOffline.next'),
        needsAttention: true, priority: 0,
      }
    }
    return {
      key: 'unknown', label: pluginText('status.unreportedServer.label'), tone: 'idle',
      summary: pluginText('status.unreportedServer.summary', { host }),
      next: serverHosted ? pluginText('status.unreportedServer.next') : pluginText('status.unreportedEdge.next'),
      needsAttention: false, priority: 1,
    }
  }

  if (v.stale) {
    return {
      key: 'attention', label: pluginText('status.stale.label'), tone: 'warn',
      summary: pluginText('status.stale.summary', { state: state.label }),
      next: v.edge_id !== 'server' && !v.edge_online
        ? pluginText('status.stale.nextOffline')
        : pluginText('status.stale.nextOnline'),
      needsAttention: true, priority: 0,
    }
  }

  if (v.drift || (!v.desired.enabled && state.tone === 'ok')) {
    const healthCopy = v.observed?.health && health.tone !== 'ok' && health.tone !== 'idle'
      ? pluginText('status.healthSuffix', { label: health.label }) : ''
    return {
      key: 'attention', label: pluginText('status.drift.label'), tone: 'warn',
      summary: !v.desired.enabled && state.tone === 'ok'
        ? pluginText('status.drift.summaryRunning')
        : pluginText('status.drift.summaryCurrent', { state: state.label, health: healthCopy }),
      next: pluginText('status.drift.next'),
      needsAttention: true, priority: 0,
    }
  }

  if (state.label === pluginText('state.stopped') || state.label === pluginText('state.disabled')) {
    return {
      key: 'stopped', label: pluginText('status.stopped.label'), tone: 'idle',
      summary: pluginText('status.stopped.summary'),
      next: v.desired.enabled ? pluginText('status.stopped.next') : undefined,
      needsAttention: false, priority: 2,
    }
  }

  const abnormal = state.tone === 'bad' || state.tone === 'warn' || health.tone === 'bad' || health.tone === 'warn'
  if (abnormal) {
    const healthCopy = v.observed?.health && health.label !== pluginText('health.notReported')
      ? pluginText('status.healthSuffix', { label: health.label }) : ''
    return {
      key: 'attention', label: pluginText('status.abnormal.label'),
      tone: state.tone === 'bad' || health.tone === 'bad' ? 'bad' : 'warn',
      summary: pluginText('status.abnormal.summary', { state: state.label, health: healthCopy }),
      next: pluginText('status.abnormal.next'), needsAttention: true, priority: 0,
    }
  }

  if (state.tone === 'ok') {
    return {
      key: 'normal', label: pluginText('status.normal.label'), tone: 'ok',
      summary: pluginText('status.normal.summary', { state: state.label }),
      needsAttention: false, priority: 3,
    }
  }

  return {
    key: 'unknown', label: pluginText('status.unknown.label'), tone: 'idle',
    summary: pluginText('status.unknown.summary', { state: state.label }),
    next: pluginText('status.unknown.next'), needsAttention: false, priority: 1,
  }
}

/** 目录里的 observed_state 在 server 侧未观测时是小写 unknown（plugincatalog 约定） */
export function trustMeta(mode: string | undefined, verified: boolean): { label: string; tone: Tone } {
  if (verified) {
    return { label: mode ? pluginText('trust.verifiedWithMode', { mode }) : pluginText('trust.verified'), tone: 'ok' }
  }
  return { label: mode ? pluginText('trust.unverifiedWithMode', { mode }) : pluginText('trust.unverified'), tone: 'warn' }
}

export const ISOLATION_LABELS: Record<string, string> = {
  shared: 'isolation.shared', 'per-instance': 'isolation.independent', none: 'isolation.none',
  process: 'isolation.independent', container: 'isolation.independent',
}

export function isolationLabel(isolation: string | undefined): string {
  if (!isolation) return pluginText('isolation.unspecified')
  const key = ISOLATION_LABELS[isolation]
  return key ? pluginText(key) : isolation
}

/* ------------------------------------------------------------------ *
 * ③ 权限与 secret 呈现
 * ------------------------------------------------------------------ */

export interface PermissionGroup {
  /** 权限类别（展示名） */
  group: string
  key: keyof PluginPermissionsData
  items: string[]
  tone: Tone
}

/** 权限分组顺序固定：硬件 > 网络 > 文件系统 > secret（风险由高到低） */
const PERM_GROUPS: { key: keyof PluginPermissionsData; tone: Tone }[] = [
  { key: 'hardware', tone: 'warn' }, { key: 'network', tone: 'warn' },
  { key: 'filesystem', tone: 'warn' }, { key: 'secrets', tone: 'bad' },
]

/** 只列出**声明了**的权限组；未声明的组不出现（不塞「无」占位，避免满屏 badge） */
export function permissionGroups(p: PluginPermissionsData | undefined): PermissionGroup[] {
  if (!p) return []
  const out: PermissionGroup[] = []
  for (const g of PERM_GROUPS) {
    const items = p[g.key] ?? []
    if (items.length > 0) out.push({ ...g, group: pluginText(`permissions.groups.${g.key}`), items })
  }
  return out
}

export function permissionCount(p: PluginPermissionsData | undefined): number {
  return permissionGroups(p).reduce((n, g) => n + g.items.length, 0)
}

/**
 * secret 引用 → 可显示的 handle 名。
 * 接受 `secret://<name>` 与裸 `<name>` 两种形态；**只输出名字，绝不输出值**。
 * 任何看起来像明文的输入也原样当名字截断显示，不做还原。
 */
export function secretHandleName(ref: string): string {
  const m = /^secret:\/\/(.+)$/.exec(ref.trim())
  return (m?.[1] ?? ref).trim() || pluginText('common.emptyHandle')
}

/** 配置项呈现：secret:// 值一律折叠成 handle 名，防止明文出现在 DOM 里 */
export function safeConfigEntries(
  config: Record<string, string> | undefined,
): { key: string; value: string; isSecret: boolean }[] {
  return Object.entries(config ?? {})
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([key, value]) => {
      const isSecret = typeof value === 'string' && value.startsWith('secret://')
      return { key, value: isSecret ? secretHandleName(value) : String(value ?? ''), isSecret }
    })
}
/** 插件在界面上的名称：优先使用插件提供的标题，机器标识只留在技术详情。 */
export function pluginDisplayName(catalog?: PluginCatalogView): string {
  const contributions = [
    ...(catalog?.contributes?.drivers ?? []),
    ...(catalog?.contributes?.applications ?? []),
    ...(catalog?.contributes?.connectors ?? []),
  ]
  const title = contributions.map((x) => resolveLocalizedText(x, 'title')).find(Boolean)
  return title || catalog?.id || pluginText('display.unknownPlugin')
}

const PERMISSION_ITEM_KEYS: Record<string, string> = {
  'hardware:uart': 'uart', 'hardware:serial': 'serial', 'hardware:serial-port': 'serialPort',
  'hardware:gpio': 'gpio', 'hardware:i2c': 'i2c', 'hardware:spi': 'spi', 'hardware:usb': 'usb',
  'network:outbound': 'outbound', 'network:inbound': 'inbound',
  'network:local-network': 'localNetwork', 'network:http': 'http',
  'filesystem:read': 'read', 'filesystem:write': 'write',
}

/** 权限项的人话标签；未知项原样保留，避免猜业务含义。 */
export function permissionItemLabel(group: keyof PluginPermissionsData, item: string): string {
  if (group === 'secrets') return pluginText('permissions.items.secret', { name: item })
  const key = PERMISSION_ITEM_KEYS[`${group}:${item}`]
  return key ? pluginText(`permissions.items.${key}`) : item
}

/* ------------------------------------------------------------------ *
 * ④ 列表载荷的宽容归一化（防白屏）
 * ------------------------------------------------------------------ */

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

function bool(v: unknown): boolean {
  return v === true
}

function normalizeI18nText(raw: unknown): Record<string, string> | undefined {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return undefined
  const out: Record<string, string> = {}
  for (const [key, value] of Object.entries(raw)) {
    if (typeof value === 'string' && value.trim()) out[key] = value.trim()
  }
  return Object.keys(out).length > 0 ? out : undefined
}

/**
 * 归一化单个实例视图：形状不合法（缺 id / 缺 desired）返回 null 由调用方丢弃，
 * 其余字段一律给安全默认值。**不补任何看起来合理的值**（例如不会把 has_observed
 * 猜成 true），因为这正是「把期望当实际」的来源。
 */
export function normalizeInstance(raw: unknown): PluginInstanceView | null {
  if (!raw || typeof raw !== 'object') return null
  const o = raw as Record<string, unknown>
  if (!o.desired || typeof o.desired !== 'object' || Array.isArray(o.desired)) return null
  const d = o.desired as Record<string, unknown>
  const id = str(o.id)
  if (!id) return null
  const obs = o.observed && typeof o.observed === 'object'
    ? o.observed as Record<string, unknown> : null
  const hasObserved = bool(o.has_observed)
  return {
    id,
    tenant_id: typeof o.tenant_id === 'number' ? o.tenant_id : 0,
    edge_id: str(o.edge_id),
    desired: {
      instance_id: str(d.instance_id) || id,
      plugin_id: str(d.plugin_id),
      version: str(d.version),
      enabled: bool(d.enabled),
      isolation: str(d.isolation),
      config: (d.config && typeof d.config === 'object' ? d.config : undefined) as
        Record<string, string> | undefined,
      secret_refs: Array.isArray(d.secret_refs) ? d.secret_refs.map(str).filter(Boolean) : undefined,
      revision: typeof d.revision === 'number' ? d.revision : 0,
      updated_at: typeof d.updated_at === 'number' ? d.updated_at : 0,
    },
    // has_observed=false 时即使服务端多给了 observed 也不采纳：以服务端判据为准
    has_observed: hasObserved,
    observed: hasObserved && obs ? {
      state: str(obs.state),
      health: str(obs.health),
      version: typeof obs.version === 'string' && obs.version ? obs.version : undefined,
      detail: typeof obs.detail === 'string' && obs.detail ? obs.detail : undefined,
      restart_count: typeof obs.restart_count === 'number' ? obs.restart_count : 0,
      last_healthy: typeof obs.last_healthy === 'number' ? obs.last_healthy : undefined,
      reported_at: typeof obs.reported_at === 'number' ? obs.reported_at : undefined,
    } : undefined,
    edge_online: bool(o.edge_online),
    desired_revision: typeof o.desired_revision === 'number' ? o.desired_revision : 0,
    applied_revision: typeof o.applied_revision === 'number' ? o.applied_revision : 0,
    drift: bool(o.drift),
    stale: bool(o.stale),
    last_ack_at: typeof o.last_ack_at === 'number' && o.last_ack_at ? o.last_ack_at : undefined,
  }
}

/** GET /api/plugin-instances 的宽容归一化：非数组/缺席一律空列表 */
export function normalizeInstances(raw: unknown): PluginInstanceView[] {
  const list = Array.isArray(raw)
    ? raw
    : (raw && typeof raw === 'object' ? (raw as Record<string, unknown>).instances : undefined)
  if (!Array.isArray(list)) return []
  const out: PluginInstanceView[] = []
  for (const item of list) {
    const v = normalizeInstance(item)
    if (v) out.push(v)
  }
  return out.sort((a, b) => a.id.localeCompare(b.id))
}

export type PluginKind = 'application' | 'driver' | 'connector' | 'unknown'

/** 后端插件 kind 可能是 Driver/Application/Connector；界面统一用稳定小写值。 */
export function normalizePluginKind(kind: string | undefined): PluginKind {
  const value = (kind ?? '').trim().toLowerCase()
  if (value === 'application' || value === 'driver' || value === 'connector') return value
  return 'unknown'
}

function normalizePermissions(raw: unknown): PluginPermissionsData {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {}
  const o = raw as Record<string, unknown>
  const list = (key: string) => Array.isArray(o[key]) ? o[key].map(str).filter(Boolean) : undefined
  return {
    hardware: list('hardware'),
    network: list('network'),
    filesystem: list('filesystem'),
    secrets: list('secrets'),
  }
}

function normalizeDriverContribution(raw: unknown): PluginCatalogDriverView | null {
  if (!raw || typeof raw !== 'object') return null
  const o = raw as Record<string, unknown>
  const id = str(o.id)
  if (!id) return null
  return {
    id,
    title: str(o.title) || undefined,
    i18n: normalizeI18nText(o.i18n),
    descriptor: str(o.descriptor) || undefined,
    configSchema: str(o.configSchema) || undefined,
    discovery: str(o.discovery) || undefined,
    capabilityCatalog: str(o.capabilityCatalog) || undefined,
    ui: normalizePluginUI(o.ui),
  }
}

function normalizeApplicationContribution(raw: unknown): PluginApplicationContributionData | null {
  if (!raw || typeof raw !== 'object') return null
  const o = raw as Record<string, unknown>
  const id = str(o.id)
  if (!id) return null
  return {
    id,
    title: str(o.title) || undefined,
    i18n: normalizeI18nText(o.i18n),
    ui: normalizePluginUI(o.ui),
  }
}

function normalizeConnectorContribution(raw: unknown): PluginConnectorContributionData | null {
  if (!raw || typeof raw !== 'object') return null
  const o = raw as Record<string, unknown>
  const id = str(o.id)
  if (!id) return null
  return {
    id,
    title: str(o.title) || undefined,
    i18n: normalizeI18nText(o.i18n),
    direction: str(o.direction) || undefined,
    host: str(o.host) || undefined,
  }
}

function normalizeContributions(raw: unknown): PluginCatalogContributesView {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {}
  const o = raw as Record<string, unknown>
  const map = <T>(value: unknown, fn: (item: unknown) => T | null): T[] | undefined => {
    if (!Array.isArray(value)) return undefined
    const items = value.map(fn).filter((item): item is T => item !== null)
    return items.length > 0 ? items : undefined
  }
  return {
    drivers: map(o.drivers, normalizeDriverContribution),
    applications: map(o.applications, normalizeApplicationContribution),
    connectors: map(o.connectors, normalizeConnectorContribution),
  }
}

/** GET /api/plugins 的宽容归一化；UI contribution 只保留契约允许的形状。 */
export function normalizeCatalog(raw: unknown): PluginCatalogView[] {
  const list = Array.isArray(raw)
    ? raw
    : (raw && typeof raw === 'object' ? (raw as Record<string, unknown>).plugins : undefined)
  if (!Array.isArray(list)) return []
  const out: PluginCatalogView[] = []
  for (const item of list) {
    if (!item || typeof item !== 'object') continue
    const o = item as Record<string, unknown>
    const id = str(o.id)
    if (!id) continue
    out.push({
      id,
      kind: normalizePluginKind(str(o.kind)),
      version: str(o.version),
      source: str(o.source),
      digest: str(o.digest),
      verified: bool(o.verified),
      compatibility: str(o.compatibility) || undefined,
      protocol: typeof o.protocol === 'number' && Number.isFinite(o.protocol) ? o.protocol : 0,
      permissions: normalizePermissions(o.permissions),
      contributes: normalizeContributions(o.contributes),
    })
  }
  return out.sort((a, b) => a.id.localeCompare(b.id))
}

/** 按 plugin_id 建目录索引（实例详情用它取 Trust / Permissions 这些声明事实） */
export function indexCatalog(list: PluginCatalogView[]): Map<string, PluginCatalogView> {
  return new Map(list.map((p) => [p.id, p]))
}

/** 摘要用短 digest（全长放进 title，不在界面上铺一长串十六进制） */
export function shortDigest(digest: string | undefined): string {
  if (!digest) return '—'
  const hex = digest.replace(/^[a-z0-9-]+:/i, '')
  return hex.length > 12 ? `${hex.slice(0, 12)}…` : hex
}
