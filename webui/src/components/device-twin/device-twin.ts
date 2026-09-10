import { observationsOf } from '@/lib/descriptor'
import type { DeviceDescriptor, DeviceView } from '@/lib/types'
import type { BoardVisualState } from './vendor/stcb/visual-state'

export type DeviceTwinIndicator = {
  kind: 'led' | 'mode' | 'page'
  value: string
  tone: 'good' | 'warn'
}

export type DeviceTwinResolution = {
  id: string
  visualState: BoardVisualState
  indicators: DeviceTwinIndicator[]
}


function observationValue(descriptor: DeviceDescriptor | null, entityID: string, property: string): unknown {
  if (!descriptor) return undefined
  const entity = descriptor.entities.find((candidate) => candidate.entity_id === entityID)
  if (!entity) return undefined
  return observationsOf(entity).find((candidate) => candidate.property === property)?.value
}

function rawValue(device: DeviceView, keys: readonly string[]): unknown {
  for (const key of keys) {
    if (Object.prototype.hasOwnProperty.call(device.state, key)) return device.state[key]
  }
  return undefined
}

function integerInRange(value: unknown, min: number, max: number): number | undefined {
  const number = typeof value === 'number' ? value : typeof value === 'string' ? Number(value) : NaN
  return Number.isInteger(number) && number >= min && number <= max ? number : undefined
}

function displayText(value: unknown): string | undefined {
  const raw = Array.isArray(value) ? value.join('') : value
  if (typeof raw !== 'string') return undefined
  const normalized = raw.trim().slice(0, 8)
  return /^[0-9 -]{0,8}$/.test(normalized) ? normalized : undefined
}

function textValue(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  const normalized = value.trim()
  return normalized ? normalized.slice(0, 48) : undefined
}

function clockDisplay(value: unknown, updatedAt: number, nowSeconds: number): string | undefined {
  const raw = textValue(value)
  const match = raw?.match(/^(\d{1,2}):(\d{2}):(\d{2})$/)
  if (!match || !Number.isFinite(updatedAt) || !Number.isFinite(nowSeconds)) return undefined
  const elapsed = Math.max(0, Math.min(6 * 60 * 60, Math.floor(nowSeconds - updatedAt)))
  const seconds = ((Number(match[1]) * 60 * 60 + Number(match[2]) * 60 + Number(match[3])) + elapsed) % (24 * 60 * 60)
  const hour = Math.floor(seconds / 3600)
  const minute = Math.floor((seconds % 3600) / 60)
  const second = seconds % 60
  return `${String(hour).padStart(2, '0')}-${String(minute).padStart(2, '0')}-${String(second).padStart(2, '0')}`
}

function resolveStcb(device: DeviceView, descriptor: DeviceDescriptor | null, nowSeconds: number): DeviceTwinResolution {
  const ledMask = integerInRange(
    observationValue(descriptor, 'led-bank', 'mask') ?? rawValue(device, ['led-bank.mask', 'led_mask', 'led']),
    0,
    255,
  ) ?? 0
  const reportedDisplay = displayText(observationValue(descriptor, 'display', 'digits') ?? rawValue(device, ['display.digits', 'digits']))
  const mode = textValue(observationValue(descriptor, 'display', 'mode') ?? rawValue(device, ['display.mode', 'Display']))
  const page = textValue(observationValue(descriptor, 'display', 'page') ?? rawValue(device, ['display.page', 'Page']))
  const clock = observationValue(descriptor, 'clock', 'time') ?? rawValue(device, ['clock.time', 'clock'])
  const derivedClock = mode === 'clock' || page === 'clock' ? clockDisplay(clock, device.updated_at, nowSeconds) : undefined
  const display = reportedDisplay ?? derivedClock ?? '        '

  return {
    id: 'stcb',
    visualState: {
      powered: device.online,
      display,
      ledMask,
      ledColor: 'blue',
    },
    indicators: [
      { kind: 'led', value: `0x${ledMask.toString(16).toUpperCase().padStart(2, '0')}`, tone: ledMask ? 'good' : 'warn' },
      ...(mode ? [{ kind: 'mode' as const, value: mode, tone: 'good' as const }] : []),
      ...(page ? [{ kind: 'page' as const, value: page, tone: 'good' as const }] : []),
    ],
  }
}

/**
 * 设备详情页的显式前端适配表。
 *
 * 这里刻意只放已内置的参考模型，不修改 Driver/Core 契约。新增设备类型时必须
 * 在此增加独立适配器；通用页面本身不知道 STC-B 字段。
 */
export function supportsDeviceTwin(device: DeviceView): boolean {
  return device.adapter === 'stcb'
}

export function resolveDeviceTwin(
  device: DeviceView,
  descriptor: DeviceDescriptor | null,
  nowSeconds = Math.floor(Date.now() / 1000),
): DeviceTwinResolution | undefined {
  if (!supportsDeviceTwin(device)) return undefined
  return resolveStcb(device, descriptor, nowSeconds)
}
