// lib/descriptor.ts 契约测试：归一化 / Capability 解析 / widget 推导 / 质量与状态语义 /
// 命令集推导 / 摘要与通用回落。断言只看「声明 → 展示模型」的映射，不含任何设备语义。
import { describe, expect, it } from 'vitest'
import {
  CATEGORY_LABEL, CATEGORY_ORDER, EMPTY_INDEX, QUALITY_LABEL,
  capabilityLabel, commandActions, entityTitle, eventDecl, formatTimestamp, formatValue,
  humanize, indexCapabilities, inferWidget, isScalar, normalizeCapabilityDocs,
  normalizeDescriptor, observationsOf, parseCapabilityRef, pickDescriptorFor,
  presentationOf, primaryObservation, qualityTone, rawRows, readInlineDescriptor,
  resolveCapability, statusMeta, summarizeRaw, toneFromHint, widgetFor,
} from '@/lib/descriptor'
import {
  CAP_CLOCK, CAP_RELAY, CAP_TEMPERATURE, UNKNOWN_CAP,
  capClock, capRelay, capTemperature, catalogPayload,
  degradedDescriptorPayloads, invalidDescriptorPayloads, makeDescriptor,
  makeDescriptorWithRootCommands, makeDeviceView, wrappedDescriptorPayloads,
} from '@/test/fixtures'
import type { CapabilityDoc, DescriptorEntity, DeviceDescriptor, Observation } from '@/lib/types'

const docs = normalizeCapabilityDocs(catalogPayload)
const idx = indexCapabilities(docs)

function obs(capability: string, property: string, value: unknown, extra: Partial<Observation> = {}): Observation {
  return { capability, property, value, ...extra }
}

/** GAP-2 的 commands 是 schema 未定义的宽容扩展，冻结类型里没有它；测试用 unknown 桥接，
 *  不去改 lib/types.ts 的契约类型（改契约需三处同步，不在本 lane 范围）。 */
function withRootCommands(commands: unknown[]): DeviceDescriptor {
  return { ...makeDescriptor({ entities: [] }), commands } as unknown as DeviceDescriptor
}

function entity(over: Partial<DescriptorEntity> = {}): DescriptorEntity {
  return {
    entity_id: 'e', unique_key: 'k', category: 'sensor', capabilities: [], ...over,
  }
}

describe('normalizeDescriptor：宽容归一化', () => {
  it('合法 Descriptor 原样通过（契约必填四字段）', () => {
    const d = normalizeDescriptor(makeDescriptor())
    expect(d).not.toBeNull()
    expect(d?.device_id).toBe('edge-1/dev-9')
    expect(d?.external_id).toBe('dev-9')
    expect(d?.status).toBe('online')
    expect(d?.entities).toHaveLength(3)
    expect(d?.manufacturer).toBe('Cloudpath')
    expect(d?.model).toBe('Demo Board')
  })

  it.each(wrappedDescriptorPayloads(makeDescriptor()))('包装形态 $name 都能拆出同一份 Descriptor', ({ payload }) => {
    expect(normalizeDescriptor(payload)?.device_id).toBe('edge-1/dev-9')
  })

  it.each(invalidDescriptorPayloads)('不合法载荷 $name → null（调用方走通用回落）', ({ payload }) => {
    expect(normalizeDescriptor(payload)).toBeNull()
  })

  it.each(degradedDescriptorPayloads)('残缺载荷 $name → 安全默认值', ({ payload, expect: check }) => {
    const d = normalizeDescriptor(payload)
    expect(d).not.toBeNull()
    expect(() => check(d as DeviceDescriptor)).not.toThrow()
  })

  it('观测的 capability 缺省时回落 Entity 首个 Capability，property 缺省时用键名', () => {
    const d = normalizeDescriptor({
      device_id: 'a/b',
      entities: [{
        entity_id: 'e', unique_key: 'k', category: 'sensor', capabilities: [CAP_TEMPERATURE],
        observations: { current: { value: 21 } },
      }],
    })
    const o = d?.entities[0]?.observations?.current
    expect(o).toMatchObject({ capability: CAP_TEMPERATURE, property: 'current', value: 21 })
  })

  it('观测缺 value 字段但有 capability → value 归一为 null（不是丢弃）', () => {
    const d = normalizeDescriptor({
      device_id: 'a/b',
      entities: [{
        entity_id: 'e', unique_key: 'k', category: 'sensor', capabilities: [],
        observations: { x: { capability: UNKNOWN_CAP, property: 'x' } },
      }],
    })
    expect(d?.entities[0]?.observations?.x?.value).toBeNull()
  })
})

