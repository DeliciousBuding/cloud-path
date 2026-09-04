// Descriptor / Capability 消费层（纯函数，无 React、无副作用）。
// 契约事实源（只读）：spec/descriptor.schema.json、spec/capability.schema.json，
// 语义说明：docs/architecture/capability-model.md（§3 presentation、§9 未知 Capability 回落）。
//
// 本文件的存在意义：设备语义（时钟/分格/提醒…）不写进组件，组件只问这里要
// 「主值 / 胶囊 / 分组 / 命令集 / 渲染 widget」，全部由 Descriptor + Capability 声明推导。
import type { Tone } from '@/components/ui'
import type {
  CapabilityActionDecl, CapabilityDoc, CapabilityPresentation,
  DeviceDescriptor, DeviceRaw, DeviceStatus, DescriptorEntity, EntityCategory,
  Observation, ObservationQuality,
} from './types'

/* ---------------- 基础字符串工具 ---------------- */

/** `display-text` / `displayText` / `display_text` → `Display Text`（UI 回落文案，非语义） */
export function humanize(s: string): string {
  const words = s
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .split(/[\s_.\-/@:]+/)
    .filter(Boolean)
  return words.map((w) => {
    // 全大写长词按标题化（TAKEN-LATE → Taken Late）；短全大写视为缩写原样保留（ISP / RX）
    const acronym = w.length <= 3 && w === w.toUpperCase()
    const shouted = !acronym && w.length > 1 && w === w.toUpperCase() && /[A-Z]/.test(w)
    const body = shouted ? w.slice(1).toLowerCase() : w.slice(1)
    return w.charAt(0).toUpperCase() + body
  }).join(' ') || s
}

/** 平台级属性展示词典（声明驱动的回退层）：Capability 文档 spec.properties[].title 缺席时，
 *  把常见属性机器名收敛为中文；未知属性仍回落 humanize——不猜业务语义。
 *  机器 ID（property key）本身永不本地化，这里只是展示别名（docs/architecture/capability-model.md §9）。 */
const PROPERTY_LABEL: Record<string, string> = {
  raw: '原始值', value: '数值', state: '状态', time: '时间', direction: '方向',
  mask: '掩码', mode: '模式', level: '设定值', enabled: '开关', count: '计数',
  status: '状态',
  commands: '命令数', pings: 'Ping 计数', ticks: '心跳计数', uptime_s: '运行时长',
}

/** 平台通用词汇表（展示回退第二层）：常见硬件名词的中文展示别名。
 *  只收跨设备通用的硬件/能力名词，不收业务语义；声明 title 永远优先，
 *  机器 ID 本身永不本地化（docs/architecture/capability-model.md §9）。 */
const GENERIC_NOUN: Record<string, string> = {
  clock: '时钟', temperature: '温度', humidity: '湿度', illuminance: '光照',
  'analog-input': '模拟输入', navigation: '导航', hall: '霍尔', vibration: '振动',
  key: '按键', buzzer: '蜂鸣器', led: 'LED 灯组', 'led-bank': 'LED 灯组',
  display: '数码管', 'display-text': '数码管显示', motor: '电机',
  diagnostics: '诊断', 'board-diagnostics': '板级诊断', relay: '继电器', switch: '开关',
  counter: '计数器', uptime: '运行时长', setpoint: '设定值', toggle: '开关',
}

/** UI  locale 匹配：声明 title 只有含中文才视为「已本地化的展示名」；
 *  纯英文 title（通常只是机器名的标题化）让位给平台通用词汇层——
 *  这是 i18n 的 locale 选择，不是覆盖声明：声明者日后给出中文 title 即自动优先。 */
const CJK_RE = /[\u3400-\u9fff]/
function localizedTitle(t: string | undefined): string | undefined {
  return t && CJK_RE.test(t) ? t : undefined
}

/** 属性展示名：文档声明 title > 平台词典 > humanize(机器名) */
export function propertyLabel(name: string, ref?: string, idx: CapabilityIndex = EMPTY_INDEX): string {
  const doc = ref ? resolveCapability(ref, idx) : undefined
  const decl = doc?.spec?.properties?.[name] as { title?: string } | undefined
  const title = str(decl?.title)
  return localizedTitle(title) ?? PROPERTY_LABEL[name] ?? title ?? humanize(name)
}
/** 平台级命令展示词典（声明缺席时的回退层）：机器 cmd → 中文；未知命令仍回落 humanize。
 *  命令白名单与文案的事实源始终是后端声明（Capability actions / Descriptor commands /
 *  适配器白名单），这里只做展示别名，不猜业务语义。 */
const CMD_LABEL: Record<string, string> = {
  buzzer: '蜂鸣器', led: 'LED 灯组', display: '数码管显示', motor: '电机',
  sync: '对时', sensor: '读取传感器', state: '状态读取', diag: '板级诊断',
  isp: '进入 ISP 下载', raw: '原始命令',
}

/** 命令展示名（无声明上下文时）：平台词典 > humanize(机器名) */
export function commandLabel(cmd: string): string {
  return CMD_LABEL[cmd] ?? humanize(cmd)
}

