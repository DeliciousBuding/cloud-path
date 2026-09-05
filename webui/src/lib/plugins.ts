// 插件控制面的展示逻辑（纯函数、无副作用、无 React）。
//
// 三条硬约束（docs/architecture/control-plane-sync.md 不变量 5/6，任务书 §6.5）：
//   ① desired 与 observed **永远分别呈现**：desired.enabled 绝不可渲染成「运行中/健康」。
//   ② `has_observed=false` → 显式「Edge 未上报」；`stale` / `drift` 各有独立视觉状态。
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

export interface PluginErrorCopy {
  /** 一行标题（说清「发生了什么」） */
  title: string
  /** 一句可执行的下一步（说清「用户能做什么」） */
  hint: string
  tone: Tone
  /** 命中的稳定码；undefined = 服务端未给码（只报状态，不猜业务规则） */
  code?: PluginErrCode
  /** 该错误是否要求「显式确认权限扩大」后重试（对应 confirm_permissions） */
  needsPermissionConfirm: boolean
  /** 是否可原样重试（同一 payload） */
  retryable: boolean
}

const ERR_COPY: Record<PluginErrCode, Omit<PluginErrorCopy, 'code'>> = {
  [PluginErr.NotFound]: {
    title: '实例不存在',
    hint: '该插件实例可能已被删除，或不属于当前租户。刷新列表后重试。',
    tone: 'idle', needsPermissionConfirm: false, retryable: false,
  },
  [PluginErr.Conflict]: {
    title: '实例已存在或版本冲突',
    hint: '同一边缘节点上已有同名实例，或期望态版本与当前记录冲突。改用另一个实例 ID，或先更新既有实例。',
    tone: 'warn', needsPermissionConfirm: false, retryable: false,
  },
  [PluginErr.Quota]: {
    title: '超出租户配额',
    hint: '当前租户的插件实例数已达上限，本次写入未生效（未产生新 revision）。请先删除不用的实例，或联系管理员调整配额。',
    tone: 'warn', needsPermissionConfirm: false, retryable: false,
  },
  [PluginErr.PermissionConfirm]: {
    title: '需要确认权限扩大',
    hint: '本次变更会授予插件更多权限。请在下方逐项核对新增权限后显式勾选确认，再重新提交。',
    tone: 'warn', needsPermissionConfirm: true, retryable: true,
  },
  [PluginErr.EdgeOffline]: {
    title: '目标边缘节点离线',
    hint: '期望态已可写入，但该边缘节点当前不在线，无法立即应用。边缘节点重连后会自动收敛到最新快照。',
    tone: 'warn', needsPermissionConfirm: false, retryable: true,
  },
  [PluginErr.SecretForbidden]: {
    title: 'Secret handle 不可用',
    hint: '引用的 secret handle 未授权给本租户或已吊销。请改用已授权的 handle 名（UI 只显示 handle，不显示明文）。',
    tone: 'bad', needsPermissionConfirm: false, retryable: false,
  },
  [PluginErr.InvalidConfig]: {
    title: '配置不合法',
    hint: '配置项未通过服务端校验（键名、长度或取值范围）。请修正后重新提交。',
    tone: 'bad', needsPermissionConfirm: false, retryable: false,
  },
}

const KNOWN = new Set<string>(Object.values(PluginErr))

/**
 * 把任意写操作异常映射成可呈现文案。
 * 只认 `ApiError.code`（服务端稳定码）；无码时按 HTTP 状态给**通用**说明，
 * 绝不把服务端 message 当规则复述，也不做自然语言解析。
 */
