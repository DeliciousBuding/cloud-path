// 概览页的数据整形（纯函数）。
//
// 两条硬约束：
//   ① **禁止假数据**：所有计数、离线设备、失败操作、近期事件一律取自 GET /api/overview
//      的服务端聚合结果；前端不自算、不塞占位数字、不写死 demo 卡片。
//   ② **任何后端形态都不得白屏**：字段缺席/类型不对时归一化成安全空值，
//      由页面渲染设计过的 Empty 态，而不是抛未捕获异常。
import type { Tone } from '@/components/ui'
import { i18n } from '@/i18n'
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
      key: 'devices', label: i18n.t('stats.devices', { ns: 'overview' }), online: o.devices_online, total: o.devices_total,
      emptyHint: i18n.t('empty.devices', { ns: 'overview' }), tone: o.devices_total === 0 ? 'idle'
        : o.devices_online === 0 ? 'bad' : 'ok',
    },
    {
      key: 'edges', label: i18n.t('stats.edges', { ns: 'overview' }), online: o.edges_online, total: o.edges_total,
      emptyHint: i18n.t('empty.edges', { ns: 'overview' }), tone: o.edges_total === 0 ? 'idle'
        : o.edges_online === 0 ? 'bad' : 'ok',
    },
    {
      key: 'plugins', label: i18n.t('stats.plugins', { ns: 'overview' }), online: o.plugins_active, total: o.plugins_desired,
      emptyHint: i18n.t('empty.plugins', { ns: 'overview' }), tone: o.plugins_desired === 0 ? 'idle'
        : o.plugins_active === 0 ? 'warn' : 'ok',
    },
    // 固定近24小时的完整失败/超时计数，来自服务端；不以有界列表长度推算
    {
      key: 'commands', label: i18n.t('stats.commandsWindow', { ns: 'overview' }), online: o.commands_failed, total: -1,
      emptyHint: i18n.t('empty.commandsWindow', { ns: 'overview' }), tone: o.commands_failed === 0 ? 'ok' : 'bad',
    },
  ]
}

/** 「需要关注」条目：全部由服务端真实字段推导，一条都不编 */
export interface OverviewAlert {
  id: string
  tone: Tone
  title: string
  hint: string
  /** live=当前运行状态，恢复后自动消失；history=历史失败记录，可人工标记已处理。 */
  kind: 'live' | 'history'
  /** 跳转目标（让用户能一步走到可操作的页面） */
  to: string
  count: number
}

export function overviewAlerts(o: OverviewView): OverviewAlert[] {
  const out: OverviewAlert[] = []

  const offlineEdges = Math.max(0, o.edges_total - o.edges_online)
  if (offlineEdges > 0) {
    out.push({
      id: 'edges-offline', tone: 'bad', kind: 'live', count: offlineEdges, to: '/edges',
      title: i18n.t('alertDetails.edgesOfflineTitle', { ns: 'overview', count: offlineEdges }),
      hint: i18n.t('alertDetails.edgesOfflineHint', { ns: 'overview' }),
    })
  }

  if (o.offline_devices.length > 0) {
    out.push({
      id: 'devices-offline', tone: 'warn', kind: 'live', count: o.offline_devices.length, to: '/devices',
      title: i18n.t('alertDetails.devicesOfflineTitle', { ns: 'overview', count: o.offline_devices.length }),
      hint: i18n.t('alertDetails.devicesOfflineHint', { ns: 'overview' }),
    })
  }

  if (o.commands_failed > 0) {
    const n = o.commands_failed
    out.push({
      id: 'commands-failed', tone: 'bad', kind: 'history', count: n, to: '/activity?tab=commands&status=failed&handled=unhandled',
      title: i18n.t('alertDetails.commandsFailedTitle', { ns: 'overview', count: n }),
      hint: i18n.t('alertDetails.commandsFailedHint', { ns: 'overview' }),
    })
  }

  const pluginGap = Math.max(0, o.plugins_desired - o.plugins_active)
  if (pluginGap > 0) {
    out.push({
      id: 'plugins-gap', tone: 'warn', kind: 'live', count: pluginGap, to: '/plugins',
      title: i18n.t('alertDetails.pluginsGapTitle', { ns: 'overview', count: pluginGap }),
      hint: i18n.t('alertDetails.pluginsGapHint', { ns: 'overview' }),
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