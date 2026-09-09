import type { CapabilityIndex } from './descriptor'
import { entityTitle, primaryObservation, propertyLabel, unitLabel } from './descriptor'
import type { DeviceDescriptor } from './types'

/** 序列键排序：优先跟随 Descriptor 的 Entity 声明顺序，未知键稳定排后。 */
export function orderSeriesKeys(keys: readonly string[], descriptor?: DeviceDescriptor | null): string[] {
  const out = [...keys].sort()
  if (!descriptor) return out
  const order = new Map<string, number>()
  descriptor.entities.forEach((entity, index) => order.set(entity.entity_id, index))
  const rank = (key: string) => {
    const dot = key.lastIndexOf('.')
    return dot > 0 ? (order.get(key.slice(0, dot)) ?? 999) : 998
  }
  return out.sort((a, b) => rank(a) - rank(b) || a.localeCompare(b))
}

/** 序列展示名：Entity 中文名 · 属性中文名；Descriptor 缺席时回落通用属性词典。 */
export function seriesLabel(
  key: string, descriptor?: DeviceDescriptor | null, capabilities: CapabilityIndex | undefined = undefined,
): string {
  const dot = key.lastIndexOf('.')
  if (dot <= 0) return propertyLabel(key)
  const entityID = key.slice(0, dot)
  const property = key.slice(dot + 1)
  const entity = descriptor?.entities.find((item) => item.entity_id === entityID || item.unique_key === entityID)
  if (!entity) return propertyLabel(property)
  const capability = entity.observations?.[property]?.capability ?? entity.capabilities[0]
  return `${entityTitle(entity)} · ${propertyLabel(property, capability, capabilities)}`
}

/** 序列单位：声明里的人话单位；与 StateTile 火花线使用同一回落链。 */
export function seriesUnit(
  key: string, descriptor?: DeviceDescriptor | null, capabilities: CapabilityIndex | undefined = undefined,
): string | undefined {
  const dot = key.lastIndexOf('.')
  if (dot > 0) {
    const entity = descriptor?.entities.find((item) =>
      item.entity_id === key.slice(0, dot) || item.unique_key === key.slice(0, dot))
    return unitLabel(entity?.observations?.[key.slice(dot + 1)]?.unit)
  }
  const entities = descriptor?.entities ?? []
  const exact = entities.find((entity) => entity.entity_id === key || entity.unique_key === key)
  const candidates = exact ? [exact] : entities.filter((entity) => key.startsWith(`${entity.entity_id}_`))
  for (const entity of candidates) {
    const unit = unitLabel(primaryObservation(entity, capabilities)?.unit)
    if (unit) return unit
  }
  for (const entity of entities) {
    const unit = unitLabel(entity.observations?.[key]?.unit)
    if (unit) return unit
  }
  return undefined
}