function str(v: unknown): string | undefined {
  return typeof v === 'string' && v.length > 0 ? v : undefined
}

function obj(v: unknown): Record<string, unknown> | null {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null
}

/* ---------------- Capability 引用解析 ---------------- */

export interface CapabilityRef {
  /** 原始引用串 */
  raw: string
  /** 命名空间（如 cloudpath.dev / io.github.owner） */
  namespace: string
  /** 能力名（如 temperature） */
  name: string
  /** 版本；无 @n 时为 null */
  version: number | null
}

/** 解析 `<ns>/capability/<name>@<ver>`；也宽容 `<name>@<ver>`、`<name>`、`<ns>/<name>` */
export function parseCapabilityRef(ref: string): CapabilityRef {
  const raw = ref.trim()
  const at = raw.lastIndexOf('@')
  const base = at > 0 ? raw.slice(0, at) : raw
  const verText = at > 0 ? raw.slice(at + 1) : ''
  const version = /^\d+$/.test(verText) ? Number(verText) : null
  const seg = base.split('/')
  const name = seg[seg.length - 1] ?? base
  const namespace = seg.length > 1 ? seg.slice(0, -1).join('/') : ''
  return { raw, namespace, name, version }
}

/** Capability 索引：全 ID / 无版本 ID / 裸名 三种键都能命中（Descriptor 的引用写法不统一时仍可解析） */
export interface CapabilityIndex {
  byId: Map<string, CapabilityDoc>
  byName: Map<string, CapabilityDoc>
  docs: CapabilityDoc[]
}

export function indexCapabilities(docs: CapabilityDoc[]): CapabilityIndex {
  const byId = new Map<string, CapabilityDoc>()
  const byName = new Map<string, CapabilityDoc>()
  for (const d of docs) {
    const id = d.metadata?.id
    if (!id) continue
    if (!byId.has(id)) byId.set(id, d)
    const ref = parseCapabilityRef(id)
    const bare = ref.namespace ? `${ref.namespace}/${ref.name}` : ref.name
    if (!byId.has(bare)) byId.set(bare, d)
    if (!byName.has(ref.name)) byName.set(ref.name, d)
  }
  return { byId, byName, docs }
}

export const EMPTY_INDEX: CapabilityIndex = { byId: new Map(), byName: new Map(), docs: [] }

export function resolveCapability(ref: string | undefined, idx: CapabilityIndex): CapabilityDoc | undefined {
  if (!ref) return undefined
  const hit = idx.byId.get(ref)
  if (hit) return hit
  const parsed = parseCapabilityRef(ref)
  const bare = parsed.namespace ? `${parsed.namespace}/${parsed.name}` : parsed.name
  return idx.byId.get(bare) ?? idx.byName.get(parsed.name)
}

/** 展示名：Capability 文档 title 优先，其次 humanize(能力名)——机器 ID 永不本地化 */
export function capabilityLabel(ref: string | undefined, idx: CapabilityIndex = EMPTY_INDEX): string {
  if (!ref) return '未声明'
  const doc = resolveCapability(ref, idx)
  const parsed = parseCapabilityRef(ref)
  const title = str(doc?.metadata?.title)
  return localizedTitle(title) ?? GENERIC_NOUN[parsed.name] ?? title ?? humanize(parsed.name || ref)
}

/* ---------------- 宽容归一化（后端形状可能演化，前端不炸） ---------------- */

function unwrapDescriptorRoot(input: unknown): Record<string, unknown> | null {
  if (Array.isArray(input)) return unwrapDescriptorRoot(input[0])
  const o = obj(input)
  if (!o) return null
  if (o.descriptor) return unwrapDescriptorRoot(o.descriptor)
  if (Array.isArray(o.descriptors)) return unwrapDescriptorRoot(o.descriptors[0])
  if (Array.isArray(o.items)) return unwrapDescriptorRoot(o.items[0])
  return o
}

function normalizeObservation(key: string, v: unknown, fallbackCapability: string): Observation | null {
  const o = obj(v)
  if (!o) return null
  const capability = str(o.capability) ?? fallbackCapability
  const property = str(o.property) ?? key
  if (!('value' in o) && !capability) return null
  const out: Observation = { capability, property, value: 'value' in o ? o.value : null }
  const unit = str(o.unit); if (unit) out.unit = unit
  const q = str(o.quality) as ObservationQuality | undefined
  if (q === 'good' || q === 'uncertain' || q === 'bad' || q === 'unavailable') out.quality = q
  const oa = str(o.observed_at); if (oa) out.observed_at = oa
  const ra = str(o.received_at); if (ra) out.received_at = ra
  if (typeof o.sequence === 'number') out.sequence = o.sequence
  return out
}