describe('Descriptor 定位与嗅探', () => {
  const d = makeDescriptor()

  it('readInlineDescriptor：state / state.raw 内联都能嗅到，普通 DeviceView 返回 null', () => {
    expect(readInlineDescriptor({ state: { descriptor: d } })?.device_id).toBe('edge-1/dev-9')
    expect(readInlineDescriptor({ state: { raw: { device_descriptor: d } } })?.device_id).toBe('edge-1/dev-9')
    expect(readInlineDescriptor(makeDeviceView())).toBeNull()
    expect(readInlineDescriptor(null)).toBeNull()
  })

  it('pickDescriptorFor：device_id 全键 / 短 ID / external_id 三种命中方式', () => {
    const bulk = { descriptors: [makeDescriptor({ device_id: 'other/x', external_id: 'other-x' }), d] }
    expect(pickDescriptorFor(bulk, 'edge-1', 'dev-9')?.device_id).toBe('edge-1/dev-9')
    expect(pickDescriptorFor([makeDescriptor({ device_id: 'dev-9' })], 'edge-1', 'dev-9')?.device_id).toBe('dev-9')
    expect(pickDescriptorFor({ items: [d] }, 'edge-1', 'dev-9')?.external_id).toBe('dev-9')
    expect(pickDescriptorFor({ descriptors: [d] }, 'edge-2', 'nope')).toBeNull()
  })
})

