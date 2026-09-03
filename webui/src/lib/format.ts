// 展示格式化工具（纯函数，无副作用；与 Go 侧语义一一对应）
import type { EventView } from './types'
import type { Tone } from '@/components/ui'

export function fmtClock(hhmm: string | undefined): string {
  return hhmm && /^\d{2}:\d{2}$/.test(hhmm) ? hhmm : '--:--'
}

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

export function fmtDrift(min: number | undefined | null): string {
  if (min == null || Number.isNaN(min)) return '—'
  const sign = min > 0 ? '+' : ''
  return `${sign}${min} 分`
}

/** 漂移健康度：|d|<=1 优 / <=5 良 / 更大差 */
export function driftTone(min: number | undefined | null): Tone {
  if (min == null || Number.isNaN(min)) return 'idle'
  const a = Math.abs(min)
  if (a <= 1) return 'ok'
  if (a <= 5) return 'warn'
  return 'bad'
}

/** 事件类型 → 展示语义。类型名是设备协议契约（docs/protocol.md），标签是平台通用语义。 */
export const EVENT_META: Record<string, { label: string; tone: Tone }> = {
  'BOOT':       { label: '上电',       tone: 'accent' },
  'REMIND':     { label: '提醒',       tone: 'warn' },
  'TAKEN':      { label: '已确认',     tone: 'ok' },
  'TAKEN-LATE': { label: '逾期确认',   tone: 'warn' },
  'MISSED':     { label: '逾期未确认', tone: 'bad' },
  'SYNC-OK':    { label: '对时成功',   tone: 'ok' },
}

export function eventMeta(type: string) {
  return EVENT_META[type] ?? { label: type, tone: 'idle' as Tone }
}

/** 命令 → 展示语义（与适配器 SupportedCommands 对应；未知命令回落原名） */
export const CMD_META: Record<string, { label: string; hint: string }> = {
  sync:    { label: '对时',     hint: '把设备时钟校准到参考时间' },
  dump:    { label: '读取状态', hint: '请求一次状态转储' },
  trigger: { label: '触发提醒', hint: '立即进入提醒状态（联调用）' },
  open:    { label: '模拟确认', hint: '模拟一次开盖/确认动作' },
  isp:     { label: '进入刷机', hint: '设备软复位进入 ISP 烧录模式' },
  raw:     { label: '原始指令', hint: '按原样写入串口（高级）' },
}

export function cmdMeta(cmd: string) {
  return CMD_META[cmd] ?? { label: cmd, hint: '' }
}

/** 命令状态 → 徽标语义 */
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