function normalizeEntity(v: unknown): DescriptorEntity | null {
  const o = obj(v)
  if (!o) return null
  const entityId = str(o.entity_id) ?? str(o.unique_key)
  if (!entityId) return null
  const uniqueKey = str(o.unique_key) ?? entityId
  const rawCaps = Array.isArray(o.capabilities) ? o.capabilities : []
  const capabilities = rawCaps.filter((c): c is string => typeof c === 'string')
  const categoryRaw = str(o.category) as EntityCategory | undefined
  const category: EntityCategory =
    categoryRaw === 'sensor' || categoryRaw === 'actuator' || categoryRaw === 'diagnostic' || categoryRaw === 'config'
      ? categoryRaw : 'sensor'
  const e: DescriptorEntity = { entity_id: entityId, unique_key: uniqueKey, category, capabilities }
  const name = str(o.name); if (name) e.name = name
  const obsSrc = obj(o.observations)
  if (obsSrc) {
    const observations: Record<string, Observation> = {}
    for (const [k, val] of Object.entries(obsSrc)) {
      const obs = normalizeObservation(k, val, capabilities[0] ?? '')
      if (obs) observations[k] = obs
    }
    if (Object.keys(observations).length) e.observations = observations
  }
  return e
}

/** 把任意后端载荷收敛为合法 DeviceDescriptor；不合法返回 null（调用方走通用回落） */
export function normalizeDescriptor(input: unknown): DeviceDescriptor | null {
  const o = unwrapDescriptorRoot(input)
  if (!o) return null
  const deviceId = str(o.device_id) ?? str(o.external_id) ?? str(o.id)
  if (!deviceId) return null
  const statusRaw = str(o.status) as DeviceStatus | undefined
  const status: DeviceStatus =
    statusRaw === 'online' || statusRaw === 'offline' || statusRaw === 'unavailable' || statusRaw === 'degraded'
      ? statusRaw : 'unavailable'
  const rawEntities = Array.isArray(o.entities) ? o.entities : []
  const entities = rawEntities.map(normalizeEntity).filter((e): e is DescriptorEntity => e !== null)
  const d: DeviceDescriptor = {
    device_id: deviceId,
    external_id: str(o.external_id) ?? deviceId,
    status,
    entities,
  }
  const mfr = str(o.manufacturer); if (mfr) d.manufacturer = mfr
  const model = str(o.model); if (model) d.model = model
  return d
}

function normalizeCapabilityDoc(v: unknown): CapabilityDoc | null {
  const o = obj(v)
  if (!o) return null
  // 形状 A：完整文档 {apiVersion,kind,metadata:{id,version},spec:{...}}
  const meta = obj(o.metadata)
  const spec = obj(o.spec)
  if (meta && str(meta.id)) {
    const doc: CapabilityDoc = {
      metadata: {
        id: str(meta.id) as string,
        version: typeof meta.version === 'number' ? meta.version : (parseCapabilityRef(str(meta.id) as string).version ?? 1),
      },
    }
    const title = str(meta.title); if (title) doc.metadata.title = title
    if (spec) doc.spec = spec as CapabilityDoc['spec']
    if (str(o.apiVersion)) doc.apiVersion = str(o.apiVersion)
    if (str(o.kind)) doc.kind = str(o.kind)
    return doc
  }
  // 形状 B：扁平 {id,version,title,properties,actions,events,presentation}
  const id = str(o.id) ?? str(o.capability)
  if (!id) return null
  const doc: CapabilityDoc = {
    metadata: { id, version: typeof o.version === 'number' ? o.version : (parseCapabilityRef(id).version ?? 1) },
  }
  const title = str(o.title); if (title) doc.metadata.title = title
  const flatSpec: Record<string, unknown> = {}
  for (const k of ['properties', 'events', 'actions', 'presentation'] as const) {
    const sub = obj(o[k]); if (sub) flatSpec[k] = sub
  }
  if (Object.keys(flatSpec).length) doc.spec = flatSpec as CapabilityDoc['spec']
  return doc
}

/** Capability catalog 归一化：数组 / {capabilities:[]} / {items:[]} / {id: doc} 映射都接受 */
export function normalizeCapabilityDocs(input: unknown): CapabilityDoc[] {
  const list: unknown[] = []
  if (Array.isArray(input)) list.push(...input)
  else {
    const o = obj(input)
    if (o) {
      if (Array.isArray(o.capabilities)) list.push(...o.capabilities)
      else if (Array.isArray(o.items)) list.push(...o.items)
      else if (Array.isArray(o.data)) list.push(...o.data)
      else list.push(...Object.values(o))
    }
  }
  const out: CapabilityDoc[] = []
  const seen = new Set<string>()
  for (const one of list) {
    const doc = normalizeCapabilityDoc(one)
    if (!doc || seen.has(doc.metadata.id)) continue
    seen.add(doc.metadata.id)
    out.push(doc)
  }
  return out
}

/** 从批量载荷里挑出属于某设备的那份 Descriptor（device_id 可能是短 ID 或 "<edge>/<dev>"） */
export function pickDescriptorFor(input: unknown, edgeId: string, devId: string): DeviceDescriptor | null {
  const fullKey = `${edgeId}/${devId}`
  const candidates: unknown[] = []
  if (Array.isArray(input)) candidates.push(...input)
  else {
    const o = obj(input)
    if (o) {
      for (const k of ['descriptors', 'items', 'data', 'capabilities'] as const) {
        if (Array.isArray(o[k])) candidates.push(...(o[k] as unknown[]))
      }
      if (!candidates.length) candidates.push(...Object.values(o))
    } else candidates.push(input)
  }
  for (const c of candidates) {
    const d = normalizeDescriptor(c)
    if (!d) continue
    if (d.device_id === fullKey || d.device_id === devId || d.external_id === devId) return d
    const wrapped = normalizeDescriptor(obj(c)?.descriptor)
    if (wrapped && (wrapped.device_id === fullKey || wrapped.device_id === devId || wrapped.external_id === devId)) return wrapped
  }
  return null
}