describe('Capability 引用解析与索引', () => {
  it('parseCapabilityRef 覆盖全 ID / 无版本 / 裸名 / 命名空间无 capability 段', () => {
    expect(parseCapabilityRef('cloudpath.dev/capability/temperature@1'))
      .toEqual({ raw: 'cloudpath.dev/capability/temperature@1', namespace: 'cloudpath.dev/capability', name: 'temperature', version: 1 })
    expect(parseCapabilityRef('temperature')).toMatchObject({ namespace: '', name: 'temperature', version: null })
    expect(parseCapabilityRef('temperature@12')).toMatchObject({ name: 'temperature', version: 12 })
    expect(parseCapabilityRef('io.github.owner/relay')).toMatchObject({ namespace: 'io.github.owner', name: 'relay', version: null })
    expect(parseCapabilityRef('a/b/capability/x@notanumber')).toMatchObject({ name: 'x', version: null })
  })

  it('catalog 归一化接受 数组 / {capabilities} / {items} / 映射 / 扁平文档，并按 id 去重', () => {
    expect(normalizeCapabilityDocs(catalogPayload)).toHaveLength(4)
    expect(normalizeCapabilityDocs([capTemperature, capTemperature])).toHaveLength(1)
    expect(normalizeCapabilityDocs({ items: [capRelay] })).toHaveLength(1)
    expect(normalizeCapabilityDocs({ [CAP_CLOCK]: capClock })).toHaveLength(1)
    expect(normalizeCapabilityDocs({ capabilities: [] })).toHaveLength(0)
    expect(normalizeCapabilityDocs('nope')).toHaveLength(0)
  })

  it('扁平 Capability 文档（形状 B）被提升为 metadata/spec 结构', () => {
    const flat = normalizeCapabilityDocs([catalogPayload.capabilities[3]])[0] as CapabilityDoc
    expect(flat.metadata).toMatchObject({ id: 'cloudpath.dev/capability/humidity@1', version: 1, title: '湿度' })
    expect(flat.spec?.presentation?.primaryProperty).toBe('relative')
    expect(flat.spec?.properties?.relative?.unit).toBe('%')
  })

  it('resolveCapability：全 ID / 去版本 ID / 裸名都命中，未收录返回 undefined', () => {
    expect(resolveCapability(CAP_TEMPERATURE, idx)?.metadata.title).toBe('温度')
    expect(resolveCapability('cloudpath.dev/capability/temperature', idx)?.metadata.title).toBe('温度')
    expect(resolveCapability('cloudpath.dev/capability/temperature@99', idx)?.metadata.title).toBe('温度')
    expect(resolveCapability('temperature', idx)?.metadata.title).toBe('温度')
    expect(resolveCapability(UNKNOWN_CAP, idx)).toBeUndefined()
    expect(resolveCapability(undefined, idx)).toBeUndefined()
    expect(resolveCapability(CAP_TEMPERATURE, EMPTY_INDEX)).toBeUndefined()
  })

  it('capabilityLabel：文档 title > 平台通用词汇 > humanize，无引用给「未声明」', () => {
    expect(capabilityLabel(CAP_TEMPERATURE, idx)).toBe('温度')
    expect(capabilityLabel(UNKNOWN_CAP, idx)).toBe('Mystery')
    // 未收录但属通用硬件名词 → 平台词汇层命中（不再甩英文 humanize）
    expect(capabilityLabel(CAP_CLOCK, idx)).toBe('时钟')
    expect(capabilityLabel(undefined, idx)).toBe('未声明')
  })

  it('capabilityLabel locale 匹配：英文 title 让位平台词汇，中文 title 优先', () => {
    const enDoc = {
      metadata: { id: 'cloudpath.dev/capability/temperature@1', version: 1, title: 'Temperature' },
      spec: {},
    } as unknown as CapabilityDoc
    const zhDoc = {
      metadata: { id: 'cloudpath.dev/capability/temperature@1', version: 1, title: '温度探针能力' },
      spec: {},
    } as unknown as CapabilityDoc
    expect(capabilityLabel('cloudpath.dev/capability/temperature@1', indexCapabilities([enDoc]))).toBe('温度')
    expect(capabilityLabel('cloudpath.dev/capability/temperature@1', indexCapabilities([zhDoc]))).toBe('温度探针能力')
  })

  it('humanize：kebab / snake / camel / 全大写长短词', () => {
    expect(humanize('display-text')).toBe('Display Text')
    expect(humanize('display_text')).toBe('Display Text')
    expect(humanize('displayText')).toBe('Display Text')
    expect(humanize('TAKEN-LATE')).toBe('Taken Late')
    expect(humanize('ISP')).toBe('ISP')
    expect(humanize('')).toBe('')
  })
})

describe('Entity / Observation 读取', () => {
  it('observationsOf 按 (capability, property) 稳定排序并去重', () => {
    const list = observationsOf(makeDescriptor().entities[0] as DescriptorEntity)
    expect(list.map((o) => o.property)).toEqual(['current', 'drift', 'sensor_state'])
  })

  it('primaryObservation：presentation.primaryProperty 命中优先于排序首位', () => {
    const e = entity({
      capabilities: [CAP_CLOCK],
      observations: {
        minute: obs(CAP_CLOCK, 'minute', 5),
        hour: obs(CAP_CLOCK, 'hour', 8),
      },
    })
    expect(primaryObservation(e, idx)?.property).toBe('hour')
    // 声明改成 minute 后，主观测随之改变（证明是声明驱动而非字段名猜测）
    const moved: CapabilityDoc = {
      ...capClock,
      spec: { ...capClock.spec, presentation: { primaryProperty: 'minute' } },
    }
    const idx2 = indexCapabilities([moved])
    expect(primaryObservation(e, idx2)?.property).toBe('minute')
    expect(primaryObservation(e, EMPTY_INDEX)?.property).toBe('hour')
  })

  it('primaryObservation：无标量观测时回落首条，空观测返回 undefined', () => {
    const arrOnly = entity({ observations: { rows: obs('x/capability/y@1', 'rows', [{ a: 1 }]) } })
    expect(primaryObservation(arrOnly, idx)?.property).toBe('rows')
    expect(primaryObservation(entity({ observations: {} }), idx)).toBeUndefined()
  })

  it('entityTitle / 分类词汇来自平台枚举，不是设备语义', () => {
    expect(entityTitle(entity({ name: '温度探针', unique_key: 'temp' }))).toBe('温度探针')
    expect(entityTitle(entity({ unique_key: 'diag_board' }))).toBe('Diag Board')
    expect(CATEGORY_ORDER).toEqual(['sensor', 'actuator', 'diagnostic', 'config'])
    expect(CATEGORY_LABEL.sensor).toBe('传感器')
  })

  it('isScalar 判定开放值类型', () => {
    expect([isScalar(1), isScalar('a'), isScalar(true), isScalar(null)]).toEqual([true, true, true, true])
    expect([isScalar(undefined), isScalar([]), isScalar({})]).toEqual([false, false, false])
  })
})

