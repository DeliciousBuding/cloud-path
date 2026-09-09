import { i18n, currentLocale } from '@/i18n'
import { capabilityLabel, indexCapabilities, normalizeCapabilityDocs, normalizeDescriptor, propertyLabel } from './descriptor'
import type { AppBindingView } from './types'

/** 跨应用通用的字段词汇；只解释含义明确的展示名，未知字段仍保留原值。 */
const RECORD_FIELD_LABEL: Record<string, string> = {
  id: 'id', name: 'name', title: 'title', label: 'label', subject: 'subject', description: 'description', message: 'message',
  created_at: 'created_at', updated_at: 'updated_at', happened_at: 'happened_at', occurred_at: 'occurred_at',
  checked_at: 'checked_at', started_at: 'started_at', ended_at: 'ended_at', start_time: 'start_time', end_time: 'end_time',
  start: 'start', end: 'end', opened_at: 'opened_at', closed_at: 'closed_at', compartment: 'compartment',
  slot: 'slot', location: 'location', position: 'position', reason: 'reason', result: 'result', summary: 'summary',
  severity: 'severity', bindings_valid: 'bindings_valid', configured: 'configured', armed: 'armed', alerting: 'alerting',
  cooldown_until: 'cooldown_until', error: 'error', event: 'event', action: 'action', type: 'type', healthy: 'healthy',
  temperature_c: 'temperature_c', version: 'version', state: 'state', status: 'status',
}

/** 已知通用字段沿用公共词汇；无展示声明时保留字段名，不用序号掩盖含义。 */
export function recordFieldLabel(key: string): string {
  const translationKey = RECORD_FIELD_LABEL[key]
  if (translationKey) return i18n.t(`plugin:recordFields.${translationKey}`)
  const label = propertyLabel(key)
  return /[\u3400-\u9fff]/.test(label) ? label : key
}

export function emptyRecordValue(value: unknown): boolean {
  return value === null || value === ''
}

/** 只调整呈现顺序：标题、名称、状态类字段优先，空值置后；不解释业务枚举。 */
export function recordEntries(value: object): [string, unknown][] {
  const entries = Object.entries(value)
  if (Array.isArray(value)) return entries
  const priority = (key: string, item: unknown) => {
    if (emptyRecordValue(item)) return 4
    if (/^(title|name|label)$/.test(key)) return 0
    if (/(^|_)(state|status)$/.test(key)) return 1
    if (/^(summary|description)$/.test(key)) return 2
    return 3
  }
  return entries.sort(([a, av], [b, bv]) => priority(a, av) - priority(b, bv))
}

function recordScalar(value: unknown): string | undefined {
  if (typeof value === 'string') return value.trim() || undefined
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  if (typeof value === 'boolean') return value ? i18n.t('plugin:record.yes') : i18n.t('plugin:record.no')
  return undefined
}

/** 从通用内容生成可扫读标题；未知结构保留调用方给出的回退标题。 */
export function recordHeadline(value: unknown, fallback: string, valuesByKey?: Record<string, Record<string, string>>): { title: string; usedKeys: string[] } {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return { title: fallback, usedKeys: [] }
  const entries = recordEntries(value).filter(([, item]) => !emptyRecordValue(item))
  const used: string[] = []
  const pick = (pattern: RegExp) => entries.find(([key, item]) => pattern.test(key) && recordScalar(item) !== undefined)

  // 应用显式声明的标题/名称优先；没有声明时才从通用位置、状态、时间字段组合。
  const named = pick(/^(title|name|label|subject)$/)
  if (named) return { title: recordScalar(named[1])!, usedKeys: [named[0]] }

  const parts: string[] = []
  const add = (entry: [string, unknown] | undefined, display?: string) => {
    if (!entry) return
    const mapped = valuesByKey?.[entry[0]]?.[String(entry[1])]
    const value = display ?? mapped ?? recordScalar(entry[1])
    if (!value) return
    parts.push(`${recordFieldLabel(entry[0])} ${value}`)
    used.push(entry[0])
  }
  const location = pick(/^(slot|location|position|compartment)$/)
  const state = pick(/^(state|status)$/) ?? pick(/(^|_)(state|status)$/)
  if (location || state) {
    add(location)
    add(state)
    return { title: parts.join(' · ') || fallback, usedKeys: used }
  }

  const time = entries.find(([key, item]) => typeof item === 'string' &&
    (/(^|_)(at|time)$/.test(key) || /^(start|end)$/.test(key)) && recordTimestamp(item) !== undefined)
  if (time) {
    add(time, typeof time[1] === 'string' ? recordTimestamp(time[1]) : undefined)
    return { title: parts.join(' · ') || fallback, usedKeys: used }
  }

  const id = pick(/(^|_)(id|key)$/)
  add(id)
  return { title: parts.join(' · ') || fallback, usedKeys: used }
}