/** 从任意载荷（DeviceView / StateData / Envelope.data / REST 响应）里嗅探内联 Descriptor */
export function readInlineDescriptor(input: unknown): DeviceDescriptor | null {
  const o = obj(input)
  if (!o) return null
  for (const key of ['descriptor', 'device_descriptor', 'deviceDescriptor']) {
    if (o[key]) {
      const d = normalizeDescriptor(o[key])
      if (d) return d
    }
  }
  // state.raw 里带 descriptor 的过渡形态
  if (obj(o.state)) return readInlineDescriptor(o.state)
  if (obj(o.raw)) return readInlineDescriptor(o.raw)
  return null
}

/* ---------------- Entity / Observation 读取 ---------------- */

export function entityTitle(e: DescriptorEntity): string {
  return e.name || humanize(e.unique_key || e.entity_id)
}

/** 观测列表：按 (capability, property) 稳定排序，去重同一 capability+property */
export function observationsOf(e: DescriptorEntity): Observation[] {
  const src = e.observations ?? {}
  const out: Observation[] = []
  const seen = new Set<string>()
  for (const [k, v] of Object.entries(src)) {
    const obs = v ?? normalizeObservation(k, v, e.capabilities[0] ?? '')
    if (!obs) continue
    const dedupe = `${obs.capability}::${obs.property}`
    if (seen.has(dedupe)) continue
    seen.add(dedupe)
    out.push(obs)
  }
  return out.sort((a, b) =>
    a.capability.localeCompare(b.capability) || a.property.localeCompare(b.property))
}

export function presentationOf(ref: string | undefined, idx: CapabilityIndex): CapabilityPresentation | undefined {
  return resolveCapability(ref, idx)?.spec?.presentation
}

/** 主观测：presentation.primaryProperty 命中优先；否则该 Entity 第一条标量观测 */
export function primaryObservation(e: DescriptorEntity, idx: CapabilityIndex = EMPTY_INDEX): Observation | undefined {
  const list = observationsOf(e)
  if (!list.length) return undefined
  for (const ref of e.capabilities) {
    const pp = presentationOf(ref, idx)?.primaryProperty
    if (typeof pp === 'string') {
      const hit = list.find((o) => o.property === pp)
      if (hit) return hit
    }
  }
  return list.find((o) => isScalar(o.value)) ?? list[0]
}

export function isScalar(v: unknown): boolean {
  return v === null || typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean'
}

/* ---------------- widget 推导（presentation 是 UI Hint，不是语义真相） ---------------- */

export type WidgetKind =
  | 'metric' | 'gauge' | 'progress' | 'number' | 'text' | 'badge'
  | 'boolean' | 'timestamp' | 'list' | 'table' | 'json'

const WIDGET_ALIASES: Record<string, WidgetKind> = {
  metric: 'metric', number: 'number', value: 'number', numeric: 'number',
  gauge: 'gauge', dial: 'gauge', progress: 'progress', bar: 'progress', percent: 'progress',
  text: 'text', string: 'text', label: 'text', 'display-text': 'text',
  badge: 'badge', chip: 'badge', pill: 'badge', state: 'badge', status: 'badge', enum: 'badge',
  boolean: 'boolean', bool: 'boolean', toggle: 'boolean', switch: 'boolean', contact: 'badge',
  timestamp: 'timestamp', datetime: 'timestamp', time: 'timestamp', clock: 'timestamp', date: 'timestamp',
  list: 'list', chips: 'list', tags: 'list', array: 'list',
  table: 'table', grid: 'table', rows: 'table',
  json: 'json', raw: 'json', code: 'json', object: 'table',
}

const ISO_RE = /^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}/

/**
 * widget 推导优先级：
 *   1) 属性级 Hint（spec.properties.<prop>.widget / defaultWidget）
 *   2) Capability 级 presentation.defaultWidget —— 只作用于 primaryProperty
 *      （未声明 primaryProperty 时视为对该 Capability 全部属性生效）
 *   3) 按观测值类型推导（未知结构 → 表格 / JSON 回落）
 * 这样同一 Capability 下的次要属性不会被主属性的 widget 误伤（如时间能力下的数值偏差）。
 */
export function widgetFor(obs: Observation, idx: CapabilityIndex = EMPTY_INDEX): WidgetKind {
  const doc = resolveCapability(obs.capability, idx)
  const prop = doc?.spec?.properties?.[obs.property]
  const propHint = typeof prop?.widget === 'string' ? prop.widget
    : typeof prop?.defaultWidget === 'string' ? prop.defaultWidget : ''
  const propMapped = WIDGET_ALIASES[propHint.trim().toLowerCase()]
  if (propMapped) return propMapped

  const p = presentationOf(obs.capability, idx)
  const capHint = typeof p?.defaultWidget === 'string' ? p.defaultWidget
    : typeof p?.widget === 'string' ? p.widget : ''
  const capMapped = WIDGET_ALIASES[capHint.trim().toLowerCase()]
  if (capMapped && (p?.primaryProperty === undefined || p?.primaryProperty === obs.property)) return capMapped

  return inferWidget(obs.value)
}

