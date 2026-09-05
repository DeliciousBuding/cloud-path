// 展示格式化工具（纯函数，无副作用）。
//
// 设备语义不在这里：事件/命令的展示文案由后端声明驱动（Capability spec.events / spec.actions），
// 未声明时回落 humanize(机器名)。机器 ID、Capability ID、事件类型永不本地化
// （docs/architecture/capability-model.md §9）。
import { ApiError } from './api'
import type { Tone } from '@/components/ui'
import { capabilityLabel, commandDecl, commandLabel, eventDecl, humanize } from './descriptor'
import type { CapabilityIndex, CommandAction } from './descriptor'
import type { EventView } from './types'

export function fmtTime(ts: number): string {
  return new Date(ts * 1000).toLocaleTimeString('zh-CN', { hour12: false })
}

/** 时分刻度（HH:MM）：分桶时间轴用，秒级细节对 ≥ 1 分钟的桶是噪音 */
export function fmtHourMin(ts: number): string {
  return new Date(ts * 1000).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' })
}

export function fmtDateTime(ts: number): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false })
}

/**
 * 时间线 day 组头：今天 / 昨天 / 「9月5日」/ 跨年补年份。
 * 长列表按天分组后才有扫读锚点，否则几十行同构细线流等于没有结构。
 */
export function fmtDay(ts: number): string {
  const d = new Date(ts * 1000)
  const now = new Date()
  const dayMs = 86_400_000
  const startOf = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const diff = Math.round((startOf(now) - startOf(d)) / dayMs)
  if (diff === 0) return '今天'
  if (diff === 1) return '昨天'
  const sameYear = d.getFullYear() === now.getFullYear()
  return d.toLocaleDateString('zh-CN', sameYear
    ? { month: 'long', day: 'numeric' }
    : { year: 'numeric', month: 'long', day: 'numeric' })
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
      const rec = o as Record<string, unknown>
      for (const k of ['label', 'message', 'reason', 'text']) {
        const v = rec[k]
        if (typeof v === 'string' && v.length > 0) return v
      }
    }
  } catch { /* 载荷不是 JSON：交由上层按类型名展示 */ }
  return undefined
}

/** 事件动词平台词典（声明缺席时的回退层）：机器动词 → 中文；未知动词回落 humanize，不猜业务语义 */
const EVENT_VERB: Record<string, string> = {
  press: '按下', pressed: '按下', release: '释放', released: '释放',
  quake: '振动', changed: '状态变化', close: '靠近', away: '离开',
  direction: '方向变化', tick: '滴答', opened: '打开', closed: '关闭',
  taken: '已取药', remind: '提醒', missed: '错过',
}

/** 脏标签判定：历史脏数据（二进制串口碎片被写成事件类型）含控制符/替换符，原样展示即乱码 */
export function isDirtyLabel(s: string): boolean {
  return /[\uFFFD\u0000-\u0008\u000B\u000C\u000E-\u001F]/.test(s)
}

/** 机器事件类型 → 中文组合名：`<capref>@n/<verb>`、`<capref>@n/<dir>:<verb>` → `能力 · 动词` */
function composeEventLabel(type: string, index?: CapabilityIndex): string {
  const m = type.match(/^(.*@\d+)(?:\/(.+))?$/)
  if (!m) return humanize(type)
  const cap = capabilityLabel(m[1], index)
  const rest = m[2]
  if (!rest) return cap
  const dir = rest.match(/^(\d+):(.+)$/)
  const verb = dir ? dir[2] : rest
  const verbLabel = EVENT_VERB[verb] ?? humanize(verb)
  return dir ? `${cap} · 方向${dir[1]}${verbLabel}` : `${cap} · ${verbLabel}`
}

/** 平台级事件类型词汇（device.* 是平台生命周期事件，非设备语义） */
const EVENT_TYPE_LABEL: Record<string, string> = {
  'device.boot': '设备启动', 'device-booted': '设备启动',
  'device.online': '设备上线', 'device-online': '设备上线',
  'device.offline': '设备离线', 'device-offline': '设备离线',
  'device.state': '状态上报', 'device-state': '状态上报',
  'device.descriptor': '描述更新', 'device-descriptor': '描述更新',
}