describe('widget 推导（presentation 是 Hint，不是语义真相）', () => {
  it('属性级 widget 优先于 Capability 级 defaultWidget', () => {
    expect(widgetFor(obs(CAP_TEMPERATURE, 'current', 26.5), idx)).toBe('gauge')
  })

  it('Capability 级 defaultWidget 只作用于 primaryProperty，次要属性不被误伤', () => {
    expect(widgetFor(obs(CAP_TEMPERATURE, 'sensor_state', 'stable'), idx)).toBe('text')
    expect(widgetFor(obs(CAP_RELAY, 'closed', false), idx)).toBe('boolean')
  })

  it('未声明 primaryProperty 时 Capability 级 defaultWidget 对全部属性生效', () => {
    const doc: CapabilityDoc = {
      metadata: { id: 'x/capability/badgeall@1', version: 1 },
      spec: { presentation: { defaultWidget: 'badge' } },
    }
    const i = indexCapabilities([doc])
    expect(widgetFor(obs('x/capability/badgeall@1', 'any', 3), i)).toBe('badge')
  })

  it('未知/非法 Hint 被忽略，回落值类型推导', () => {
    const doc: CapabilityDoc = {
      metadata: { id: 'x/capability/bogus@1', version: 1 },
      spec: { properties: { v: { widget: 'sparkle' } }, presentation: { defaultWidget: 'not-a-widget' } },
    }
    const i = indexCapabilities([doc])
    expect(widgetFor(obs('x/capability/bogus@1', 'v', 12), i)).toBe('number')
    expect(widgetFor(obs(UNKNOWN_CAP, 'whatever', 12), idx)).toBe('number')
  })

  it('常见别名归一（dial→gauge、percent→progress、clock→timestamp、rows→table…）', () => {
    const mk = (widget: string): CapabilityDoc => ({
      metadata: { id: `x/capability/${widget}@1`, version: 1 },
      spec: { properties: { v: { widget } } },
    })
    const i = indexCapabilities(['dial', 'percent', 'clock', 'rows', 'chips', 'pill', 'code', 'object']
      .map((w) => mk(w)))
    const cases: [string, string][] = [
      ['dial', 'gauge'], ['percent', 'progress'], ['clock', 'timestamp'], ['rows', 'table'],
      ['chips', 'list'], ['pill', 'badge'], ['code', 'json'], ['object', 'table'],
    ]
    for (const [hint, want] of cases) {
      expect(widgetFor(obs(`x/capability/${hint}@1`, 'v', 'x'), i)).toBe(want)
    }
  })

  it('inferWidget：未知结构一律落表格 / JSON，不会白屏', () => {
    expect(inferWidget(1)).toBe('number')
    expect(inferWidget(true)).toBe('boolean')
    expect(inferWidget('2026-09-03T10:00:00Z')).toBe('timestamp')
    expect(inferWidget('hello')).toBe('text')
    expect(inferWidget(null)).toBe('text')
    expect(inferWidget([1, 'a', null])).toBe('list')
    expect(inferWidget([{ a: 1 }])).toBe('table')
    expect(inferWidget({ a: 1 })).toBe('table')
  })
})

