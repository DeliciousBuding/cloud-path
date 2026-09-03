// 边缘节点（Edge）的展示事实推导（纯函数）。
//
// 一条硬要求：一台 Edge 掉线**不得影响**其他 Edge 的呈现 —— 所以这里对每台 Edge
// 独立算事实，缺数据就是缺数据（0 / 0 时间戳），绝不借用别的 Edge 的值补齐。
import type { DeviceView, EdgeView } from './types'

export interface EdgeFacts {
  edge: EdgeView
  /** 该 Edge 名下的设备（按设备键前缀归属，不用后端给的 devices 数组单独判断） */
  devices: DeviceView[]
  onlineDevices: number
  /** 最近一次上报时间（unix 秒）；0 = 从没上报过 */
  lastReport: number
  /** 后端 hello 时声明的设备键（可能与当前实际归属不一致，用于提示差异） */
  declared: string[]
}

/** 设备键 `"<edge_id>/<device_id>"` → 归属的 edge_id（不含 `/` 时返回原串） */
export function edgeOfDevice(deviceKey: string): string {
  const i = deviceKey.indexOf('/')
  return i > 0 ? deviceKey.slice(0, i) : deviceKey
}

/** 把设备按 Edge 归组，并给出每台的在线数与最近上报时间 */
export function edgeFacts(edges: EdgeView[], devices: DeviceView[]): EdgeFacts[] {
  const byEdge = new Map<string, DeviceView[]>()
  for (const d of devices) {
    const k = edgeOfDevice(d.id || d.edge_id)
    const arr = byEdge.get(k)
    if (arr) arr.push(d)
    else byEdge.set(k, [d])
  }
  return edges.map((edge) => {
    // 归属优先用实际设备表；后端 devices 数组只在设备表缺席时兜底（保持 declared 供对照）
    const list = byEdge.get(edge.edge_id)
      ?? (edge.devices ?? []).map((key) => ({
        id: key, edge_id: edge.edge_id, adapter: '', online: false,
        state: {}, updated_at: 0, last_seen: 0,
      } as DeviceView))
    let lastReport = 0
    let onlineDevices = 0
    for (const d of list) {
      const t = Math.max(d.updated_at ?? 0, d.last_seen ?? 0)
      if (t > lastReport) lastReport = t
      if (d.online) onlineDevices++
    }
    return { edge, devices: list, onlineDevices, lastReport, declared: edge.devices ?? [] }
  })
}

/** 在线优先，其次按最近上报倒序，最后按 ID 字典序（同一次渲染内顺序稳定，不跳位） */
export function sortEdgeFacts(facts: EdgeFacts[]): EdgeFacts[] {
  return [...facts].sort((a, b) => {
    if (a.edge.online !== b.edge.online) return a.edge.online ? -1 : 1
    if (b.lastReport !== a.lastReport) return b.lastReport - a.lastReport
    return a.edge.edge_id.localeCompare(b.edge.edge_id)
  })
}

export type EdgeFilter = 'all' | 'online' | 'offline'

export function filterEdgeFacts(facts: EdgeFacts[], filter: EdgeFilter): EdgeFacts[] {
  if (filter === 'online') return facts.filter((f) => f.edge.online)
  if (filter === 'offline') return facts.filter((f) => !f.edge.online)
  return facts
}

/** 设备短名：后端 name 缺席时回落设备键尾段（永不显示空白） */
export function deviceLabel(d: DeviceView): string {
  if (d.name) return d.name
  const tail = d.id.split('/').pop()
  return tail || d.id || '未命名设备'
}