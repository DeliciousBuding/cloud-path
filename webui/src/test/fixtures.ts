// 测试夹具 SSOT：形状严格对齐冻结契约
//   spec/descriptor.schema.json   ($id cloudpath.dev/descriptor/v1)
//   spec/capability.schema.json   ($id cloudpath.dev/capability/v1alpha1)
// 另外收纳两类「后端缺口」样本（契约宽容/后端尚未产出），用来验证前端归一化，而不是去改后端：
//   GAP-1 Capability action 未声明 command → 前端必须回落 action key
//   GAP-2 Descriptor 顶层 commands 扩展字段 → schema 未定义，前端允许消费
// 夹具里的设备语义（温度/继电器/时钟）只是样本数据，组件与断言都不认识这些名字。
import type { CapabilityDoc, DeviceDescriptor, DeviceView } from '@/lib/types'

export const CAP_TEMPERATURE = 'cloudpath.dev/capability/temperature@1'
export const CAP_RELAY = 'example.dev/capability/relay@2'
export const CAP_CLOCK = 'cloudpath.dev/capability/clock@1'
/** 被 Descriptor 引用、但 catalog 未收录的 Capability（未知能力回落路径） */
export const UNKNOWN_CAP = 'vendor.example/capability/mystery@3'

export const capTemperature: CapabilityDoc = {
  apiVersion: 'capabilities.cloudpath.dev/v1alpha1',
  kind: 'Capability',
  metadata: { id: CAP_TEMPERATURE, version: 1, title: '温度' },
  spec: {
    properties: {
      // 属性级 widget Hint 优先于 Capability 级 defaultWidget
      current: { type: 'number', unit: 'Cel', min: -40, max: 125, widget: 'gauge' },
      sensor_state: { type: 'string' },
    },
    presentation: { primaryProperty: 'current', defaultWidget: 'metric', tone: 'ok' },
  },
}

export const capRelay: CapabilityDoc = {
  apiVersion: 'capabilities.cloudpath.dev/v1alpha1',
  kind: 'Capability',
  metadata: { id: CAP_RELAY, version: 2, title: '继电器' },
  spec: {
    properties: { closed: { type: 'boolean', access: 'readwrite' } },
    presentation: { primaryProperty: 'closed', defaultWidget: 'boolean' },
    actions: {
      close: { title: '闭合', command: 'relay_on', primary: true, description: '接通负载' },
      open: { title: '断开', command: 'relay_off' },
      // GAP-1：没有 command 字段 → cmd 回落 action key `pulse`；inputSchema → 需要参数
      pulse: {
        title: '点动',
        description: '按毫秒脉冲',
        inputSchema: {
          type: 'object',
          properties: { ms: { type: 'integer' }, note: { type: 'string' } },
        },
      },
      factory_reset: {
        title: '恢复出厂',
        destructive: true,
        confirmation: '确认恢复出厂？设备侧配置将被清空。',
      },
    },
  },
}

export const capClock: CapabilityDoc = {
  apiVersion: 'capabilities.cloudpath.dev/v1alpha1',
  kind: 'Capability',
  metadata: { id: CAP_CLOCK, version: 1 },
  spec: {
    properties: { hour: { type: 'integer' }, minute: { type: 'integer' } },
    presentation: { primaryProperty: 'hour' },
    events: {
      'device.clock.drift': { title: '时钟漂移', description: '偏差超过阈值', tone: 'warn' },
    },
  },
}

/** 形状 B：扁平 Capability 文档（catalog 直发 {id,version,title,properties,…}） */
export const flatCapPayload = {
  id: 'cloudpath.dev/capability/humidity@1',
  version: 1,
  title: '湿度',
  properties: { relative: { type: 'number', unit: '%' } },
  presentation: { primaryProperty: 'relative' },
}

export const catalogPayload = {
  capabilities: [capTemperature, capRelay, capClock, flatCapPayload],
}

export function makeDescriptor(over: Partial<DeviceDescriptor> = {}): DeviceDescriptor {
  return {
    device_id: 'edge-1/dev-9',
    external_id: 'dev-9',
    manufacturer: 'Cloudpath',
    model: 'Demo Board',
    status: 'online',
    entities: [
      {
        entity_id: 'e-temp', unique_key: 'temp', name: '温度探针', category: 'sensor',
        capabilities: [CAP_TEMPERATURE, UNKNOWN_CAP],
        observations: {
          current: { capability: CAP_TEMPERATURE, property: 'current', value: 26.5, unit: 'Cel', quality: 'good' },
          sensor_state: { capability: CAP_TEMPERATURE, property: 'sensor_state', value: 'stable' },
          drift: { capability: CAP_TEMPERATURE, property: 'drift', value: 0.4, quality: 'uncertain' },
        },
      },
      {
        entity_id: 'e-relay', unique_key: 'relay', category: 'actuator', capabilities: [CAP_RELAY],
        observations: { closed: { capability: CAP_RELAY, property: 'closed', value: false } },
      },
      {
        entity_id: 'e-diag', unique_key: 'diag', category: 'diagnostic', capabilities: [UNKNOWN_CAP],
        observations: {
          mystery_rows: {
            capability: UNKNOWN_CAP, property: 'mystery_rows',
            value: [{ name: 'row-a', code: 7 }, { name: 'row-b', code: 9 }],
          },
          mystery_blob: {
            capability: UNKNOWN_CAP, property: 'mystery_blob',
            value: { nested: { a: [1, 2] }, flag: true },
          },
        },
      },
    ],
    ...over,
  }
}