export function pluginErrorCopy(e: unknown): PluginErrorCopy {
  if (e instanceof ApiError && e.code && KNOWN.has(e.code)) {
    return { ...ERR_COPY[e.code as PluginErrCode], code: e.code as PluginErrCode }
  }
  if (e instanceof ApiError) {
    if (e.status === 401) {
      return {
        title: '登录已失效', hint: '请重新登录后再操作插件实例。',
        tone: 'warn', needsPermissionConfirm: false, retryable: true,
      }
    }
    if (e.status === 403) {
      return {
        title: '权限不足',
        hint: '当前角色不能修改插件实例（需要 operator 或 admin）。可继续查看期望态与实际态。',
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
      title: `请求失败（HTTP ${e.status}）`,
      hint: '服务端拒绝了本次写入，期望态未改变。稍后重试；若持续失败请查看服务端日志。',
      tone: 'bad', needsPermissionConfirm: false, retryable: true,
    }
  }
  return {
    title: '无法连接 server',
    hint: '网络不可达或服务未启动。本次写入未提交，期望态保持不变。',
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
  const host = serverHosted ? '应用宿主' : '边缘节点'
  if (!v.has_observed) {
    return {
      key: 'unreported',
      label: host + '未上报',
      tone: 'idle',
      hint: serverHosted
        ? '应用宿主尚未上报实际态。启用只是期望，当前是否运行以应用数据区为准。'
        : v.edge_online
        ? '期望态已下发，但该边缘节点还没有回过实际态。不能据此判断插件是否在运行。'
        : '边缘节点离线，尚未回过实际态。边缘节点重连并应用快照后这里才会出现运行事实。',
    }
  }
  if (v.stale) {
    return {
      key: 'stale',
      label: '实际态已过期',
      tone: 'warn',
      hint: host + '的上报已超过新鲜期，下面的实际态是历史事实，不代表当前运行状况。',
    }
  }
  if (v.drift) {
    return {
      key: 'drift',
      label: '期望与实际不一致',
      tone: 'warn',
      hint: `期望修订版 ${v.desired_revision}，${host}已应用 ${v.applied_revision}。可触发一次重新下发让${host}重新收敛。`,
    }
  }
  if (v.applied_revision < v.desired_revision) {
    return {
      key: 'pending',
      label: '等待' + host + '应用',
      tone: 'accent',
      hint: `期望修订版 ${v.desired_revision} 已提交，${host}当前应用到 ${v.applied_revision}`,
    }
  }
  return {
    key: 'synced',
    label: '已收敛',
    tone: 'ok',
    hint: `${host}已应用期望修订版 ${v.applied_revision}`,
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
  created: { label: '已创建', tone: 'idle' },
  starting: { label: '启动中', tone: 'accent' },
  running: { label: '运行中', tone: 'ok' },
  stopping: { label: '停止中', tone: 'accent' },
  stopped: { label: '已停止', tone: 'idle' },
  failed: { label: '启动失败', tone: 'bad' },
}

/** observed.detail 的已知机器标记 → 人话（其余是 server 脱敏摘要，原样呈现） */
const HOST_DETAIL_LABEL: Record<string, string> = { 'server-apphost': '中心服务应用宿主' }
export function hostDetailLabel(detail?: string): string | undefined {
  if (!detail) return undefined
  return HOST_DETAIL_LABEL[detail] ?? detail
}

/** pluginhost.Health 的规范大写名 → 展示语义 */
const HEALTH_META: Record<string, { label: string; tone: Tone }> = {
  HEALTHY: { label: '健康', tone: 'ok' },
  DEGRADED: { label: '降级', tone: 'warn' },
  UNKNOWN: { label: '未知', tone: 'idle' },
}

export function stateMeta(state: string | undefined): { label: string; tone: Tone } {
  if (!state) return { label: '未上报', tone: 'idle' }
  return STATE_META[state] ?? { label: state, tone: 'idle' }
}

export function healthMeta(health: string | undefined): { label: string; tone: Tone } {
  if (!health) return { label: '未上报', tone: 'idle' }
  return HEALTH_META[health] ?? { label: health, tone: 'idle' }
}

/** 目录里的 observed_state 在 server 侧未观测时是小写 unknown（plugincatalog 约定） */
export function trustMeta(mode: string | undefined, verified: boolean): { label: string; tone: Tone } {
  if (verified) return { label: mode ? `已验证 · ${mode}` : '已验证', tone: 'ok' }
  return { label: mode ? `未验证 · ${mode}` : '未验证', tone: 'warn' }
}

export const ISOLATION_LABELS: Record<string, string> = {
  shared: '共享进程',
  'per-instance': '实例独立进程',
  none: '无隔离',
  process: '独立进程',
  container: '容器',
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
  { key: 'secrets', group: 'Secret', tone: 'bad' },
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
  const d = (o.desired && typeof o.desired === 'object' ? o.desired : {}) as Record<string, unknown>
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
    out.push(o as unknown as PluginCatalogView)
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