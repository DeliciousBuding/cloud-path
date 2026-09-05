// Schema 驱动渲染器（A2）：把 Descriptor / Capability 声明翻译成 UI。
// 本文件不含任何设备语义（不认识时钟、分格、提醒…）：
//   - 展示什么 = Descriptor 的 entities[].observations + Capability 的 presentation
//   - 怎么展示 = presentation.defaultWidget（UI Hint），缺席则按值类型推导
//   - 未知 Capability = 通用表格 / JSON 回落（docs/architecture/capability-model.md §9）
// 颜色只走设计系统 token（ui.tsx TONE_CLS / index.css），390px 下不产生横向溢出。
import { Activity, Boxes, SlidersHorizontal, Stethoscope, Zap, type LucideIcon } from 'lucide-react'
import { Badge, Panel, TONE_CLS, TONE_TEXT_CLS, type Tone } from './ui'
import { cn } from '@/lib/cn'
import { useReducedMotion } from '@/hooks/useReducedMotion'
import { Sparkline } from './Sparkline'
import type { SeriesPoint } from '@/store/ws'
import {
  CATEGORY_LABEL, CATEGORY_ORDER, EMPTY_INDEX, QUALITY_LABEL, capabilityLabel, commandLabel,
  entityTitle, formatTimestamp, formatValue, humanize, isScalar, observationsOf, parseCapabilityRef,
  pickDisplayField, propertyLabel, presentationOf, primaryObservation, qualityTone, rawRows,
  resolveCapability, statusMeta, toneFromHint, widgetFor,
} from '@/lib/descriptor'
import type { CapabilityIndex, SummaryValue, WidgetKind } from '@/lib/descriptor'
import type {
  DescriptorEntity, DeviceDescriptor, DeviceRaw, DeviceStatus, EntityCategory,
  Observation, ObservationQuality,
} from '@/lib/types'

/** Entity 分类图标（平台词汇，来自 descriptor schema 的 category 枚举） */
export const CATEGORY_ICON: Record<EntityCategory, LucideIcon> = {
  sensor: Activity, actuator: Zap, diagnostic: Stethoscope, config: SlidersHorizontal,
}

const QUALITY_DOT: Record<ObservationQuality, string> = {
  good: 'bg-ok', uncertain: 'bg-warn', bad: 'bg-bad', unavailable: 'bg-idle/50',
}

/* ---------------- 原子件 ---------------- */

/** 质量指示点：good 不打扰（不渲染），其余按语义色标注 */
export function QualityDot({ q }: { q?: ObservationQuality }) {
  if (!q || q === 'good') return null
  return (
    // role=img 才能让 aria-label 进入无障碍树（裸 span 的 label 会被忽略）
    <span role="img" className={cn('inline-block h-1.5 w-1.5 shrink-0 rounded-full', QUALITY_DOT[q])}
      title={`观测质量：${QUALITY_LABEL[q]}`} aria-label={`观测质量 ${QUALITY_LABEL[q]}`} />
  )
}

export function StatusBadge({ status }: { status?: DeviceStatus }) {
  const meta = statusMeta(status)
  return <Badge tone={meta.tone}>{meta.label}</Badge>
}

export function JsonBlock({ value, className, maxHeight = 'max-h-56', label = '原始 JSON（通用回落视图）' }: {
  value: unknown; className?: string; maxHeight?: string; label?: string
}) {
  let text: string
  try {
    text = JSON.stringify(value ?? null, null, 2)
  } catch {
    text = String(value)
  }
  return (
    // 可滚动区域必须键盘可达（WCAG 2.1.1）：tabIndex=0 + role=group + 可读名称
    <pre
      tabIndex={0} role="group" aria-label={label}
      className={cn('num overflow-auto rounded-xl bg-surface-2 p-3 font-mono text-[11px] leading-relaxed text-ink-2',
        maxHeight, className)}
      title="通用 JSON 视图（未收录结构的回落）"
    >{text}</pre>
  )
}