export function inferWidget(v: unknown): WidgetKind {
  if (typeof v === 'number') return 'number'
  if (typeof v === 'boolean') return 'boolean'
  if (typeof v === 'string') return ISO_RE.test(v.trim()) ? 'timestamp' : 'text'
  if (v === null || v === undefined) return 'text'
  if (Array.isArray(v)) return v.every(isScalar) ? 'list' : 'table'
  return 'table'
}

/* ---------------- 值格式化 ---------------- */

const numFmt = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 })

/** 观测值 → 展示文本（单位单独渲染，不拼接；null/undefined → —） */
export function formatValue(v: unknown): string {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'number') return Number.isFinite(v) ? numFmt.format(v) : '—'
  if (typeof v === 'boolean') return v ? '是' : '否'
  if (typeof v === 'string') return v
  if (Array.isArray(v)) return `${v.length} 项`
  const o = obj(v)
  if (o) {
    const picked = pickDisplayField(o)
    if (picked !== undefined) return String(picked)
  }
  return 'JSON'
}

/** ISO 时间串 → 本地时刻（无效则原样） */
export function formatTimestamp(v: unknown): string {
  if (typeof v !== 'string' && typeof v !== 'number') return formatValue(v)
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return String(v)
  return d.toLocaleString('zh-CN', { hour12: false })
}

/** 数组/对象里的「显示字段」通用挑选：label/name/title/text/value 优先，否则第一个标量 */
export function pickDisplayField(o: Record<string, unknown>): unknown {
  for (const k of ['label', 'name', 'title', 'text', 'value']) {
    if (isScalar(o[k]) && o[k] !== null && o[k] !== '') return o[k]
  }
  for (const v of Object.values(o)) if (isScalar(v) && v !== null && v !== '') return v
  return undefined
}

/* ---------------- 语义色（只由 quality / status / presentation.tone 决定） ---------------- */

const TONES: Tone[] = ['ok', 'warn', 'bad', 'accent', 'idle']

export function qualityTone(q: ObservationQuality | undefined): Tone {
  switch (q) {
    case 'good': return 'ok'
    case 'uncertain': return 'warn'
    case 'bad': return 'bad'
    case 'unavailable': return 'idle'
    default: return 'idle'
  }
}

export const QUALITY_LABEL: Record<ObservationQuality, string> = {
  good: '良好', uncertain: '不确定', bad: '异常', unavailable: '不可用',
}

export function statusMeta(s: DeviceStatus | undefined): { label: string; tone: Tone; online: boolean } {
  switch (s) {
    case 'online': return { label: '在线', tone: 'ok', online: true }
    case 'degraded': return { label: '降级', tone: 'warn', online: true }
    case 'offline': return { label: '离线', tone: 'idle', online: false }
    case 'unavailable': return { label: '不可用', tone: 'bad', online: false }
    default: return { label: '未知', tone: 'idle', online: false }
  }
}

export const CATEGORY_ORDER: EntityCategory[] = ['sensor', 'actuator', 'diagnostic', 'config']

export const CATEGORY_LABEL: Record<EntityCategory, string> = {
  sensor: '传感器', actuator: '执行器', diagnostic: '诊断', config: '配置',
}

/** presentation.tone 只有在是合法 Tone 时才采纳（UI Hint 不可信输入） */
export function toneFromHint(p: CapabilityPresentation | undefined): Tone | undefined {
  const t = typeof p?.tone === 'string' ? p.tone : undefined
  return t && (TONES as string[]).includes(t) ? (t as Tone) : undefined
}
/* ---------------- 命令集推导（前端不维护白名单，事实源=声明） ---------------- */

export type CommandSource = 'descriptor' | 'adapter' | 'none'

/** 一条可下发命令的展示模型（由 Capability actions / Descriptor commands / 适配器白名单推导） */
export interface CommandAction {
  /** POST /api/devices/{edge}/{dev}/commands 的 cmd 字段 */
  cmd: string
  label: string
  hint?: string
  variant?: 'primary' | 'ghost' | 'danger'
  confirmText?: string
  needsInput?: boolean
  inputPlaceholder?: string
  inputMaxLength?: number
  /** 来源 Capability 引用与 Entity（展示用，便于运维定位） */
  capability?: string
  entityId?: string
  entityLabel?: string
}

export interface CommandSet {
  actions: CommandAction[]
  source: CommandSource
}

const MAX_ACTIONS = 16
const DEFAULT_ARGS_MAX = 64

function variantOf(decl: Record<string, unknown>): CommandAction['variant'] {
  const v = str(decl.variant)
  if (v === 'primary' || v === 'ghost' || v === 'danger') return v
  if (decl.destructive === true || str(decl.risk) === 'high') return 'danger'
  if (decl.primary === true) return 'primary'
  return 'ghost'
}

