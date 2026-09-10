import type { DeviceView } from './types'

/**
 * WS `state` 可能比设备元数据先到，运行中新注册的设备会被实时层先建成
 * adapter/name/port 为空的占位对象。状态与时间戳仍以 live 为准，但静态元数据
 * 必须由最近一次 REST/snapshot 事实补齐，否则设备名会退化成 ID，3D 的 adapter
 * 判据也会失败。
 */
export function mergeDeviceMetadata(live: DeviceView, fallback?: DeviceView): DeviceView {
  if (!fallback) return live
  return {
    ...fallback,
    ...live,
    id: live.id || fallback.id,
    edge_id: live.edge_id || fallback.edge_id,
    adapter: live.adapter || fallback.adapter,
    name: live.name || fallback.name,
    port: live.port || fallback.port,
  }
}