function cellText(v: unknown): string {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean') return formatValue(v)
  const o = v as Record<string, unknown>
  const picked = Array.isArray(v) ? undefined : pickDisplayField(o)
  if (picked !== undefined) return String(picked)
  try { return JSON.stringify(v) } catch { return '—' }
}

function columnsOf(rows: Record<string, unknown>[]): string[] {
  const seen: string[] = []
  for (const r of rows) for (const k of Object.keys(r)) if (!seen.includes(k)) seen.push(k)
  return seen.slice(0, 6)
}

/** 通用表格：数组型观测（对象数组或标量数组）都能渲染，列名来自数据本身 */
export function GenericTable({ value, className, label = '数据表（通用视图）' }: {
  value: unknown[]; className?: string; label?: string
}) {
  const rows = value.map((v) => (v && typeof v === 'object' && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : { value: v }))
  const cols = columnsOf(rows)
  if (!cols.length) return <p className="py-3 text-center text-xs text-ink-3">空集合</p>
  return (
    // 390px：表格只在自身容器内横向滚动（overflow-x-auto），不把横向溢出推给 body；
    // 容器可聚焦并带名称，键盘/读屏用户才能进入这块滚动区。
    <div tabIndex={0} role="group" aria-label={label} className={cn('overflow-x-auto', className)}>
      <table className="w-full border-collapse text-left text-xs">
        <thead>
          <tr className="text-[11px] text-ink-3">
            <th className="px-1 pb-1.5 font-medium">#</th>
            {cols.map((c) => (
              <th key={c} className="whitespace-nowrap px-1 pb-1.5 font-medium">
                {c === 'value' ? '值' : propertyLabel(c)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-hairline">
          {rows.map((r, i) => (
            <tr key={i}>
              <td className="num px-1 py-1.5 text-ink-3">{i + 1}</td>
              {cols.map((c) => (
                <td key={c} className="max-w-[9rem] truncate px-1 py-1.5" title={cellText(r[c])}>
                  {cellText(r[c])}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function ScalarChips({ value }: { value: unknown[] }) {
  if (!value.length) return <p className="py-2 text-center text-xs text-ink-3">空集合</p>
  return (
    <div className="flex flex-wrap gap-1.5">
      {value.map((v, i) => (
        <span key={i} className="num max-w-full truncate rounded-full bg-ink-3/10 px-2 py-0.5 text-[11px] font-medium text-ink-2"
          title={cellText(v)}>
          {cellText(v)}
        </span>
      ))}
    </div>
  )
}

function RangeBar({ value, range, className }: {
  value: number | null; range: [number, number]; className?: string
}) {
  const pct = value === null
    ? 0
    : Math.max(0, Math.min(100, ((value - range[0]) / (range[1] - range[0])) * 100))
  return (
    <span className={cn('block h-1 w-full overflow-hidden rounded-full bg-ink-3/12', className)} aria-hidden>
      <span className="block h-full rounded-full bg-accent" style={{ width: `${pct}%` }} />
    </span>
  )
}

/** 量程仅在 Capability/presentation 显式声明 min/max 时才画条（不猜语义量程） */
function rangeOf(obs: Observation, idx: CapabilityIndex): [number, number] | null {
  const doc = resolveCapability(obs.capability, idx)
  const prop = doc?.spec?.properties?.[obs.property]
  const p = presentationOf(obs.capability, idx)
  const num = (v: unknown): number | null => (typeof v === 'number' && Number.isFinite(v) ? v : null)
  const min = num(prop?.min) ?? num(prop?.minimum) ?? num(p?.min) ?? num(p?.minimum)
  const max = num(prop?.max) ?? num(prop?.maximum) ?? num(p?.max) ?? num(p?.maximum)
  return min !== null && max !== null && max > min ? [min, max] : null
}

/** 标量值文本（按 widget 选择时间/数值/布尔格式） */
function ScalarText({ widget, value, emphasis }: { widget: WidgetKind; value: unknown; emphasis?: boolean }) {
  const text = widget === 'timestamp' ? formatTimestamp(value) : formatValue(value)
  return (
    <span className={cn('num', emphasis ? 'block text-[28px] font-semibold leading-none tracking-tight'
      : 'text-[13px] font-medium')}>{text}</span>
  )
}

/* ---------------- 观测值 widget ---------------- */

/**
 * 单个观测值的渲染：presentation.defaultWidget 优先，未声明则按值类型推导；
 * 对象/数组一律走通用表格或 JSON —— 未知结构不会白屏，也不需要前端认识它。
 */
export function ValueWidget({ obs, idx = EMPTY_INDEX, emphasis = false, className }: {
  obs: Observation
  idx?: CapabilityIndex
  emphasis?: boolean
  className?: string
}) {
  const widget = widgetFor(obs, idx)
  const value = obs.value

  if (widget === 'list' && Array.isArray(value)) return <ScalarChips value={value} />

  if ((widget === 'table' || widget === 'json') && !isScalar(value)) {
    if (Array.isArray(value)) {
      return <GenericTable value={value} label={`${propertyLabel(obs.property, obs.capability, idx)} 数据表`} />
    }
    if (value && typeof value === 'object') {
      const entries = Object.entries(value as Record<string, unknown>)
      const flat = entries.length > 0 && entries.every(([, v]) => isScalar(v))
      if (flat && widget === 'table') {
        return (
          <dl className="space-y-2">
            {entries.map(([k, v]) => (
              <div key={k} className="kv">
                <dt className="min-w-0 truncate">{humanize(k)}</dt>
                <dd className="num min-w-0 truncate">{formatValue(v)}</dd>
              </div>
            ))}
          </dl>
        )
      }
      return <JsonBlock value={value} maxHeight="max-h-40" label={`${propertyLabel(obs.property, obs.capability, idx)} 原始 JSON`} />
    }
  }

  if (widget === 'json') {
    return <JsonBlock value={value} maxHeight="max-h-40" label={`${propertyLabel(obs.property, obs.capability, idx)} 原始 JSON`} />
  }

  if (widget === 'boolean' || widget === 'badge') {
    const tone: Tone = value === false
      ? 'idle'
      : toneFromHint(presentationOf(obs.capability, idx)) ?? qualityTone(obs.quality)
    return (
      <span className={cn('inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium', TONE_CLS[tone])}>
        {obs.quality && obs.quality !== 'good' && <QualityDot q={obs.quality} />}
        {formatValue(value)}
      </span>
    )
  }

  const range = widget === 'gauge' || widget === 'progress' ? rangeOf(obs, idx) : null
  return (
    <span className={cn('flex min-w-0 flex-col items-end gap-1', className)}>
      {range && <RangeBar value={typeof value === 'number' ? value : null} range={range} className="max-w-[8rem]" />}
      <span className="flex min-w-0 items-baseline gap-1">
        <ScalarText widget={widget} value={value} emphasis={emphasis} />
        {obs.unit && (
          <span className={cn('num shrink-0 text-ink-3', emphasis ? 'text-sm' : 'text-[11px]')}>{obs.unit}</span>
        )}
      </span>
    </span>
  )
}

/* ---------------- Entity / Descriptor ---------------- */

/** 观测表：属性名（humanize）+ 值 + 单位 + 质量点；未收录 Capability 打标 */
export function ObservationTable({ observations, idx = EMPTY_INDEX }: {
  observations: Observation[]
  idx?: CapabilityIndex
}) {
  if (!observations.length) return <p className="py-3 text-center text-xs text-ink-3">还没有观测值</p>
  return (
    <dl className="space-y-2.5">
      {observations.map((o) => {
        const known = Boolean(resolveCapability(o.capability, idx))
        return (
          <div key={`${o.capability}::${o.property}`} className="kv">
            <dt className="flex min-w-0 items-center gap-1.5">
              <QualityDot q={o.quality} />
              <span className="truncate" title={`${capabilityLabel(o.capability, idx)} · ${o.property}`}>
                {propertyLabel(o.property, o.capability, idx)}
              </span>
              {!known && <Badge tone="idle" className="shrink-0">未收录</Badge>}
            </dt>
            <dd className="min-w-0 shrink-0 text-right">
              <ValueWidget obs={o} idx={idx} />
            </dd>
          </div>
        )
      })}
    </dl>
  )
}

/* ---------------- 实时状态矩阵（human-first 默认视图） ---------------- */

/** 观测新鲜度：received_at 早于阈值（默认 60s，不猜设备周期的通用阈值）视为 stale。
 *  只在异常 Entity 上标注；全局新鲜度由页面头部说一次，不逐卡重复。 */
export function isStaleObs(o: Observation, nowSec: number, thresholdSec = 60): boolean {
  const at = typeof o.received_at === 'string' ? Date.parse(o.received_at) / 1000 : Number.NaN
  return Number.isFinite(at) && nowSec - at > thresholdSec
}

const QUALITY_RANK: ObservationQuality[] = ['bad', 'uncertain', 'unavailable', 'good']

function worstQuality(os: Observation[]): ObservationQuality | undefined {
  return QUALITY_RANK.find((q) => os.some((o) => o.quality === q))
}

/** 实时状态单格：展示名 + 当前值 + 单位；质量点/stale 只标异常格；
 *  次级观测默认折叠（渐进披露）；机器 ID 与原始 JSON 不进这一层（去能力/诊断页）。 */
/**
 * 状态行（实时状态默认视图）：一个实体一行——展示名 + 质量 + 火花线 + 右对齐当前值。
 * 行 / divider 能解决的就不要再套一张卡：16 个实体 = 16 行扫完，宽屏不留网格空洞。
 */
export function StateRow({ entity, idx = EMPTY_INDEX, nowSec, series }: {
  entity: DescriptorEntity
  idx?: CapabilityIndex
  nowSec?: number
  /** 会话数值序列（deviceKey 下的 属性键 -> 点）；命中主观测键时行内嵌火花线 */
  series?: Record<string, SeriesPoint[]>
}) {
  const obs = observationsOf(entity)
  const primary = primaryObservation(entity, idx)
  const rest = obs.filter((o) => o !== primary)
  const q = worstQuality(obs)
  const stale = nowSec !== undefined && obs.some((o) => isStaleObs(o, nowSec))
  // 序列键形态因适配器而异（entity.property 点分 / 裸属性名 / 实体名）：逐级回落
  const pts = primary
    ? (series?.[`${entity.entity_id}.${primary.property}`]
      ?? series?.[primary.property]
      ?? series?.[entity.entity_id])
    : undefined
  const text = primary
    ? (widgetFor(primary, idx) === 'timestamp' ? formatTimestamp(primary.value) : formatValue(primary.value))
    : ''
  return (
    <li className="px-3.5 py-2.5">
      <div className="flex min-w-0 items-center gap-3">
        <span className="flex min-w-0 flex-1 items-center gap-1.5">
          <span className="truncate text-[13px] font-medium" title={entity.name || entity.unique_key}>
            {entityTitle(entity)}
          </span>
          <QualityDot q={q} />
          {stale && <Badge tone="warn" className="shrink-0">未更新</Badge>}
        </span>
        {pts && pts.length >= 2 && (
          // Sparkline 自身是 w-full：宽度由外层固定槽给，避免与内部类冲突撑破行
          <span className="hidden w-24 shrink-0 sm:block">
            <Sparkline points={pts} />
          </span>
        )}
        {primary ? (
          <span
            className={cn('num w-24 shrink-0 truncate text-right tracking-tight',
              text.length > 12 ? 'text-[13px] font-medium' : 'text-[15px] font-semibold')}
            title={`${capabilityLabel(primary.capability, idx)} · ${primary.property}`}>
            {text}
            {primary.unit && <span className="ml-1 text-[11px] font-normal text-ink-3">{primary.unit}</span>}
          </span>
        ) : (
          <span className="w-24 shrink-0 text-right text-[12px] text-ink-3">暂无数据</span>
        )}
      </div>
      {rest.length > 0 && (
        <details className="mt-1.5">
          <summary className="cursor-pointer select-none text-[11px] text-ink-3 transition-colors hover:text-ink-2">
            其余 {rest.length} 项
          </summary>
          <div className="mt-2">
            <ObservationTable observations={rest} idx={idx} />
          </div>
        </details>
      )}
    </li>
  )
}

/**
 * 实时状态矩阵：按 Descriptor category 分组，每组一个面板、每个实体一行。
 * 组头只有组名（实体计数是调试噪音，不是用户信息）；值列右对齐，扫读时眼睛只走两条线。
 */
export function StateMatrix({ descriptor, idx = EMPTY_INDEX, categories, nowSec, series, className }: {
  descriptor: DeviceDescriptor
  idx?: CapabilityIndex
  categories?: EntityCategory[]
  nowSec?: number
  series?: Record<string, SeriesPoint[]>
  className?: string
}) {
  const groups = (categories ?? CATEGORY_ORDER)
    .map((category) => ({ category, entities: descriptor.entities.filter((e) => e.category === category) }))
    .filter((g) => g.entities.length > 0)
  if (!groups.length) {
    return <p className="py-6 text-center text-sm text-ink-3">Descriptor 未声明 Entity</p>
  }
  return (
    <div className={cn('space-y-5', className)}>
      {groups.map(({ category, entities }) => (
        <section key={category}>
          <h3 className="mb-1.5 px-0.5 text-[12px] font-medium text-ink-3">{CATEGORY_LABEL[category]}</h3>
          <ul className="card m-0 list-none divide-y divide-hairline p-0">
            {entities.map((e) => (
              <StateRow key={e.entity_id || e.unique_key} entity={e} idx={idx} nowSec={nowSec} series={series} />
            ))}
          </ul>
        </section>
      ))}
    </div>
  )
}

/** KPI 瓦片（设备概览首屏）：大字号只给真正的主指标；语义色只用于 warn/bad，其余保持中性 */
export function MetricTile({ v }: { v: SummaryValue }) {
  const reduced = useReducedMotion()
  const valueTone = v.tone === 'bad' || v.tone === 'warn' ? TONE_TEXT_CLS[v.tone] : undefined
  return (
    <div className={cn('card min-w-0 p-4 fade-up', v.tone === 'bad' && !reduced && 'remind')}>
      <p className="min-w-0 truncate text-[11px] font-medium text-ink-3" title={v.title}>{v.label}</p>
      <p className={cn('num mt-1.5 truncate text-[24px] font-semibold leading-none tracking-tight', valueTone)}
        title={v.title}>
        {v.text}
        {v.unit && <span className="ml-1 text-[12px] font-normal text-ink-3">{v.unit}</span>}
      </p>
    </div>
  )
}

/* ---------------- Capability schema 浏览器（developer-facing） ---------------- */

function declTitle(decl: unknown, fallback: string): string {
  const o = decl && typeof decl === 'object' && !Array.isArray(decl) ? (decl as Record<string, unknown>) : null
  const t = o?.title ?? o?.label
  // locale 匹配：只有中文 title 才算已本地化的展示名，英文 title 让位给平台词汇/humanize
  return typeof t === 'string' && t.length > 0 && /[\u3400-\u9fff]/.test(t) ? t : fallback
}

/** Capability 浏览器：每条声明 Capability 一行（展示名 +  canonical ID + 规模），
 *  点击展开 Inspector（属性/动作/事件/schema JSON）——机器 ID 只在这一层与诊断页出现。 */
export function CapabilityBrowser({ descriptor, idx = EMPTY_INDEX, className }: {
  descriptor: DeviceDescriptor
  idx?: CapabilityIndex
  className?: string
}) {
  const set = new Set<string>()
  for (const e of descriptor.entities) for (const c of e.capabilities) if (c) set.add(c)
  const refs = [...set]
  if (!refs.length) return <p className="py-6 text-center text-sm text-ink-3">Descriptor 未声明 Capability</p>
  return (
    <ul className={cn('m-0 list-none divide-y divide-hairline p-0', className)}>
      {refs.map((ref) => {
        const doc = resolveCapability(ref, idx)
        const spec = (doc?.spec ?? {}) as Record<string, unknown>
        const props = Object.entries((spec.properties ?? {}) as Record<string, unknown>)
        const actions = Object.entries((spec.actions ?? {}) as Record<string, unknown>)
        const events = Object.entries((spec.events ?? {}) as Record<string, unknown>)
        const parsed = parseCapabilityRef(ref)
        return (
          <li key={ref}>
            <details className="py-2.5">
              <summary className="flex cursor-pointer select-none flex-wrap items-center gap-x-3 gap-y-1">
                <span className="min-w-0 truncate text-[13px] font-medium">{capabilityLabel(ref, idx)}</span>
                {!doc && <Badge tone="idle" className="shrink-0">未收录</Badge>}
                <span className="num min-w-0 truncate font-mono text-[11px] text-ink-3" title={ref}>{ref}</span>
                <span className="num ml-auto shrink-0 text-[11px] text-ink-3">
                  {props.length} 属性 · {actions.length} 动作 · {events.length} 事件
                </span>
              </summary>
              <div className="mt-2.5 space-y-3 pl-0.5">
                {props.length > 0 && (
                  <div className="overflow-x-auto">
                    <table className="w-full border-collapse text-left text-xs">
                      <thead>
                        <tr className="text-[11px] text-ink-3">
                          <th className="px-1 pb-1 font-medium">属性</th>
                          <th className="px-1 pb-1 font-medium">机器名</th>
                          <th className="px-1 pb-1 font-medium">类型</th>
                          <th className="px-1 pb-1 font-medium">单位</th>
                          <th className="px-1 pb-1 font-medium">访问</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-hairline">
                        {props.map(([name, decl]) => {
                          const d = (decl ?? {}) as Record<string, unknown>
                          return (
                            <tr key={name}>
                              <td className="max-w-[10rem] truncate px-1 py-1.5">{declTitle(d, propertyLabel(name, ref, idx))}</td>
                              <td className="num px-1 py-1.5 font-mono text-[11px] text-ink-3">{name}</td>
                              <td className="px-1 py-1.5 text-ink-2">{String(d.type ?? '—')}</td>
                              <td className="num px-1 py-1.5 text-ink-2">{String(d.unit ?? '—')}</td>
                              <td className="px-1 py-1.5 text-ink-2">{String(d.access ?? '—')}</td>
                            </tr>
                          )
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
                {actions.length > 0 && (
                  <div className="flex flex-wrap gap-1.5">
                    {actions.map(([name, decl]) => (
                      <span key={name} className="badge bg-ink-3/10 text-ink-2" title={name}>
                        {declTitle(decl, commandLabel(name))}
                      </span>
                    ))}
                  </div>
                )}
                {events.length > 0 && (
                  <div className="flex flex-wrap gap-1.5">
                    {events.map(([name, decl]) => (
                      <span key={name} className="badge bg-ink-3/10 text-ink-2" title={name}>
                        {declTitle(decl, humanize(name))}
                      </span>
                    ))}
                  </div>
                )}
                {doc && (
                  <details>
                    <summary className="cursor-pointer select-none text-[11px] text-ink-3 transition-colors hover:text-ink-2">
                      Schema JSON（v{parsed.version ?? doc.metadata?.version ?? '—'}）
                    </summary>
                    <JsonBlock className="mt-1.5" value={doc.spec} maxHeight="max-h-48" label={`${ref} schema JSON`} />
                  </details>
                )}
              </div>
            </details>
          </li>
        )
      })}
    </ul>
  )
}

/** Entity 清单（取证面）：entity_id / 分类 / Capability 引用，全部机器原文，只在诊断页出现 */
export function EntityInventory({ descriptor, className }: {
  descriptor: DeviceDescriptor
  className?: string
}) {
  return (
    <div className={cn('overflow-x-auto', className)}>
      <table className="w-full border-collapse text-left text-xs">
        <thead>
          <tr className="text-[11px] text-ink-3">
            <th className="px-1 pb-1.5 font-medium">实体</th>
            <th className="px-1 pb-1.5 font-medium">entity_id</th>
            <th className="px-1 pb-1.5 font-medium">分类</th>
            <th className="px-1 pb-1.5 font-medium">Capability 引用</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-hairline">
          {descriptor.entities.map((e) => (
            <tr key={e.entity_id || e.unique_key}>
              <td className="max-w-[12rem] truncate px-1 py-1.5">{entityTitle(e)}</td>
              <td className="num px-1 py-1.5 font-mono text-[11px] text-ink-3">{e.entity_id}</td>
              <td className="px-1 py-1.5 text-ink-2">{CATEGORY_LABEL[e.category] ?? e.category}</td>
              <td className="px-1 py-1.5">
                <span className="flex min-w-0 flex-wrap gap-1">
                  {e.capabilities.length === 0
                    ? <span className="text-ink-3">—</span>
                    : e.capabilities.map((c) => (
                      <span key={c} className="num max-w-[16rem] truncate font-mono text-[10px] text-ink-3" title={c}>
                        {c}
                      </span>
                    ))}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

/** legacy raw（诊断面）→ 通用视图：标量成键值行，数组成表格/胶囊，对象成 JSON */
export function RawView({ raw, title = '上报字段（通用视图）', className }: {
  raw: DeviceRaw | undefined
  title?: string
  className?: string
}) {
  const rows = rawRows(raw)
  if (!rows.length) {
    return (
      <Panel className={className} title={title}>
        <p className="py-4 text-center text-sm text-ink-3">等待设备上报…</p>
      </Panel>
    )
  }
  const scalarKinds: WidgetKind[] = ['text', 'number', 'boolean', 'timestamp', 'badge', 'metric']
  const scalars = rows.filter((r) => scalarKinds.includes(r.widget))
  const scalarKeys = new Set(scalars.map((r) => r.key))
  const complex = rows.filter((r) => !scalarKeys.has(r.key))

  return (
    <Panel className={className} title={title} right={<Badge tone="idle">{rows.length} 字段</Badge>}>
      {scalars.length > 0 && (
        <dl className="space-y-2.5">
          {scalars.map((r) => (
            <div key={r.key} className="kv">
              <dt className="min-w-0 truncate" title={r.key}>{r.label}</dt>
              <dd className="min-w-0 truncate text-right">
                <ScalarText widget={r.widget} value={r.value} />
              </dd>
            </div>
          ))}
        </dl>
      )}
      {complex.map((r) => (
        <div key={r.key} className="mt-4 border-t border-hairline pt-3 first:mt-0">
          <p className="mb-1.5 truncate text-[11px] text-ink-3" title={r.key}>{r.label}</p>
          {Array.isArray(r.value)
            ? (r.value.every(isScalar)
              ? <ScalarChips value={r.value} />
              : <GenericTable value={r.value} label={`${r.label} 数据表`} />)
            : <JsonBlock value={r.value} maxHeight="max-h-40" label={`${r.label} 原始 JSON`} />}
        </div>
      ))}
      <p className="mt-3 flex items-center gap-1 border-t border-hairline pt-2 text-[11px] text-ink-3">
        <Boxes size={11} />
        <span className={cn('truncate', TONE_TEXT_CLS.idle)}>
          该设备尚未提供 Descriptor，此处按上报字段通用渲染
        </span>
      </p>
    </Panel>
  )
}