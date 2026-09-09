import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { EventFeed, commandDisplayMeta, commandFailureInfo, eventDisplayLabel } from '@/components/EventFeed'
import { renderWithProviders } from '@/test/render'
import type { EventView } from '@/lib/types'

const ev = (id: number, ts: number): EventView => ({ id, ts, type: 'device-booted', device_id: 'e/d', payload: '' })

describe('EventFeed day 分组', () => {
  const now = Math.floor(Date.now() / 1000)

  it('dayGrouped（跨天历史）按天分组，组头是扫读锚点', () => {
    renderWithProviders(<EventFeed events={[ev(1, now), ev(2, now - 86_400)]} dayGrouped limit={10} />)
    expect(screen.getByText('今天')).toBeInTheDocument()
    expect(screen.getByText('昨天')).toBeInTheDocument()
  })

  it('日期由组头承载：行内不再重复完整日期（完整时间只在 title）', () => {
    renderWithProviders(<EventFeed events={[ev(1, now), ev(2, now - 86_400)]} dayGrouped limit={10} />)
    expect(screen.queryByText(/\d{4}\/\d+\/\d+ /)).toBeNull()
  })

  it('紧凑列表（概览/详情页）不插组头，保持单行密度', () => {
    renderWithProviders(<EventFeed events={[ev(1, now), ev(2, now - 86_400)]} limit={10} />)
    expect(screen.queryByText('今天')).toBeNull()
    expect(screen.queryByText('昨天')).toBeNull()
  })
})

describe('机器名中文优先展示名', () => {
  it('事件优先用通用中文词典，原始机器名只留在 title', () => {
    const machineName = 'Device Compartment Opened'
    renderWithProviders(<EventFeed events={[{ ...ev(1, Math.floor(Date.now() / 1000)), type: machineName }]} limit={10} />)
    expect(screen.getByText('设备舱门已打开')).toBeInTheDocument()
    expect(screen.getByTitle(`原始类型：${machineName}`)).toBeInTheDocument()
    expect(screen.queryByText(machineName)).toBeNull()
  })

  it('覆盖用户可见的英文事件与操作机器名', () => {
    expect(eventDisplayLabel('Pillbox Remind')).toBe('药盒提醒')
    expect(eventDisplayLabel('stcb.sensor')).toBe('传感器状态')
    expect(eventDisplayLabel('Read Register')).toBe('读取寄存器')
    expect(commandDisplayMeta('read-register').label).toBe('读取寄存器')
    expect(commandFailureInfo('device busy')).toEqual({
      message: '设备正忙', next: '等待设备空闲后重试',
    })
  })

  it('未知事件与操作不把英文机器码当主标签，原码只进技术详情', () => {
    expect(eventDisplayLabel('vendor-setpoint-changed')).toBe('未知事件')
    expect(commandDisplayMeta('vendor-setpoint-changed')).toEqual({ label: '未知操作', hint: '' })

    renderWithProviders(<EventFeed events={[{
      ...ev(1, Math.floor(Date.now() / 1000)),
      type: 'vendor-setpoint-changed',
      payload: '{"message":"network timeout"}',
    }]} limit={10} />)
    expect(screen.getByText('未知事件')).toBeInTheDocument()
    expect(screen.getByText('网络连接超时')).toBeInTheDocument()
    expect(screen.queryByText('vendor-setpoint-changed')).toBeNull()
    expect(screen.queryByText('network timeout')).toBeNull()
  })
})

// 展开入口只在载荷确有增量信息时出现。回归点：判据曾是 `payload !== '{}'`，
// 于是 {"type":"device-booted"} 这种「展开就是它自己」的载荷也给按钮——
// 本地演示栈 9 条事件里 9 条都是这个形状，等于每行都挂一个骗点击的入口。
describe('EventFeed 原始载荷展开入口', () => {
  const now = Math.floor(Date.now() / 1000)
  const withPayload = (payload: string, id = 1, type = 'device-booted'): EventView =>
    ({ id, ts: now, type, device_id: 'e/d', payload })

  it('载荷只有 type → 不给「查看事件详情」按钮', () => {
    renderWithProviders(<EventFeed events={[withPayload('{"type":"device-booted"}')]} limit={10} />)
    expect(screen.queryByRole('button', { name: /事件详情/ })).toBeNull()
    expect(screen.queryByRole('group', { name: '事件详情数据' })).toBeNull()
  })

  it('空载荷与空对象同样不给入口', () => {
    renderWithProviders(<EventFeed events={[withPayload(''), withPayload('{}', 2)]} limit={10} />)
    expect(screen.queryByRole('button', { name: /事件详情/ })).toBeNull()
  })

  it('载荷有 type 以外的键 → 给入口，点开是原始 JSON，再点收起', async () => {
    const user = userEvent.setup()
    const raw = '{"type":"setpoint-changed","value":42}'
    renderWithProviders(<EventFeed events={[withPayload(raw, 1, 'setpoint-changed')]} limit={10} />)
    expect(screen.queryByRole('group', { name: '事件详情数据' })).toBeNull()

    await user.click(screen.getByRole('button', { name: '查看事件详情' }))
    expect(screen.getByRole('group', { name: '事件详情数据' }).textContent).toContain(raw)

    await user.click(screen.getByRole('button', { name: '收起事件详情' }))
    expect(screen.queryByRole('group', { name: '事件详情数据' })).toBeNull()
  })

  it('载荷不是 JSON → 保留入口（脏串口碎片这类原文本身就是取证材料）', async () => {
    const user = userEvent.setup()
    renderWithProviders(<EventFeed events={[withPayload('RAW-SERIAL-GARBAGE')]} limit={10} />)
    await user.click(screen.getByRole('button', { name: '查看事件详情' }))
    expect(screen.getByRole('group', { name: '事件详情数据' }).textContent).toContain('RAW-SERIAL-GARBAGE')
  })

  it('混合列表只给有增量的那一行入口（按行判定，不是全有或全无）', () => {
    renderWithProviders(<EventFeed events={[
      withPayload('{"type":"device-booted"}', 1),
      withPayload('{"type":"setpoint-changed","value":7}', 2, 'setpoint-changed'),
      withPayload('{"type":"probed"}', 3, 'probed'),
    ]} limit={10} />)
    expect(screen.getAllByRole('button', { name: '查看事件详情' })).toHaveLength(1)
  })

  it('dayGrouped 长历史模式下同样按行判定', () => {
    renderWithProviders(<EventFeed dayGrouped limit={10} events={[
      withPayload('{"type":"device-booted"}', 1),
      withPayload('{"type":"device-booted"}', 2),
      { ...withPayload('{"type":"remind","slot":1}', 3, 'remind'), ts: now - 86_400 },
    ]} />)
    expect(screen.getAllByRole('button', { name: '查看事件详情' })).toHaveLength(1)
  })
})
