import { capabilityLabel, indexCapabilities, normalizeCapabilityDocs, normalizeDescriptor, propertyLabel } from './descriptor'
import type { AppBindingView } from './types'

/** 任意业务字段没有展示声明时只给序号，不把机器变量翻译成臆测的业务语义。 */
export function recordFieldLabel(key: string, index: number): string {
  const label = propertyLabel(key)
  return /[\u3400-\u9fff]/.test(label) ? label : '数据项 ' + (index + 1)
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
