import { describe, expect, it } from 'vitest'
import { appTime, applicationResultSummary, applicationRunningState, bindingLabels, recordFieldLabel, scheduleSummary, scheduleZone } from '@/lib/application-plane'
import { isolationLabel } from '@/lib/plugins'
import { appBinding, appPresentation } from '@/test/application-plane'

describe('通用应用展示，不猜业务或设备', () => {
  it('未知机器字段不进入主视图，通用中文名称可复用', () => {
    expect(recordFieldLabel('count')).toBe('计数')
    expect(recordFieldLabel('opaque_domain_flag')).toBe('opaque_domain_flag')
    expect(recordFieldLabel('应用说明')).toBe('应用说明')
  })
  it('名称只取公开声明；重复的局部实体标识不能认成某一台设备', () => {
    expect(bindingLabels(appBinding, appPresentation)).toEqual({ entity: '入口信号', capability: '输入信号' })
    const other = { ...appPresentation.descriptors[0], device_id: 'node-b/device-b' }
    expect(bindingLabels(appBinding, { ...appPresentation, descriptors: [...appPresentation.descriptors, other] }).entity).toBeUndefined()
    expect(bindingLabels(appBinding, null)).toEqual({ entity: undefined, capability: '应用所需能力' })
  })
  it.each([
    ['* * * * *', '每分钟'], ['*/5 * * * *', '每小时内每隔 5 分钟'],
    ['0 * * * *', '每小时第 0 分钟'], ['30 8 * * *', '每天 08:30'],
    ['15 8 * * 1-5', '自定义时间规则'], ['*/60 * * * *', '自定义时间规则'],
    ['0 0 0 * * *', '自定义时间规则'], ['@every 1m', '自定义时间规则'],
  ])('时间表达式 %s 只在无歧义时解释', (cron, expected) => {
    expect(scheduleSummary(cron)).toBe(expected)
  })
  it('计划时间按计划声明的时区显示，坏时区不能白屏或假装本地时间', () => {
    const ts = Date.UTC(2026, 8, 6, 0, 30) / 1000
    expect(appTime(ts, 'Asia/Shanghai')).toContain('08:30')
    expect(appTime(ts, 'UTC')).toContain('00:30')
    expect(appTime(ts, 'invalid-zone')).toBe('时间信息不可用')
    expect(scheduleZone('invalid-zone')).toBe('时区信息不可用')
    expect(appTime(undefined)).toBe('尚无记录')
  })
  it('真实宿主隔离枚举在展示层有中文，未知值不篡改', () => {
    expect(isolationLabel('shared')).toBe('共享运行')
    expect(isolationLabel('per-instance')).toBe('独立运行')
    expect(isolationLabel('future-mode')).toBe('future-mode')
  })
  it('运行态来源冲突显式报冲突，不用后到的数据面覆盖观察态', () => {
    expect(applicationRunningState(true, 'running')).toBe('running')
    expect(applicationRunningState(false, 'stopped')).toBe('stopped')
    expect(applicationRunningState(true, 'stopped')).toBe('conflict')
    expect(applicationRunningState(false, 'running')).toBe('conflict')
    expect(applicationRunningState(undefined, 'running')).toBe('unknown')
    expect(applicationRunningState(true, undefined)).toBe('running')
    expect(applicationRunningState(false, 'unknown')).toBe('stopped')
  })
  it.each([
    ['ok', '成功'], ['succeeded', '成功'], ['failed', '失败'], ['pending', '处理中'], ['sent', '已发送'],
  ])('应用状态 %s 在主视图显示中文', (status, label) => {
    expect(applicationResultSummary({ status }).text).toBe(`状态 ${label}`)
  })
  it('未知应用状态不进入主视图，原文仍由结果原文承载', () => {
    expect(applicationResultSummary({ status: 'vendor_unknown' }).text).toBeUndefined()
  })
})
