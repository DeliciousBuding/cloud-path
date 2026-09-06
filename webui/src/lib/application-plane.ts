import { capabilityLabel, indexCapabilities, normalizeCapabilityDocs, normalizeDescriptor, propertyLabel } from './descriptor'
import type { AppBindingView } from './types'

/** 已知通用字段沿用公共词汇；无展示声明时保留字段名，不用序号掩盖含义。 */
export function recordFieldLabel(key: string): string {
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

/** 只格式化带时区且有效的 RFC3339 字符串，原值始终保留在 time/title 和原始数据中。 */
export function recordTimestamp(value: string, timeZone?: string): string | undefined {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value)) return undefined
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return undefined
  const offset = /([+-])(\d{2}):(\d{2})$/.exec(value)
  const minutes = offset ? (Number(offset[2]) * 60 + Number(offset[3])) * (offset[1] === '+' ? 1 : -1) : 0
  if (new Date(timestamp + minutes * 60_000).toISOString().slice(0, 19) !== value.slice(0, 19)) return undefined
  return new Intl.DateTimeFormat('zh-CN', {
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
    capability: /[\u3400-\u9fff]/.test(capability) ? capability : '应用所需能力',
  }
}

/** 只解释无歧义的五字段时间规则；复杂表达式保留在技术详情。 */
export function scheduleSummary(cron: string): string {
  const parts = cron.trim().split(/\s+/)
  if (parts.length !== 5) return '自定义时间规则'
  const [minute, hour, day, month, weekday] = parts
  if ([day, month, weekday].some((v) => v !== '*')) return '自定义时间规则'
  if (minute === '*' && hour === '*') return '每分钟'
  if (/^\*\/[1-9]\d?$/.test(minute) && hour === '*') {
    const step = Number(minute.slice(2))
    if (step < 60) return '每小时内每隔 ' + step + ' 分钟'
  }
  if (/^\d{1,2}$/.test(minute) && Number(minute) < 60) {
    if (hour === '*') return '每小时第 ' + Number(minute) + ' 分钟'
    if (/^\d{1,2}$/.test(hour) && Number(hour) < 24) {
      return '每天 ' + hour.padStart(2, '0') + ':' + minute.padStart(2, '0')
    }
  }
  return '自定义时间规则'
}

export function appTime(value?: number, timeZone?: string): string {
  if (!value) return '尚无记录'
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
      hourCycle: 'h23', timeZone,
    }).format(new Date(value * 1000))
  } catch { return '时间信息不可用' }
}

export function scheduleZone(timeZone: string): string {
  if (!timeZone) return '未提供时区'
  try {
    return new Intl.DateTimeFormat('zh-CN', { timeZone, timeZoneName: 'longGeneric' })
      .formatToParts(new Date()).find((p) => p.type === 'timeZoneName')?.value ?? '未提供时区'
  } catch { return '时区信息不可用' }
}