describe('值格式化与语义色', () => {
  it('formatValue：空值破折号、布尔中文、数组计数、对象挑显示字段', () => {
    expect(formatValue(null)).toBe('—')
    expect(formatValue('')).toBe('—')
    expect(formatValue(Number.NaN)).toBe('—')
    expect(formatValue(26.5)).toBe('26.5')
    expect(formatValue(1234.5)).toBe('1,234.5')
    expect(formatValue(true)).toBe('是')
    expect(formatValue(false)).toBe('否')
    expect(formatValue([1, 2, 3])).toBe('3 项')
    expect(formatValue({ label: 'ok' })).toBe('ok')
    expect(formatValue({ nested: { a: 1 } })).toBe('JSON')
  })

  it('formatTimestamp：有效时间本地化，无效原样，非时间值走 formatValue', () => {
    const out = formatTimestamp('2026-09-03T10:00:00Z')
    expect(out).not.toBe('2026-09-03T10:00:00Z')
    expect(out).toContain('2026')
    expect(formatTimestamp('not-a-time')).toBe('not-a-time')
    expect(formatTimestamp(null)).toBe('—')
  })

  it('quality → 语义色与中文标签（good 不打扰）', () => {
    expect(qualityTone('good')).toBe('ok')
    expect(qualityTone('uncertain')).toBe('warn')
    expect(qualityTone('bad')).toBe('bad')
    expect(qualityTone('unavailable')).toBe('idle')
    expect(qualityTone(undefined)).toBe('idle')
    expect(QUALITY_LABEL).toEqual({ good: '良好', uncertain: '不确定', bad: '异常', unavailable: '不可用' })
  })

  it('statusMeta 覆盖四个契约状态与未知回落', () => {
    expect(statusMeta('online')).toEqual({ label: '在线', tone: 'ok', online: true })
    expect(statusMeta('degraded')).toEqual({ label: '降级', tone: 'warn', online: true })
    expect(statusMeta('offline')).toEqual({ label: '离线', tone: 'idle', online: false })
    expect(statusMeta('unavailable')).toEqual({ label: '不可用', tone: 'bad', online: false })
    expect(statusMeta(undefined)).toEqual({ label: '未知', tone: 'idle', online: false })
  })

  it('toneFromHint 只采纳合法 Tone（UI Hint 是不可信输入）', () => {
    expect(toneFromHint({ tone: 'bad' })).toBe('bad')
    expect(toneFromHint({ tone: 'hot-pink' })).toBeUndefined()
    expect(toneFromHint(undefined)).toBeUndefined()
  })

  it('presentationOf 只读声明，未收录返回 undefined', () => {
    expect(presentationOf(CAP_TEMPERATURE, idx)?.primaryProperty).toBe('current')
    expect(presentationOf(UNKNOWN_CAP, idx)).toBeUndefined()
  })
})