/** GAP-2：Descriptor 顶层声明命令（schema 未定义该扩展字段，前端按宽容扩展消费） */
export function makeDescriptorWithRootCommands(): DeviceDescriptor {
  const d = makeDescriptor({ entities: [] })
  return {
    ...d,
    commands: [
      { command: 'reboot', title: '重启设备', destructive: true },
      'identify',
    ],
  } as DeviceDescriptor & { commands: unknown[] }
}

export function makeDeviceView(over: Partial<DeviceView> = {}): DeviceView {
  return {
    id: 'edge-1/dev-9',
    edge_id: 'edge-1',
    adapter: 'demo',
    name: '演示设备',
    port: 'COM5',
    online: true,
    state: {
      mode: 'idle',
      uptime_s: 3721,
      online: true,
      slots: [
        { label: 'A1', filled: true },
        { label: 'A2', filled: false },
      ],
      diag: { nested: { code: 12 } },
    },
    updated_at: 1_770_000_000,
    last_seen: 1_770_000_000,
    ...over,
  }
}

/** 归一化必须判为「不合法」（返回 null，调用方走通用回落）的载荷 */
export const invalidDescriptorPayloads: { name: string; payload: unknown }[] = [
  { name: 'null', payload: null },
  { name: '字符串', payload: 'descriptor' },
  { name: '空对象', payload: {} },
  { name: 'device_id 空串', payload: { device_id: '', status: 'online', entities: [] } },
]

/** 形状残缺但可救的载荷：归一化必须给出安全默认值而不是崩 */
export const degradedDescriptorPayloads: { name: string; payload: unknown; expect: (d: DeviceDescriptor) => void }[] = [
  {
    name: 'status 非法 → unavailable',
    payload: { device_id: 'a/b', status: 'weird', entities: [] },
    expect: (d) => { if (d.status !== 'unavailable') throw new Error(`status=${d.status}`) },
  },
  {
    name: 'category 非法 → sensor',
    payload: { device_id: 'a/b', entities: [{ entity_id: 'e', unique_key: 'k', category: 'robot', capabilities: [] }] },
    expect: (d) => { if (d.entities[0]?.category !== 'sensor') throw new Error('category 未回落') },
  },
  {
    name: 'entity 缺 entity_id → 丢弃该 entity',
    payload: { device_id: 'a/b', entities: [{ unique_key: '', category: 'sensor', capabilities: [] }] },
    expect: (d) => { if (d.entities.length !== 0) throw new Error('残缺 entity 未被丢弃') },
  },
  {
    name: 'observation 非法 quality → 丢弃 quality 字段',
    payload: {
      device_id: 'a/b',
      entities: [{
        entity_id: 'e', unique_key: 'k', category: 'sensor', capabilities: ['x/capability/y@1'],
        observations: { v: { capability: 'x/capability/y@1', property: 'v', value: 1, quality: 'perfect' } },
      }],
    },
    expect: (d) => {
      if (d.entities[0]?.observations?.v?.quality !== undefined) throw new Error('非法 quality 被采纳')
    },
  },
  {
    name: '缺 device_id → 回落 external_id',
    payload: { external_id: 'dev-9', status: 'online', entities: [] },
    expect: (d) => { if (d.device_id !== 'dev-9') throw new Error('device_id 未回落 external_id') },
  },
  {
    name: 'external_id 缺省 → 等于 device_id',
    payload: { device_id: 'edge/dev', entities: [] },
    expect: (d) => { if (d.external_id !== 'edge/dev') throw new Error('external_id 未回落') },
  },
]

/** Descriptor 载荷的包装形态（REST/WS 各家返回不一样，归一化都要吃下） */
export function wrappedDescriptorPayloads(d: DeviceDescriptor): { name: string; payload: unknown }[] {
  return [
    { name: '裸 Descriptor', payload: d },
    { name: '{descriptor}', payload: { descriptor: d } },
    { name: '{descriptors:[…]}', payload: { descriptors: [d] } },
    { name: '{items:[…]}', payload: { items: [d] } },
    { name: '数组根', payload: [d] },
    { name: '{descriptor,capabilities}', payload: { descriptor: d, capabilities: [capTemperature] } },
  ]
}