// 插件控制面的展示逻辑（纯函数、无副作用、无 React）。
//
// 三条硬约束（docs/architecture/control-plane-sync.md 不变量 5/6，任务书 §6.5）：
//   ① desired 与 observed **永远分别呈现**：desired.enabled 绝不可渲染成「运行中/健康」。
//   ② `has_observed=false` → 显式呈现对应运行宿主未上报；`stale` / `drift` 各有独立视觉状态。
//   ③ 错误一律按 api.PluginErr* **稳定码**呈现文案，不解析服务端错误文本；
//      secret 只显示 handle 名，不显示明文；不呈现本机绝对路径与插件 stdout/stderr 原文。
import { ApiError } from './api'
import type { Tone } from '@/components/ui'
import { PluginErr } from './types'
import type {
  PluginCatalogView, PluginErrCode, PluginInstanceView, PluginPermissionsData,
} from './types'

/* ------------------------------------------------------------------ *
 * ① 稳定错误码 → 文案
 * ------------------------------------------------------------------ */

/** 当前运行时位置规则对应的稳定码；机器码只进入技术详情，不作主文案。 */
const PLUGIN_RUNTIME_ERR_CODES = {
  HostMismatch: 'plugin_instance_host_mismatch',
  KindUnsupported: 'plugin_instance_kind_unsupported',
  KindUnavailable: 'plugin_instance_kind_unavailable',
} as const

type PluginRuntimeErrCode = typeof PLUGIN_RUNTIME_ERR_CODES[keyof typeof PLUGIN_RUNTIME_ERR_CODES]
type MappedPluginErrCode = PluginErrCode | PluginRuntimeErrCode

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

const ERR_COPY: Record<MappedPluginErrCode, Omit<PluginErrorCopy, 'code'>> = {
  [PluginErr.NotFound]: {
    title: '没有找到这个运行实例',
    hint: '它可能已被删除，或不属于当前组织。请返回列表刷新后再试。',
    tone: 'idle', needsPermissionConfirm: false, retryable: false,
  },
  [PluginErr.Conflict]: {
    title: '无法保存：名称或版本冲突',
    hint: '同一个网关里已经有同名项目，或版本与现有记录不一致。请换一个名称，或先更新已有项目。',
    tone: 'warn', needsPermissionConfirm: false, retryable: false,
  },
  [PluginErr.Quota]: {
    title: '已经达到数量上限',
    hint: '当前组织可添加的项目数量已达上限，本次保存未生效。请先删除不用的项目，或联系管理员提高上限。',
    tone: 'warn', needsPermissionConfirm: false, retryable: false,
  },
  [PluginErr.PermissionConfirm]: {
    title: '需要你同意新增权限',
    hint: '这次修改会让插件获得更多权限。请核对下方权限并勾选确认，再重新提交。',
    tone: 'warn', needsPermissionConfirm: true, retryable: true,
  },
  [PluginErr.EdgeOffline]: {
    title: '目标网关当前离线',
    hint: '设置可以保存，但该网关暂时无法应用。网关重新连接后会自动同步最新设置。',
    tone: 'warn', needsPermissionConfirm: false, retryable: true,
  },
  [PluginErr.SecretForbidden]: {
    title: '找不到可用的密钥',
    hint: '这个密钥不存在、没有授权或已失效。请改用已授权的密钥名称；界面只显示名称，不显示明文。',
    tone: 'bad', needsPermissionConfirm: false, retryable: false,
  },
  [PluginErr.InvalidConfig]: {
    title: '设置内容有误',
    hint: '部分设置不符合要求，例如名称、长度或取值范围有误。请修改后重新提交。',
    tone: 'bad', needsPermissionConfirm: false, retryable: false,
  },
  [PLUGIN_RUNTIME_ERR_CODES.HostMismatch]: {
    title: '运行位置与插件类型不匹配',
    hint: '驱动程序只能运行在网关，应用插件只能运行在中心服务。新建时请选择正确运行位置；已有实例若位置不对，请删除后在正确位置重新创建。',
    tone: 'warn', needsPermissionConfirm: false, retryable: false,
  },
  [PLUGIN_RUNTIME_ERR_CODES.KindUnsupported]: {
    title: '暂不支持这种插件类型',
    hint: '连接器目前没有可用运行时，选择网关或中心服务都不能创建实例。请确认插件类型是否正确，或等待支持后再试。',
    tone: 'warn', needsPermissionConfirm: false, retryable: false,
  },
  [PLUGIN_RUNTIME_ERR_CODES.KindUnavailable]: {
    title: '暂时无法确认插件类型',
    hint: '请确认插件已经安装到目标运行位置并完成同步，然后刷新重试；如果已安装仍失败，请联系管理员检查插件安装信息。',
    tone: 'warn', needsPermissionConfirm: false, retryable: true,
  },
}

