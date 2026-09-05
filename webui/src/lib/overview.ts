// 概览页的数据整形（纯函数）。
//
// 两条硬约束：
//   ① **禁止假数据**：所有计数、离线设备、失败命令、近期事件一律取自 GET /api/overview
//      的服务端聚合结果；前端不自算、不塞占位数字、不写死 demo 卡片。
//   ② **任何后端形态都不得白屏**：字段缺席/类型不对时归一化成安全空值，
//      由页面渲染设计过的 Empty 态，而不是抛未捕获异常。
import type { Tone } from '@/components/ui'
import type { CommandView, DeviceView, EventView, OverviewView } from './types'

function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0
}

function arr<T>(v: unknown): T[] {
  return Array.isArray(v) ? (v as T[]) : []
}

/**
 * 宽容归一化：后端字段缺席或类型漂移时给出安全默认值。
 * 不做任何「补一个看起来合理的数字」的动作 —— 缺席就是 0 / 空数组。
 */
export function normalizeOverview(raw: unknown): OverviewView {
  const o = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>
  return {
    devices_online: num(o.devices_online),
    devices_total: num(o.devices_total),
    edges_online: num(o.edges_online),
    edges_total: num(o.edges_total),
    plugins_active: num(o.plugins_active),
    plugins_desired: num(o.plugins_desired),
    commands_failed: num(o.commands_failed),
    recent_events: arr<EventView>(o.recent_events),
    offline_devices: arr<DeviceView>(o.offline_devices),
    failed_commands: arr<CommandView>(o.failed_commands),
    server_time: num(o.server_time),
  }
}

/** 概览的四个统计瓦片（值全部来自服务端聚合） */
export interface OverviewStat {
  key: 'devices' | 'edges' | 'plugins' | 'commands'
  label: string
  online: number
  total: number
  /** total=0 时的说明（Empty 语义，不是错误） */
  emptyHint: string
  tone: Tone
}

export function overviewStats(o: OverviewView): OverviewStat[] {
  return [
    {
      key: 'devices', label: '在线设备', online: o.devices_online, total: o.devices_total,
      emptyHint: '等待边缘节点接入设备', tone: o.devices_total === 0 ? 'idle'
        : o.devices_online === 0 ? 'bad' : 'ok',
    },
    {
      key: 'edges', label: '在线边缘', online: o.edges_online, total: o.edges_total,
      emptyHint: '尚未有边缘节点注册', tone: o.edges_total === 0 ? 'idle'
        : o.edges_online === 0 ? 'bad' : 'ok',
    },
    {
      key: 'plugins', label: '活跃插件', online: o.plugins_active, total: o.plugins_desired,
      emptyHint: '还没有插件实例', tone: o.plugins_desired === 0 ? 'idle'
        : o.plugins_active === 0 ? 'warn' : 'ok',
    },
    // 失败命令是「越小越好」的量：没有 total，只有计数
    {
      key: 'commands', label: '失败命令', online: o.commands_failed, total: -1,
      emptyHint: '没有失败的命令', tone: o.commands_failed === 0 ? 'ok' : 'bad',
    },
  ]
}

/** 「需要关注」条目：全部由服务端真实字段推导，一条都不编 */
export interface OverviewAlert {
  id: string
  tone: Tone
  title: string
  hint: string
  /** 跳转目标（让用户能一步走到可操作的页面） */
  to: string
  count: number
}

export function overviewAlerts(o: OverviewView): OverviewAlert[] {
  const out: OverviewAlert[] = []

  const offlineEdges = Math.max(0, o.edges_total - o.edges_online)
  if (offlineEdges > 0) {
    out.push({
      id: 'edges-offline', tone: 'bad', count: offlineEdges, to: '/edges',
      title: `${offlineEdges} 台边缘节点离线`,
      hint: '离线节点上的设备不会上报状态；已下发的命令会排队等它重连。其余在线节点不受影响。',
    })
  }

  if (o.offline_devices.length > 0) {
    out.push({
      id: 'devices-offline', tone: 'warn', count: o.offline_devices.length, to: '/devices',
      title: `${o.offline_devices.length} 台设备离线`,
      hint: '这些设备最近一次上报后未再更新，点开可看最后在线时间与历史事件。',
    })
  }

  if (o.commands_failed > 0 || o.failed_commands.length > 0) {
    const n = Math.max(o.commands_failed, o.failed_commands.length)
    out.push({
      id: 'commands-failed', tone: 'bad', count: n, to: '/activity',
      title: `${n} 条命令执行失败`,
      hint: '失败原因见命令回执；可先在设备详情页重发一次，或检查目标设备是否在线。',
    })
  }

  const pluginGap = Math.max(0, o.plugins_desired - o.plugins_active)
  if (pluginGap > 0) {
    out.push({
      id: 'plugins-gap', tone: 'warn', count: pluginGap, to: '/plugins',
      title: `${pluginGap} 个插件实例未达到活跃`,
      hint: '期望态已提交但运行宿主还没上报活跃。可能是宿主不可用、正在应用，或实际态与期望态不一致。',
    })
  }

  return out
}

/** 设备短名（列表/提示里用；后端给的 name 可能缺席，回落设备键的可读部分） */
export function deviceShortName(d: DeviceView): string {
  if (d.name) return d.name
  const tail = d.id.split('/').pop()
  return tail || d.id
}