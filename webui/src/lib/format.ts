// 展示格式化工具（纯函数，无副作用）。
//
// 设备语义不在这里：事件/命令的展示文案由后端声明驱动（Capability spec.events / spec.actions），
// 未声明时回落 humanize(机器名)。机器 ID、Capability ID、事件类型永不本地化
// （docs/architecture/capability-model.md §9）。
import { ApiError } from './api'
import type { Tone } from '@/components/ui'
import { eventDecl, humanize } from './descriptor'
import type { CapabilityIndex, CommandAction } from './descriptor'
import type { EventView } from './types'

export function fmtTime(ts: number): string {
  return new Date(ts * 1000).toLocaleTimeString('zh-CN', { hour12: false })
}

export function fmtDateTime(ts: number): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false })
}

export function timeAgo(ts: number): string {
  if (!ts) return '—'
  const s = Math.max(0, Math.floor(Date.now() / 1000 - ts))
  if (s < 5) return '刚刚'
  if (s < 60) return `${s} 秒前`
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`
  return `${Math.floor(s / 86400)} 天前`
}

export function fmtUptime(sec: number): string {
  if (sec < 60) return `${sec} 秒`
  if (sec < 3600) return `${Math.floor(sec / 60)} 分钟`
  if (sec < 86400) return `${Math.floor(sec / 3600)} 小时 ${Math.floor((sec % 3600) / 60)} 分`
  return `${Math.floor(sec / 86400)} 天 ${Math.floor((sec % 86400) / 3600)} 小时`
}

/** 事件载荷里后端给的展示标签（WS EventData.label / REST payload.label），没有则 undefined */
export function payloadLabel(payload: string | undefined): string | undefined {
  if (!payload) return undefined
  try {
    const o = JSON.parse(payload) as unknown
    if (o && typeof o === 'object' && !Array.isArray(o)) {
      const v = (o as Record<string, unknown>).label
      if (typeof v === 'string' && v.length > 0) return v
    }
  } catch { /* 载荷不是 JSON：交由上层按类型名展示 */ }
  return undefined
}

/** 事件展示名：后端 label > Capability events 声明的 title > humanize(类型名) */
export function eventLabel(type: string, index?: CapabilityIndex, label?: string): string {
  return label || (index ? eventDecl(type, index)?.title : undefined) || humanize(type)
}

/** 事件语义色：只采纳 Capability 声明的 tone；未声明一律中性，不猜业务含义 */
export function eventTone(type: string, index?: CapabilityIndex): Tone {
  return (index ? eventDecl(type, index)?.tone : undefined) ?? 'idle'
}

/** 命令展示名/提示：命令集声明（CommandAction）优先，回落 humanize(cmd) */
export function cmdMeta(cmd: string, actions?: CommandAction[]): { label: string; hint: string } {
  const a = actions?.find((x) => x.cmd === cmd)
  if (a) return { label: a.label, hint: a.hint ?? '' }
  return { label: humanize(cmd), hint: '' }
}

/** 命令生命周期状态 → 徽标语义（平台级状态机，非设备语义） */
export const CMD_STATUS_META: Record<string, { label: string; tone: Tone }> = {
  pending: { label: '待发送', tone: 'idle' },
  sent:    { label: '已下发', tone: 'accent' },
  ok:      { label: '成功',   tone: 'ok' },
  failed:  { label: '失败',   tone: 'bad' },
  timeout: { label: '超时',   tone: 'warn' },
}

export function cmdStatusMeta(status: string) {
  return CMD_STATUS_META[status] ?? { label: status, tone: 'idle' as Tone }
}

/** 事件流合并去重：WS 实时事件（负 id）与 REST 历史（正 id）按 设备+时间+类型 归并 */
export function mergeEvents(live: EventView[], history: EventView[]): EventView[] {
  const seen = new Set<string>()
  const out: EventView[] = []
  for (const e of [...live, ...history]) {
    const k = `${e.device_id}:${e.ts}:${e.type}`
    if (seen.has(k)) continue
    seen.add(k)
    out.push(e)
  }
  return out.sort((a, b) => b.ts - a.ts || b.id - a.id)
}

/** 用户角色 → 中文标签（docs/api.md §2.1 role ∈ admin|operator|viewer；未知角色回落原名） */
const ROLE_LABELS: Record<string, string> = {
  admin: '管理员',
  operator: '操作员',
  viewer: '只读',
}

export function roleLabel(role: string): string {
  return ROLE_LABELS[role] ?? role
}

/**
 * 下拉候选等窄容器里的标签截断。
 * 原生 <option> 不受 CSS truncate 约束（下拉弹层宽度也不受父容器限制），
 * 因此后端给的长标识符只能在文本层收敛，否则 390px 上选择器会被撑宽、弹层不可读。
 */
export function optionLabel(s: string, max = 32): string {
  const v = String(s ?? '')
  return v.length > max ? `${v.slice(0, max)}…` : v
}

/**
 * 命令下发失败 → 人话。按 HTTP 状态判定，语义对齐 docs/design.md 的 REST 错误约定
 * （400 参数/白名单、401 令牌、404 设备不存在、409 edge 离线、429 命令限流、
 * 503 存储不可用或 edge 队列满）；不把服务端 message 当规则复述。
 */
export function commandErrorCopy(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.status) {
      case 400: return '命令或参数不被接受：不在适配器白名单内，或参数超长 / 含控制字符'
      case 401: return '登录已失效，请重新登录后再下发命令'
      case 403: return '权限不足：当前角色不能下发命令（需要 operator 或 admin）'
      case 404: return '设备不存在，或不属于当前租户'
      case 409: return '目标设备所在的边缘节点离线，命令无法下发'
      case 429: return e.retryAfter ? `下发过于频繁，请 ${e.retryAfter} 秒后重试` : '下发过于频繁，请稍后重试'
      case 503: return '服务端存储不可用或边缘队列已满，请稍后重试'
      default: return `下发失败（HTTP ${e.status}）`
    }
  }
  return e instanceof Error && e.message ? e.message : '无法连接 server（服务未启动或网络不可达）'
}