const KNOWN = new Set<string>(Object.keys(ERR_COPY))

/**
 * 把任意写操作异常映射成可呈现文案。
 * 只认 `ApiError.code`（服务端稳定码）；无码时按 HTTP 状态给**通用**说明，
 * 绝不把服务端 message 当规则复述，也不做自然语言解析。
 */
export function pluginErrorCopy(e: unknown): PluginErrorCopy {
  if (e instanceof ApiError && e.code && KNOWN.has(e.code)) {
    return { ...ERR_COPY[e.code as MappedPluginErrCode], code: e.code as MappedPluginErrCode }
  }
  if (e instanceof ApiError) {
    if (e.status === 401) {
      return {
        title: '登录已失效', hint: '请重新登录后再操作。',
        tone: 'warn', needsPermissionConfirm: false, retryable: true,
      }
    }
    if (e.status === 403) {
      return {
        title: '权限不足',
        hint: '当前账号不能修改这个项目，但仍可查看保存的设置和运行情况。',
        tone: 'warn', needsPermissionConfirm: false, retryable: false,
      }
    }
    if (e.status === 429) {
      return {
        title: '操作过于频繁',
        hint: e.retryAfter ? `请 ${e.retryAfter} 秒后重试。` : '请稍后重试。',
        tone: 'warn', needsPermissionConfirm: false, retryable: true,
      }
    }
    return {
      title: `保存失败（HTTP ${e.status}）`,
      hint: '平台没有保存这次修改，原设置保持不变。请稍后重试；如果仍然失败，请联系管理员。',
      tone: 'bad', needsPermissionConfirm: false, retryable: true,
    }
  }
  return {
    title: '无法连接平台',
    hint: '网络不可达或平台暂时不可用。本次保存未提交，原设置保持不变。',
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

/**
 * 由服务端投影字段推导同步状态 —— 前端**不自己算**是否 drift/stale，
 * 只把 `drift` / `stale` / `has_observed` / revision 说成人话。
 */
export function syncState(v: PluginInstanceView): SyncState {
  const serverHosted = v.edge_id === 'server'
  const host = serverHosted ? '中心服务' : '网关'
  if (!v.has_observed) {
    return {
      key: 'unreported',
      label: '状态待确认',
      tone: 'idle',
      hint: serverHosted
        ? '还没有收到这个应用的运行状态。保存的启用设置不代表它正在运行，请查看应用数据。'
        : v.edge_online
        ? '设置已保存，但还没有收到这个网关的运行状态，不能据此判断它是否正在运行。'
        : '网关当前离线，暂时没有运行状态。重新连接后会自动更新。',
    }
  }
  if (v.stale) {
    return {
      key: 'stale',
      label: '状态可能已过期',
      tone: 'warn',
      hint: host + '收到的运行状态已超过有效期，当前显示的是上次状态，不代表现在的运行情况。',
    }
  }
  if (v.drift) {
    return {
      key: 'drift',
      label: '有差异',
      tone: 'warn',
      hint: `${host}还没有应用最新设置。可以重新同步一次。`,
    }
  }
  if (v.applied_revision < v.desired_revision) {
    return {
      key: 'pending',
      label: '正在应用设置',
      tone: 'accent',
      hint: `最新设置已保存，正在等待${host}应用。`,
    }
  }
  return {
    key: 'synced',
    label: '已同步',
    tone: 'ok',
    hint: `${host}已应用当前设置。`,
  }
}

/** 实例状态 → 展示语义。两套后端事实源：
 *  - edge pluginhost.State：规范大写（STOPPED/HEALTHY…）；
 *  - server AppHost（appruntime.InstanceState）：小写（running/stopping…，见 internal/appruntime/types.go）。
 *  两套都在同一 observed 投影里，词汇必须都覆盖，否则服务器托管实例会露出机器串。
 *  未知值原样呈现，不猜含义。 */
const STATE_META: Record<string, { label: string; tone: Tone }> = {
  STOPPED: { label: '已停止', tone: 'idle' },
  STARTING: { label: '启动中', tone: 'accent' },
  HEALTHY: { label: '运行中', tone: 'ok' },
  DEGRADED: { label: '降级', tone: 'warn' },
  CRASHED: { label: '已崩溃', tone: 'bad' },
  BACKOFF: { label: '重启退避', tone: 'warn' },
  DISABLED: { label: '已禁用', tone: 'idle' },
  degraded: { label: '运行异常', tone: 'warn' },
  crashed: { label: '已中断', tone: 'bad' },
  backoff: { label: '正在重试', tone: 'warn' },
  disabled: { label: '已停用', tone: 'idle' },
  created: { label: '已创建', tone: 'idle' },
  starting: { label: '启动中', tone: 'accent' },
  running: { label: '运行中', tone: 'ok' },
  stopping: { label: '停止中', tone: 'accent' },
  stopped: { label: '已停止', tone: 'idle' },
  failed: { label: '启动失败', tone: 'bad' },
}

/** observed.detail 的已知机器标记 → 人话（其余是 server 脱敏摘要，原样呈现） */
const HOST_DETAIL_LABEL: Record<string, string> = { 'server-apphost': '中心服务' }
export function hostDetailLabel(detail?: string): string | undefined {
  if (!detail) return undefined
  return HOST_DETAIL_LABEL[detail] ?? detail
}

/** pluginhost.Health 的规范大写名 → 展示语义 */
const HEALTH_META: Record<string, { label: string; tone: Tone }> = {
  HEALTHY: { label: '健康', tone: 'ok' },
  DEGRADED: { label: '降级', tone: 'warn' },
  UNHEALTHY: { label: '异常', tone: 'bad' },
  UNKNOWN: { label: '未知', tone: 'idle' },
  healthy: { label: '健康', tone: 'ok' },
  degraded: { label: '降级', tone: 'warn' },
  unhealthy: { label: '异常', tone: 'bad' },
  unknown: { label: '未知', tone: 'idle' },
}

export function stateMeta(state: string | undefined): { label: string; tone: Tone } {
  if (!state) return { label: '未上报', tone: 'idle' }
  return STATE_META[state] ?? { label: '状态待确认', tone: 'idle' }
}

export function healthMeta(health: string | undefined): { label: string; tone: Tone } {
  if (!health) return { label: '未上报', tone: 'idle' }
  return HEALTH_META[health] ?? { label: '状态待确认', tone: 'idle' }
}

export function instanceLocationLabel(v: PluginInstanceView): string {
  if (v.edge_id === 'server') return '中心服务'
  return `网关 ${v.edge_id || '未知'}${v.edge_online ? '' : '（离线）'}`
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
  const host = serverHosted ? '中心服务' : '网关'
  const state = stateMeta(v.observed?.state)
  const health = healthMeta(v.observed?.health)

  if (!v.has_observed) {
    const hostOffline = !serverHosted && v.desired.enabled && !v.edge_online
    return {
      key: hostOffline ? 'attention' : 'unknown', label: hostOffline ? '等待网关连接' : '状态待确认',
      tone: hostOffline ? 'warn' : 'idle',
      summary: hostOffline ? '网关离线，还没有收到运行状态' : `还没有收到${host}的运行状态`,
      next: serverHosted
        ? '稍后刷新；如果一直没有状态，请查看运行记录。'
        : hostOffline
          ? '先恢复网关连接，连接后会自动更新。'
          : '稍后刷新；如果一直没有状态，请重新应用设置。',
      needsAttention: hostOffline, priority: hostOffline ? 0 : 1,
    }
  }

  if (v.stale) {
    return {
      key: 'attention', label: '状态可能已过期', tone: 'warn',
      summary: `上次状态：${state.label}`,
      next: v.edge_id !== 'server' && !v.edge_online
        ? '先恢复网关连接，等待最新状态；确认连接后再重新应用设置。'
        : '等待最新状态；如果长时间没有更新，请重新应用设置。',
      needsAttention: true, priority: 0,
    }
  }

  if (v.drift || (!v.desired.enabled && state.tone === 'ok')) {
    const healthCopy = v.observed?.health && health.tone !== 'ok' && health.tone !== 'idle'
      ? ` · 健康${health.label}` : ''
    return {
      key: 'attention', label: '最新设置尚未生效', tone: 'warn',
      summary: !v.desired.enabled && state.tone === 'ok'
        ? '当前仍在运行' : `当前：${state.label}${healthCopy}`,
      next: '打开详情核对设置，再点击「重新应用设置」。',
      needsAttention: true, priority: 0,
    }
  }

  if (state.label === '已停止' || state.label === '已停用' || state.label === '已禁用') {
    return {
      key: 'stopped', label: '已停止', tone: 'idle',
      summary: '当前没有运行',
      next: v.desired.enabled ? '如果应该运行，请重新应用设置。' : undefined,
      needsAttention: false, priority: 2,
    }
  }

  const abnormal = state.tone === 'bad' || state.tone === 'warn'
    || health.tone === 'bad' || health.tone === 'warn'
  if (abnormal) {
    const healthCopy = v.observed?.health && health.label !== '未上报' ? ` · 健康${health.label}` : ''
    return {
      key: 'attention', label: '需要处理', tone: state.tone === 'bad' || health.tone === 'bad' ? 'bad' : 'warn',
      summary: `当前：${state.label}${healthCopy}`,
      next: '查看运行记录或重新应用设置；如果持续异常，请联系管理员。',
      needsAttention: true, priority: 0,
    }
  }

  if (state.tone === 'ok') {
    return {
      key: 'normal', label: '运行正常', tone: 'ok',
      summary: `当前：${state.label}`,
      needsAttention: false, priority: 3,
    }
  }

  return {
    key: 'unknown', label: '状态待确认', tone: 'idle',
    summary: `当前：${state.label}`,
    next: '打开详情查看最近更新；如果状态一直没有变化，请重新应用设置。',
    needsAttention: false, priority: 1,
  }
}

/** 目录里的 observed_state 在 server 侧未观测时是小写 unknown（plugincatalog 约定） */
export function trustMeta(mode: string | undefined, verified: boolean): { label: string; tone: Tone } {
  if (verified) return { label: mode ? `已验证 · ${mode}` : '已验证', tone: 'ok' }
  return { label: mode ? `未验证 · ${mode}` : '未验证', tone: 'warn' }
}

export const ISOLATION_LABELS: Record<string, string> = {
  shared: '共享运行',
  'per-instance': '独立运行',
  none: '不隔离',
  process: '独立运行',
  container: '独立运行',
}

export function isolationLabel(isolation: string | undefined): string {
  if (!isolation) return '未指定'
  return ISOLATION_LABELS[isolation] ?? isolation
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
const PERM_GROUPS: { key: keyof PluginPermissionsData; group: string; tone: Tone }[] = [
  { key: 'hardware', group: '硬件', tone: 'warn' },
  { key: 'network', group: '网络', tone: 'warn' },
  { key: 'filesystem', group: '文件系统', tone: 'warn' },
  { key: 'secrets', group: '密钥', tone: 'bad' },
]

/** 只列出**声明了**的权限组；未声明的组不出现（不塞「无」占位，避免满屏 badge） */
export function permissionGroups(p: PluginPermissionsData | undefined): PermissionGroup[] {
  if (!p) return []
  const out: PermissionGroup[] = []
  for (const g of PERM_GROUPS) {
    const items = p[g.key] ?? []
    if (items.length > 0) out.push({ ...g, items })
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
  return (m?.[1] ?? ref).trim() || '（空 handle）'
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
/** 插件在界面上的名称：优先使用插件提供的功能标题，机器标识只留在技术详情。 */
export function pluginDisplayName(catalog?: PluginCatalogView): string {
  const contributions = [
    ...(catalog?.contributes?.drivers ?? []),
    ...(catalog?.contributes?.applications ?? []),
    ...(catalog?.contributes?.connectors ?? []),
  ]
  const title = contributions.find((x) => x.title?.trim())?.title?.trim()
  return title || catalog?.id || '插件信息未提供'
}

const PERMISSION_ITEM_LABELS: Record<string, string> = {
  'hardware:uart': '访问串口',
  'hardware:gpio': '控制输入输出端口',
  'hardware:i2c': '访问 I2C 设备',
  'hardware:spi': '访问 SPI 设备',
  'hardware:usb': '访问 USB 设备',
  'network:outbound': '访问网络',
  'network:inbound': '接受网络连接',
  'network:http': '访问网页服务',
  'filesystem:read': '读取文件',
  'filesystem:write': '写入文件',
}

/** 权限项的人话标签；未知项原样保留，避免猜业务含义。 */
export function permissionItemLabel(group: keyof PluginPermissionsData, item: string): string {
  if (group === 'secrets') return `使用密钥 ${item}`
  return PERMISSION_ITEM_LABELS[`${group}:${item}`] ?? item
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

/** GET /api/plugins 的宽容归一化 */
export function normalizeCatalog(raw: unknown): PluginCatalogView[] {
  const list = Array.isArray(raw)
    ? raw
    : (raw && typeof raw === 'object' ? (raw as Record<string, unknown>).plugins : undefined)
  if (!Array.isArray(list)) return []
  const out: PluginCatalogView[] = []
  for (const item of list) {
    if (!item || typeof item !== 'object') continue
    const o = item as Record<string, unknown>
    if (!str(o.id)) continue
    out.push({
      ...(o as unknown as PluginCatalogView),
      kind: normalizePluginKind(str(o.kind)),
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

/** 按 Edge 归组实例（「已安装」分区用：哪台 Edge 上跑着什么） */
export function groupByEdge(instances: PluginInstanceView[]): Map<string, PluginInstanceView[]> {
  const out = new Map<string, PluginInstanceView[]>()
  for (const v of instances) {
    const arr = out.get(v.edge_id)
    if (arr) arr.push(v)
    else out.set(v.edge_id, [v])
  }
  return out
}