/** 应用操作结果的首屏摘要：只展示可读的通用字段，机器字段原文留在“查看结果原文”。 */
export function applicationResultSummary(value: unknown): { text?: string; usedKeys: string[] } {
  const usedKeys: string[] = []
  const scalar = (item: unknown): string | undefined => {
    if (typeof item === 'string') return item.trim() || undefined
    if (typeof item === 'number' && Number.isFinite(item)) return String(item)
    if (typeof item === 'boolean') return item ? i18n.t('plugin:record.yes') : i18n.t('plugin:record.no')
    return undefined
  }
  const direct = scalar(value)
  if (direct !== undefined) return { text: direct, usedKeys }
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return { usedKeys }
  const record = value as Record<string, unknown>
  const parts: string[] = []
  const add = (key: string, text: string, label?: string) => {
    usedKeys.push(key)
    parts.push(label ? label + ' ' + text : text)
  }
  for (const key of ['message', 'summary', 'result', 'name', 'title']) {
    const text = scalar(record[key])
    if (text) { add(key, text); break }
  }
  const statusText = (raw: string): string | undefined => {
    switch (raw.trim().toLowerCase()) {
      case 'ok':
      case 'succeeded':
        return i18n.t('plugin:summary.success')
      case 'failed':
        return i18n.t('plugin:summary.failure')
      case 'pending':
        return i18n.t('plugin:summary.pending')
      case 'sent':
        return i18n.t('plugin:summary.sent')
      default:
        return undefined
    }
  }
  for (const key of ['state', 'status']) {
    const text = scalar(record[key])
    const label = text ? statusText(text) : undefined
    if (label) { add(key, label, i18n.t('plugin:summary.status')); break }
  }
  const count = record.run_count
  if (typeof count === 'number' && Number.isFinite(count)) add('run_count', String(count), i18n.t('plugin:summary.runCount'))
  for (const key of ['finished_at', 'ended_at', 'updated_at', 'created_at']) {
    const raw = record[key]
    const text = typeof raw === 'string' ? recordTimestamp(raw) : undefined
    if (text) { add(key, text, i18n.t('plugin:summary.finishedAt')); break }
  }
  if (!parts.length && typeof record.ok === 'boolean') add('ok', record.ok ? i18n.t('plugin:summary.success') : i18n.t('plugin:summary.failure'))
  return { text: parts.join(' · ') || undefined, usedKeys }
}
/** 应用运行态的唯一展示结论：观察态与实际运行态冲突时明确报冲突，不二选一。 */
export type ApplicationRunningState = 'running' | 'stopped' | 'conflict' | 'unknown'
export function applicationRunningState(running: boolean | undefined, runtimeState?: string): ApplicationRunningState {
  const observed = runtimeState?.trim().toLowerCase()
  if (observed === 'running') {
    if (running === true) return 'running'
    if (running === false) return 'conflict'
    return 'unknown'
  }
  if (observed === 'stopped') {
    return running === true ? 'conflict' : 'stopped'
  }
  // 组件未收到观察态时，才允许数据面单独给出运行结论；收到未知观察态则保持待确认。
  if (runtimeState !== undefined) return running === false ? 'stopped' : 'unknown'
  if (running === true) return 'running'
  if (running === false) return 'stopped'
  return 'unknown'
}

