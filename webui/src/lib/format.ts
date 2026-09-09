// 展示格式化工具（纯函数，无副作用）。
//
// 设备语义不在这里：事件/操作的展示文案由后端声明驱动（Capability spec.events / spec.actions），
// 未声明时回落 humanize(机器名)。机器 ID、Capability ID、事件类型永不本地化
// （docs/architecture/capability-model.md §9）。
import { ApiError } from './api'
import { currentLocale, i18n } from '@/i18n'
import type { Tone } from '@/components/ui'
import { capabilityLabel, commandDecl, commandLabel, eventDecl, humanize } from './descriptor'
import type { CapabilityIndex, CommandAction } from './descriptor'
import type { EventView } from './types'

export function fmtTime(ts: number): string {
  return new Intl.DateTimeFormat(currentLocale(), {
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(new Date(ts * 1000))
}

export function fmtDateTime(ts: number): string {
  if (!ts) return '—'
  return new Intl.DateTimeFormat(currentLocale(), {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(new Date(ts * 1000))
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
  if (diff === 0) return i18n.t('time.today', { ns: 'common' })
  if (diff === 1) return i18n.t('time.yesterday', { ns: 'common' })
  const sameYear = d.getFullYear() === now.getFullYear()
  return new Intl.DateTimeFormat(currentLocale(), sameYear
    ? { month: 'long', day: 'numeric' }
    : { year: 'numeric', month: 'long', day: 'numeric' }).format(d)
}

export function timeAgo(ts: number): string {
  if (!ts) return '—'
  const s = Math.max(0, Math.floor(Date.now() / 1000 - ts))
  if (s < 5) return i18n.t('time.justNow', { ns: 'common' })
  if (s < 60) return i18n.t('time.secondsAgo', { ns: 'common', count: s })
  if (s < 3600) return i18n.t('time.minutesAgo', { ns: 'common', count: Math.floor(s / 60) })
  if (s < 86400) return i18n.t('time.hoursAgo', { ns: 'common', count: Math.floor(s / 3600) })
  return i18n.t('time.daysAgo', { ns: 'common', count: Math.floor(s / 86400) })
}

export function fmtUptime(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) return '—'
  if (sec < 60) return i18n.t('time.seconds', { ns: 'common', count: sec })
  if (sec < 3600) return i18n.t('time.minutes', { ns: 'common', count: Math.floor(sec / 60) })
  if (sec < 86400) return i18n.t('time.hoursMinutes', {
    ns: 'common', hours: Math.floor(sec / 3600), minutes: Math.floor((sec % 3600) / 60),
  })
  return i18n.t('time.daysHours', {
    ns: 'common', days: Math.floor(sec / 86400), hours: Math.floor((sec % 86400) / 3600),
  })
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

/**
 * 载荷相对**行内已展示内容**是否还有增量信息。
 *
 * 事件行已经把 `type` 渲染成徽标，把 `label/message/reason/text` 之一渲染成行内摘要，
 * 所以只剩这些键的载荷展开后是零增量。真实数据里这恰好是最常见的形状——
 * `{"type":"device-booted"}` 展开就是它自己，「展开原始载荷」于是变成一个骗点击的按钮。
 *
 * 判据刻意只排除 `type`：其余键一律算增量，包括已被摘出来的 `label` 等——原始 JSON 是
 * 取证面，展示层不该替用户决定哪个键「已经看过了」。解析不了也不是 JSON 的载荷一律
 * 保留展开（原文本身就是证据）。
 */
export function payloadHasMore(payload: string | undefined): boolean {
  if (!payload) return false
  let parsed: unknown
  try {
    parsed = JSON.parse(payload)
  } catch {
    return true // 不是 JSON：原文即取证材料，保留展开入口
  }
  if (Array.isArray(parsed)) return parsed.length > 0
  if (!parsed || typeof parsed !== 'object') return true // 裸标量：行内没有对应展示位
  return Object.keys(parsed as Record<string, unknown>).some((k) => k !== 'type')
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

/** 操作展示名/提示，回落顺序：设备操作集声明 > catalog 里的 action 声明 > 平台词典 > humanize(cmd)。
 *  跨设备列表（活动页 / 概览）没有单设备操作集，传 idx 让它照样吃到声明标题。 */
export function cmdMeta(
  cmd: string, actions?: CommandAction[], idx?: CapabilityIndex,
): { label: string; hint: string } {
  const a = actions?.find((x) => x.cmd === cmd)
  if (a) return { label: a.label, hint: a.hint ?? '' }
  const decl = idx ? commandDecl(cmd, idx) : undefined
  if (decl?.title) return { label: decl.title, hint: decl.description ?? '' }
  const friendly: Record<string, string> = {
    tone: '播放音调',
    tone_sequence: '播放音序',
    isp: '进入下载模式',
  }
  return { label: friendly[cmd] ?? commandLabel(cmd), hint: '' }
}

/** 操作生命周期状态 → 徽标语义（平台级状态机，非设备语义） */
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
export function roleLabel(role: string): string {
  return i18n.t(`roles.${role}`, { ns: 'common', defaultValue: role })
}

/**
 * 鉴权形态 → 人话（docs/api.md §1）。这里说的是 server **实际执行**的鉴权，
 * 不是「有没有配 legacy 令牌」：账号模式下必须显示为需登录，否则系统页会把一个
 * 已收紧的部署说成裸奔。未知形态回落原值，不猜语义——与 roleLabel 同一纪律。
 */
export function authModeLabel(mode?: string): string {
  if (!mode) return '—'
  return i18n.t(`authModes.${mode}`, { ns: 'common', defaultValue: mode })
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

/** 后端 len(args) 按 UTF-8 字节计数；声明只能收紧，不能放宽传输上限。 */
export function argsMaxBytes(declared?: number): number {
  return typeof declared === 'number' && Number.isFinite(declared) && declared >= 0
    ? Math.min(64, Math.floor(declared)) : 64
}

/** 保留原文，不静默剥离、截断或压缩 JSON；与服务端的换行/NUL 门禁一致。 */
export function argsError(args: string, max = 64): string | undefined {
  if (/[\r\n\0]/.test(args)) return i18n.t('validation.argsControl', { ns: 'common' })
  const bytes = new TextEncoder().encode(args).length
  const limit = argsMaxBytes(max)
  if (bytes > limit) return i18n.t('validation.argsTooLong', { ns: 'common', bytes, limit })
  return undefined
}

/**
 * 操作下发失败 → 人话。按 HTTP 状态判定，语义对齐 docs/design.md 的 REST 错误约定
 * （400 参数/白名单、401 令牌、404 设备不存在、409 edge 离线、429 操作限流、
 * 503 存储不可用或 edge 队列满）；不把服务端 message 当规则复述。
 */
export function commandErrorCopy(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.status) {
      case 400: return i18n.t('command.badRequest', { ns: 'errors' })
      case 401: return i18n.t('command.unauthorized', { ns: 'errors' })
      case 403: return i18n.t('command.forbidden', { ns: 'errors' })
      case 404: return i18n.t('command.notFound', { ns: 'errors' })
      case 409: return i18n.t('command.offline', { ns: 'errors' })
      case 429: return e.retryAfter
        ? i18n.t('command.rateLimitedAfter', { ns: 'errors', seconds: e.retryAfter })
        : i18n.t('command.rateLimited', { ns: 'errors' })
      case 503: return i18n.t('command.unavailable', { ns: 'errors' })
      default: return i18n.t('command.failed', { ns: 'errors', status: e.status })
    }
  }
  return e instanceof Error && e.message ? e.message : i18n.t('network', { ns: 'errors' })
}

/** 桶宽候选（秒）：从数据跨度自动选，保证 ≤ want 个桶且桶宽是人话单位 */
const DENSITY_STEPS = [60, 300, 900, 1800, 3600, 7200, 14400, 43200, 86400]

function stepLabel(sec: number): string {
  if (sec < 3600) return sec === 60 ? i18n.t('time.minutesShortOne', { ns: 'common' }) : i18n.t('time.minutesShort', { ns: 'common', count: sec / 60 })
  if (sec < 86400) return sec === 3600 ? i18n.t('time.hoursShortOne', { ns: 'common' }) : i18n.t('time.hoursShort', { ns: 'common', count: sec / 3600 })
  return sec === 86400 ? i18n.t('time.daysShortOne', { ns: 'common' }) : i18n.t('time.daysShort', { ns: 'common', count: sec / 86400 })
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