function confirmOf(decl: Record<string, unknown>, label: string): string | undefined {
  const c = decl.confirmation ?? decl.confirmText ?? decl.confirm
  if (typeof c === 'string' && c.length) return c
  if (c === true || decl.destructive === true) {
    return `确认执行「${label}」？该动作由 Capability 声明为破坏性操作。`
  }
  return undefined
}

function inputTemplate(schema: unknown): string {
  const props = obj(obj(schema)?.properties)
  if (!props) return ''
  const seed: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(props)) {
    const t = str(obj(v)?.type)
    seed[k] = t === 'number' || t === 'integer' ? 0
      : t === 'boolean' ? false
      : t === 'array' ? []
      : t === 'object' ? {}
      : ''
  }
  if (!Object.keys(seed).length) return ''
  try { return JSON.stringify(seed) } catch { return '' }
}

/** Descriptor 顶层/Entity 上宽容声明的命令集（schema 未强制，但允许扩展字段） */
function declaredCommands(container: Record<string, unknown>): CommandAction[] {
  const out: CommandAction[] = []
  for (const field of ['commands', 'actions'] as const) {
    const list = container[field]
    if (!Array.isArray(list)) continue
    for (const item of list) {
      if (typeof item === 'string' && item.length) {
        out.push({ cmd: item, label: humanize(item) })
        continue
      }
      const o = obj(item)
      if (!o) continue
      const cmd = str(o.command) ?? str(o.cmd) ?? str(o.id) ?? str(o.name) ?? str(o.action)
      if (!cmd) continue
      const label = str(o.title) ?? str(o.label) ?? str(o.name) ?? commandLabel(cmd)
      const a: CommandAction = { cmd, label, variant: variantOf(o) }
      const hint = str(o.description) ?? str(o.hint); if (hint) a.hint = hint
      const confirmText = confirmOf(o, label); if (confirmText) a.confirmText = confirmText
      const schema = obj(o.inputSchema) ?? obj(o.input) ?? obj(o.args)
      const template = inputTemplate(schema)
      if (schema || template) { a.needsInput = true; a.inputPlaceholder = template || undefined }
      else if (o.args === true || o.needsInput === true) a.needsInput = true
      const maxLen = typeof o.maxArgsLength === 'number' ? o.maxArgsLength : DEFAULT_ARGS_MAX
      a.inputMaxLength = maxLen
      out.push(a)
    }
  }
  return out
}

/** Capability actions → CommandAction */
function actionsFromCapabilities(
  descriptor: DeviceDescriptor, idx: CapabilityIndex,
): CommandAction[] {
  const out: CommandAction[] = []
  const ordered = [...descriptor.entities].sort((a, b) =>
    CATEGORY_ORDER.indexOf(a.category) - CATEGORY_ORDER.indexOf(b.category))
  for (const e of ordered) {
    for (const ref of e.capabilities) {
      const doc = resolveCapability(ref, idx)
      const actions = obj(doc?.spec?.actions) as Record<string, CapabilityActionDecl> | undefined
      if (!actions) continue
      for (const [name, declRaw] of Object.entries(actions)) {
        const decl = (obj(declRaw) ?? {}) as Record<string, unknown>
        const cmd = str(decl.command) ?? str(decl.cmd) ?? name
        const label = str(decl.title) ?? str(decl.label) ?? commandLabel(name)
        const a: CommandAction = {
          cmd, label, variant: variantOf(decl),
          capability: ref, entityId: e.entity_id, entityLabel: entityTitle(e),
        }
        const hint = str(decl.description) ?? str(decl.hint); if (hint) a.hint = hint
        const confirmText = confirmOf(decl, label); if (confirmText) a.confirmText = confirmText
        const template = inputTemplate(decl.inputSchema)
        if (template) { a.needsInput = true; a.inputPlaceholder = template }
        a.inputMaxLength = typeof decl.maxArgsLength === 'number' ? decl.maxArgsLength : DEFAULT_ARGS_MAX
        out.push(a)
      }
    }
  }
  return out
}

/**
 * 命令集：Descriptor/Capability 声明优先，其次适配器白名单（/api/adapters，仍是后端事实源），
 * 最后为空。前端不再有任何命令文案/图标白名单表。
 */
export function commandActions(input: {
  descriptor?: DeviceDescriptor | null
  index?: CapabilityIndex
  adapterCommands?: string[]
}): CommandSet {
  const idx = input.index ?? EMPTY_INDEX
  const actions: CommandAction[] = []
  const seen = new Set<string>()
  const push = (a: CommandAction) => {
    if (!a.cmd || seen.has(a.cmd) || actions.length >= MAX_ACTIONS) return
    seen.add(a.cmd)
    actions.push(a)
  }

  let source: CommandSource = 'none'
  const d = input.descriptor ?? null
  if (d) {
    const root = obj(d as unknown)
    if (root) for (const a of declaredCommands(root)) push(a)
    for (const a of actionsFromCapabilities(d, idx)) push(a)
    for (const e of d.entities) {
      const eo = obj(e as unknown)
      if (eo) for (const a of declaredCommands(eo)) push({ ...a, entityId: e.entity_id, entityLabel: entityTitle(e) })
    }
    if (actions.length) source = 'descriptor'
  }
  if (source === 'none') {
    for (const c of input.adapterCommands ?? []) push({ cmd: c, label: commandLabel(c) })
    if (actions.length) source = 'adapter'
  }
  return { actions, source }
}