/** 只格式化带时区且有效的 RFC3339 字符串，原值始终保留在 time/title 和原始数据中。 */
export function recordTimestamp(value: string, timeZone?: string): string | undefined {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value)) return undefined
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return undefined
  const offset = /([+-])(\d{2}):(\d{2})$/.exec(value)
  const minutes = offset ? (Number(offset[2]) * 60 + Number(offset[3])) * (offset[1] === '+' ? 1 : -1) : 0
  if (new Date(timestamp + minutes * 60_000).toISOString().slice(0, 19) !== value.slice(0, 19)) return undefined
  return new Intl.DateTimeFormat(currentLocale(), {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
    hourCycle: 'h23', timeZone,
  }).format(new Date(timestamp))
}

export function bindingLabels(binding: AppBindingView, payload: unknown) {
  const data = payload as { descriptors?: unknown; capabilities?: unknown } | null | undefined
  const descriptors = Array.isArray(data?.descriptors) ? data.descriptors.map(normalizeDescriptor) : []
  const matches = descriptors.flatMap((d) => d?.entities.filter((e) => e.entity_id === binding.entity_id) ?? [])
  const index = indexCapabilities(normalizeCapabilityDocs(data?.capabilities))
  const capability = capabilityLabel(binding.capability, index)
  return {
    // entity_id 当前是设备内局部标识：多设备同名不能冒充一个确定的绑定设备。
    entity: matches.length === 1 ? matches[0].name : undefined,
    capability: /[\u3400-\u9fff]/.test(capability) ? capability : i18n.t('plugin:plane.requiredCapability'),
  }
}

/** 只解释无歧义的五字段时间规则；复杂表达式保留在技术详情。 */
export function scheduleSummary(cron: string): string {
  const parts = cron.trim().split(/\s+/)
  if (parts.length !== 5) return i18n.t('plugin:schedule.custom')
  const [minute, hour, day, month, weekday] = parts
  if ([day, month, weekday].some((v) => v !== '*')) return i18n.t('plugin:schedule.custom')
  if (minute === '*' && hour === '*') return i18n.t('plugin:schedule.everyMinute')
  if (/^\*\/[1-9]\d?$/.test(minute) && hour === '*') {
    const step = Number(minute.slice(2))
    if (step < 60) return i18n.t('plugin:schedule.everyMinutes', { step })
  }
  if (/^\d{1,2}$/.test(minute) && Number(minute) < 60) {
    if (hour === '*') return i18n.t('plugin:schedule.minuteOfHour', { minute: Number(minute) })
    if (/^\d{1,2}$/.test(hour) && Number(hour) < 24) {
      return i18n.t('plugin:schedule.dailyAt', { time: hour.padStart(2, '0') + ':' + minute.padStart(2, '0') })
    }
  }
  return i18n.t('plugin:schedule.custom')
}

export function appTime(value?: number, timeZone?: string): string {
  if (!value) return i18n.t('plugin:time.noRecord')
  try {
    return new Intl.DateTimeFormat(currentLocale(), {
      year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
      hourCycle: 'h23', timeZone,
    }).format(new Date(value * 1000))
  } catch { return i18n.t('plugin:time.unavailable') }
}

export function scheduleZone(timeZone: string): string {
  if (!timeZone) return i18n.t('plugin:time.timezoneMissing')
  try {
    return new Intl.DateTimeFormat(currentLocale(), { timeZone, timeZoneName: 'longGeneric' })
      .formatToParts(new Date()).find((p) => p.type === 'timeZoneName')?.value ?? i18n.t('plugin:time.timezoneMissing')
  } catch { return i18n.t('plugin:time.timezoneUnavailable') }
}
