import { describe, expect, it } from 'vitest'
import { normalizeOverview, overviewAlerts, overviewStats } from '@/lib/overview'
import type { CommandView, OverviewView } from '@/lib/types'

const failure: CommandView = {
  id: 1, device_id: 'edge/device', cmd: 'ping', args: '', status: 'failed',
  created_at: 100, acked_at: 110, result: 'device busy',
}

function overview(fields: Partial<OverviewView> = {}): OverviewView {
  return { ...normalizeOverview(null), ...fields }
}

function commandStat(o: OverviewView) {
  return overviewStats(o).find((stat) => stat.key === 'commands')
}

function commandAlert(o: OverviewView) {
  return overviewAlerts(o).find((alert) => alert.id === 'commands-failed')
}

describe('overview failure window', () => {
  it('labels both the count and its empty state as the last 24 hours', () => {
    const o = overview()
    expect(commandStat(o)).toEqual({
      key: 'commands', label: '近24小时失败命令', online: 0, total: -1,
      emptyHint: '近24小时没有失败或超时的命令', tone: 'ok',
    })
    expect(commandAlert(o)).toBeUndefined()
  })

  it('uses the full server count for both card and attention, never the bounded preview length', () => {
    const o = overview({
      commands_failed: 2107,
      failed_commands: Array.from({ length: 20 }, (_, i) => ({ ...failure, id: i + 1 })),
    })
    expect(commandStat(o)).toMatchObject({ online: 2107, tone: 'bad' })
    expect(commandAlert(o)).toMatchObject({
      count: 2107, title: '近24小时 2107 条命令失败或超时',
    })
  })

  it('directs users to failure records and receipts, without suggesting a blind replay', () => {
    const o = overview({
      commands_failed: 1,
      failed_commands: [{ ...failure, status: 'timeout' }],
    })
    const alert = commandAlert(o)
    expect(alert).toMatchObject({
      count: 1, to: '/activity', title: '近24小时 1 条命令失败或超时',
    })
    expect(alert?.hint).toContain('查看失败或超时记录及回执')
    expect(alert?.hint).toContain('发生时间')
    expect(alert?.hint).toContain('全部历史记录')
    expect(alert?.hint).not.toMatch(/重发|重试|重新下发/)
  })

  it('does not turn stale or inconsistent preview rows into a different attention count', () => {
    const preview = [failure, { ...failure, id: 2, status: 'timeout' }]
    const empty = overview({ commands_failed: 0, failed_commands: preview })
    expect(commandStat(empty)?.online).toBe(0)
    expect(commandAlert(empty)).toBeUndefined()
    const counted = overview({ commands_failed: 1, failed_commands: preview })
    expect(commandStat(counted)?.online).toBe(1)
    expect(commandAlert(counted)?.count).toBe(1)
  })

  it('retains a reported count even if the optional preview is empty', () => {
    const o = overview({ commands_failed: 3 })
    expect(commandStat(o)?.online).toBe(3)
    expect(commandAlert(o)?.count).toBe(3)
  })

  it('keeps missing or malformed aggregate fields at safe empty values', () => {
    const o = normalizeOverview({ commands_failed: '9', failed_commands: null })
    expect(o.commands_failed).toBe(0)
    expect(o.failed_commands).toEqual([])
    expect(commandStat(o)?.online).toBe(0)
    expect(commandAlert(o)).toBeUndefined()
  })
})