describe('commandActions：命令集只来自声明', () => {
  const d = makeDescriptor()

  it('Capability actions → 命令、文案、变体、确认文案、来源 Entity', () => {
    const set = commandActions({ descriptor: d, index: idx })
    expect(set.source).toBe('descriptor')
    const byCmd = Object.fromEntries(set.actions.map((a) => [a.cmd, a]))
    expect(byCmd.relay_on).toMatchObject({ label: '闭合', variant: 'primary', hint: '接通负载', capability: CAP_RELAY, entityId: 'e-relay', entityLabel: 'Relay' })
    expect(byCmd.relay_off).toMatchObject({ label: '断开', variant: 'ghost' })
    expect(byCmd.factory_reset).toMatchObject({ label: '恢复出厂', variant: 'danger', confirmText: '确认恢复出厂？设备侧配置将被清空。' })
  })

  it('GAP-1：action 未声明 command → cmd 回落 action key；inputSchema → needsInput + 参数模板', () => {
    const pulse = commandActions({ descriptor: d, index: idx }).actions.find((a) => a.cmd === 'pulse')
    expect(pulse).toMatchObject({ label: '点动', needsInput: true, inputPlaceholder: '{"ms":0,"note":""}', inputMaxLength: 64 })
  })

  it('GAP-2：Descriptor 顶层 commands 扩展（对象与裸字符串都接受），破坏性动作自动生成确认文案', () => {
    const set = commandActions({ descriptor: makeDescriptorWithRootCommands(), index: idx })
    expect(set.source).toBe('descriptor')
    expect(set.actions[0]).toMatchObject({ cmd: 'reboot', label: '重启设备', variant: 'danger' })
    expect(set.actions[0]?.confirmText).toContain('确认执行「重启设备」')
    // 裸字符串命令不带任何 UI Hint（variant 由 UI 侧按 ghost 默认渲染）
    expect(set.actions[1]).toMatchObject({ cmd: 'identify', label: 'Identify' })
    expect(set.actions[1]?.variant).toBeUndefined()
  })

  it('Descriptor 无可下发动作时回落适配器白名单（后端事实源），source=adapter', () => {
    const noActions = makeDescriptor({ entities: [] })
    const set = commandActions({ descriptor: noActions, index: idx, adapterCommands: ['raw', 'query_state'] })
    expect(set.source).toBe('adapter')
    expect(set.actions.map((a) => a.cmd)).toEqual(['raw', 'query_state'])
    expect(set.actions[1]?.label).toBe('Query State')
  })

  it('既无声明也无白名单 → 空命令集 source=none（UI 显示等待声明）', () => {
    expect(commandActions({ descriptor: null, index: idx })).toEqual({ actions: [], source: 'none' })
    expect(commandActions({ descriptor: makeDescriptor({ entities: [] }), index: EMPTY_INDEX }).source).toBe('none')
  })

  it('同一 cmd 去重，且动作数量有上限（防声明爆炸撑破面板）', () => {
    const dup = withRootCommands([{ command: 'raw' }, 'raw', { cmd: 'raw' }])
    expect(commandActions({ descriptor: dup, index: idx, adapterCommands: ['raw'] }).actions).toHaveLength(1)

    const many = withRootCommands(Array.from({ length: 25 }, (_, i) => `cmd_${i}`))
    expect(commandActions({ descriptor: many, index: idx }).actions).toHaveLength(16)
  })
})

describe('摘要与通用回落（Descriptor 缺席时不白屏）', () => {
  it('summarizeRaw：标量成主值/胶囊，对象数组成分组，嵌套对象被跳过', () => {
    const s = summarizeRaw(makeDeviceView().state)
    expect(s.source).toBe('raw')
    expect(s.primary).toMatchObject({ label: 'Mode', text: 'idle' })
    expect(s.chips.map((c) => c.text)).toEqual(['3,721', '是'])
    expect(s.groups[0]?.label).toBe('Slots')
    expect(s.groups[0]?.items.map((i) => i.text)).toEqual(['A1', 'A2'])
  })

  it('rawRows：每个上报字段一行，widget 由值类型推导', () => {
    const rows = rawRows(makeDeviceView().state)
    expect(rows.map((r) => [r.key, r.widget])).toEqual([
      ['mode', 'text'], ['uptime_s', 'number'], ['online', 'boolean'], ['slots', 'table'], ['diag', 'table'],
    ])
    expect(rawRows(undefined)).toEqual([])
  })

  it('eventDecl：只采纳 Capability 声明的事件标题与语义色，未收录返回 undefined', () => {
    expect(eventDecl('device.clock.drift', idx)).toEqual({
      title: '时钟漂移', description: '偏差超过阈值', tone: 'warn',
    })
    expect(eventDecl('device.unknown.thing', idx)).toBeUndefined()
    expect(eventDecl('device.clock.drift', EMPTY_INDEX)).toBeUndefined()
  })
})