/** 事件展示名：后端 label > 脏数据降级 > 平台事件词汇 > Capability 声明 title > 组合中文名 > humanize */
export function eventLabel(type: string, index?: CapabilityIndex, label?: string): string {
  if (label && !isDirtyLabel(label)) return label
  if (isDirtyLabel(type)) return '无效事件（历史脏数据）'
  return EVENT_TYPE_LABEL[type]
    || (index ? eventDecl(type, index)?.title : undefined)
    || composeEventLabel(type, index)
}

/** 事件语义色：只采纳 Capability 声明的 tone；未声明一律中性，不猜业务含义 */
export function eventTone(type: string, index?: CapabilityIndex): Tone {
  return (index ? eventDecl(type, index)?.tone : undefined) ?? 'idle'
}

/** 命令展示名/提示，回落顺序：设备命令集声明 > catalog 里的 action 声明 > 平台词典 > humanize(cmd)。
 *  跨设备列表（活动页 / 概览）没有单设备命令集，传 idx 让它照样吃到声明标题。 */
export function cmdMeta(
  cmd: string, actions?: CommandAction[], idx?: CapabilityIndex,
): { label: string; hint: string } {
  const a = actions?.find((x) => x.cmd === cmd)
  if (a) return { label: a.label, hint: a.hint ?? '' }
  const decl = idx ? commandDecl(cmd, idx) : undefined
  if (decl?.title) return { label: decl.title, hint: decl.description ?? '' }
  return { label: commandLabel(cmd), hint: '' }
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
 * 参数校验（Vercel field 纪律：保留用户原文，不静默剥离/截断，错在框下显式说）。
 *  返回人话错误；合法返回 undefined。后端契约：不含换行/NUL、长度上限（docs/api.md）。 */
export function argsError(args: string, max = 64): string | undefined {
  if (/[\r\n\0]/.test(args)) return '参数不能包含换行或控制字符'
  if (args.length > max) return `参数 ${args.length} 字符，超过 ${max} 字符上限`
  return undefined
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

/** 桶宽候选（秒）：从数据跨度自动选，保证 ≤ want 个桶且桶宽是人话单位 */
const DENSITY_STEPS = [60, 300, 900, 1800, 3600, 7200, 14400, 43200, 86400]

function stepLabel(sec: number): string {
  if (sec < 3600) return sec === 60 ? '分钟' : `${sec / 60} 分钟`
  if (sec < 86400) return sec === 3600 ? '小时' : `${sec / 3600} 小时`
  return sec === 86400 ? '天' : `${sec / 86400} 天`
}

/**
 * 事件密度分桶（纯函数）：窗口 = 最早事件 → 现在，桶宽按跨度自动取人话单位；peak 供标题说人话。
 * 不承诺数据没覆盖的区间（例如硬说「近 24 小时」），窗口起点由调用方如实标注。
 * 少于两条事件返回 null（画不出分布，不画假图）。
 */
export function bucketEventDensity(
  tsList: number[], nowSec: number, want = 24,
): { points: { t: number; v: number }[]; stepSec: number; label: string; peak: number } | null {
  if (tsList.length < 2) return null
  const min = Math.min(...tsList)
  const span = Math.max(3600, nowSec - min)
  const step = DENSITY_STEPS.find((x) => span / x <= want) ?? 86400
  const start = Math.floor(min / step) * step
  const n = Math.max(2, Math.ceil((nowSec - start) / step) + 1)
  const base = Math.floor(start / step)
  const counts = new Array<number>(n).fill(0)
  for (const ts of tsList) {
    const i = Math.floor(ts / step) - base
    if (i >= 0 && i < n) counts[i] += 1
  }
  const peak = counts.reduce((a, b) => Math.max(a, b), 0)
  return { points: counts.map((v, i) => ({ t: start + i * step, v })), stepSec: step, label: stepLabel(step), peak }
}