/* ---------------- 卡片摘要（Descriptor 优先，legacy raw 通用回落） ---------------- */

export interface SummaryValue {
  label: string
  text: string
  unit?: string
  tone: Tone
  /** 悬停详情：capability/property/quality 溯源 */
  title?: string
}

export interface SummaryGroup {
  label: string
  items: SummaryValue[]
}

export interface DeviceSummary {
  primary: SummaryValue | null
  chips: SummaryValue[]
  groups: SummaryGroup[]
  source: 'descriptor' | 'raw' | 'none'
}

const MAX_CHIPS = 4
const MAX_GROUPS = 2
const MAX_GROUP_ITEMS = 8
const MAX_TEXT_LEN = 24

function obsToSummary(
  e: DescriptorEntity, o: Observation, idx: CapabilityIndex, label?: string, toneOverride?: Tone,
): SummaryValue {
  const p = presentationOf(o.capability, idx)
  const widget = widgetFor(o, idx)
  const text = widget === 'timestamp' ? formatTimestamp(o.value) : formatValue(o.value)
  const title = [
    capabilityLabel(o.capability, idx), o.property,
    o.quality ? `质量 ${QUALITY_LABEL[o.quality]}` : '',
    o.received_at ? `接收 ${formatTimestamp(o.received_at)}` : '',
  ].filter(Boolean).join(' · ')
  return {
    label: label ?? entityTitle(e),
    text,
    unit: o.unit,
    tone: toneOverride ?? toneFromHint(p) ?? qualityTone(o.quality),
    title,
  }
}

const ALERT_RANK: Record<string, number> = { bad: 0, warn: 1 }

function isHeadlineCandidate(ref: string | undefined, idx: CapabilityIndex): boolean {
  const p = presentationOf(ref, idx)
  return p?.summary === true || p?.card === true || p?.headline === true || p?.primary === true
}

/** Descriptor → 卡片摘要：headline 由 presentation 提示或首个 Entity 主观测决定 */
export function summarizeDescriptor(d: DeviceDescriptor, idx: CapabilityIndex = EMPTY_INDEX): DeviceSummary {
  const ordered = [...d.entities].sort((a, b) =>
    CATEGORY_ORDER.indexOf(a.category) - CATEGORY_ORDER.indexOf(b.category))
  const candidates: { v: SummaryValue; headline: boolean }[] = []
  const groups: SummaryGroup[] = []

  for (const e of ordered) {
    const primary = primaryObservation(e, idx)
    if (primary && isScalar(primary.value)) {
      candidates.push({
        v: obsToSummary(e, primary, idx),
        headline: isHeadlineCandidate(primary.capability, idx)
          || e.capabilities.some((c) => isHeadlineCandidate(c, idx)),
      })
    }
    if (groups.length < MAX_GROUPS) {
      for (const o of observationsOf(e)) {
        if (!Array.isArray(o.value) || !o.value.length) continue
        const items = o.value.slice(0, MAX_GROUP_ITEMS).map((item, i): SummaryValue => {
          const o2 = obj(item)
          const text = o2 ? String(pickDisplayField(o2) ?? JSON.stringify(item)) : String(item)
          return {
            label: `${entityTitle(e)} ${i + 1}`, text,
            tone: qualityTone(o.quality),
            title: `${capabilityLabel(o.capability, idx)} · ${o.property} #${i + 1}`,
          }
        })
        groups.push({ label: entityTitle(e), items })
        break
      }
    }
  }

  // 质量告警上浮：非主属性但 quality=bad/uncertain 的观测也进胶囊（声明驱动，不猜业务语义）
  const alerts: SummaryValue[] = []
  for (const e of ordered) {
    const main = primaryObservation(e, idx)
    for (const o of observationsOf(e)) {
      if (o === main) continue
      if (o.quality !== 'bad' && o.quality !== 'uncertain') continue
      // 告警胶囊的语义色必须由 quality 决定：presentation.tone 是能力级 UI Hint，
      // 若让它盖过 quality，就会出现「因 bad/uncertain 才上浮、却画成 ok 绿」的自相矛盾。
      alerts.push(obsToSummary(e, o, idx, propertyLabel(o.property, o.capability, idx), qualityTone(o.quality)))
    }
  }
  alerts.sort((a, b) => (ALERT_RANK[a.tone] ?? 9) - (ALERT_RANK[b.tone] ?? 9))

  const headlineIdx = candidates.findIndex((c) => c.headline)
  // 无 presentation hint 时主值优先数值/短文本：卡片主值字号大，长文本必然截断成
  // 「refere…」这类残字；可扫读的数值比截断长文更符合首屏信息密度。
  const compactIdx = candidates.findIndex((c) => /^-?[\d.,]/.test(c.v.text) || c.v.text.length <= 8)
  const pickIdx = headlineIdx >= 0 ? headlineIdx : compactIdx >= 0 ? compactIdx : 0
  const primary = candidates[pickIdx]?.v ?? null
  const chips = candidates.filter((c) => c.v !== primary).map((c) => c.v)
  for (const a of alerts.slice(0, 2)) {
    if (chips.length >= MAX_CHIPS) chips.pop()
    chips.push(a)
  }
  const source = primary || chips.length || groups.length ? 'descriptor' : 'none'
  return { primary, chips: chips.slice(0, MAX_CHIPS), groups, source }
}

/** 设备概览 KPI：按 category 序取各 Entity 主观测（仅标量），完全由 Descriptor 推导、无设备特例；
 *  语义色/单位/标题沿用 obsToSummary（声明驱动）。 */
export function metricTiles(d: DeviceDescriptor, idx: CapabilityIndex = EMPTY_INDEX, max = 4): SummaryValue[] {
  const ordered = [...d.entities].sort((a, b) =>
    CATEGORY_ORDER.indexOf(a.category) - CATEGORY_ORDER.indexOf(b.category))
  const out: SummaryValue[] = []
  for (const e of ordered) {
    if (out.length >= max) break
    const primary = primaryObservation(e, idx)
    if (!primary || !isScalar(primary.value)) continue
    out.push(obsToSummary(e, primary, idx))
  }
  return out
}

/** legacy raw（诊断面）→ 卡片摘要：完全通用，按键顺序取标量，数组转胶囊组 */
export function summarizeRaw(raw: DeviceRaw | undefined): DeviceSummary {
  const entries = Object.entries(raw ?? {})
  const candidates: SummaryValue[] = []
  const groups: SummaryGroup[] = []
  for (const [key, value] of entries) {
    const label = rawKeyLabel(key)
    if (Array.isArray(value)) {
      if (groups.length >= MAX_GROUPS || !value.length) continue
      const items = value.slice(0, MAX_GROUP_ITEMS).map((item, i): SummaryValue => {
        const o = obj(item)
        const text = o ? String(pickDisplayField(o) ?? JSON.stringify(item)) : String(item)
        return { label: `${label} ${i + 1}`, text, tone: 'idle', title: `${key} #${i + 1}` }
      })
      groups.push({ label, items })
      continue
    }
    if (!isScalar(value)) continue
    const text = typeof value === 'string' && ISO_RE.test(value.trim()) ? formatTimestamp(value) : formatValue(value)
    if (text.length > MAX_TEXT_LEN) continue
    if (candidates.length > MAX_CHIPS) continue
    candidates.push({ label, text, tone: 'idle', title: key })
  }
  const primary = candidates.shift() ?? null
  return {
    primary, chips: candidates.slice(0, MAX_CHIPS), groups,
    source: primary || groups.length ? 'raw' : 'none',
  }
}

/** raw 字段名 → 展示名：点分键（entity.property）拆开后逐层回落
 *  （通用词汇 → 属性词典 → humanize）；非点分键保持 humanize，避免平台词汇猜单键语义。 */
function rawKeyLabel(key: string): string {
  const dot = key.lastIndexOf('.')
  if (dot <= 0) return humanize(key)
  const ent = key.slice(0, dot)
  const prop = key.slice(dot + 1)
  return `${GENERIC_NOUN[ent] ?? humanize(ent)} · ${PROPERTY_LABEL[prop] ?? GENERIC_NOUN[prop] ?? humanize(prop)}`
}

/** 详情页通用回落：raw 的每个键一行，widget 由值类型推导（未知结构 → 表格/JSON） */
export interface RawRow {
  key: string
  label: string
  widget: WidgetKind
  value: unknown
}

export function rawRows(raw: DeviceRaw | undefined): RawRow[] {
  return Object.entries(raw ?? {}).map(([key, value]) => ({
    key, label: rawKeyLabel(key), widget: inferWidget(value), value,
  }))
}

/** 事件声明查找：扫描 catalog 里各 Capability 的 spec.events。
 *  事件类型属于 Capability/Application 命名空间，前端不维护平台级事件枚举；
 *  未收录的事件类型仍照常展示（humanize 回落），只是没有声明的标题/语义色。 */
export function eventDecl(type: string, idx: CapabilityIndex): {
  title?: string; description?: string; tone?: Tone
} | undefined {
  if (!idx.docs.length || !type) return undefined
  const keys = [type, type.split('#').pop() ?? type, type.split('.').pop() ?? type, humanize(type)]
  for (const doc of idx.docs) {
    const events = obj(doc.spec?.events)
    if (!events) continue
    for (const k of keys) {
      const decl = obj(events[k])
      if (!decl) continue
      const tone = str(decl.tone)
      return {
        // 事件标题同样走 locale 匹配：英文 title 让位给上层中文组合名
        title: localizedTitle(str(decl.title) ?? str(decl.label)),
        description: str(decl.description),
        tone: tone && (TONES as string[]).includes(tone) ? (tone as Tone) : undefined,
      }
    }
  }
  return undefined
}
