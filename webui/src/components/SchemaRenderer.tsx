// Schema 驱动渲染器（A2）：把 Descriptor / Capability 声明翻译成 UI。
// 本文件不含任何设备语义（不认识时钟、分格、提醒…）：
//   - 展示什么 = Descriptor 的 entities[].observations + Capability 的 presentation
//   - 怎么展示 = presentation.defaultWidget（UI Hint），缺席则按值类型推导
//   - 未知 Capability = 通用表格 / JSON 回落（docs/architecture/capability-model.md §9）
// 颜色只走设计系统 token（ui.tsx TONE_CLS / index.css），390px 下不产生横向溢出。
import { Activity, Boxes, Braces, SlidersHorizontal, Stethoscope, Zap, type LucideIcon } from 'lucide-react'
import { Badge, KeyValue, Panel, TONE_CLS, TONE_TEXT_CLS, type Tone } from './ui'
import { cn } from '@/lib/cn'
import {
  CATEGORY_LABEL, CATEGORY_ORDER, EMPTY_INDEX, QUALITY_LABEL, capabilityLabel, entityTitle,
  formatTimestamp, formatValue, humanize, isScalar, observationsOf, pickDisplayField,
  presentationOf, primaryObservation, qualityTone, rawRows, resolveCapability, statusMeta,
  toneFromHint, widgetFor,
} from '@/lib/descriptor'
import type { CapabilityIndex, SummaryGroup, SummaryValue, WidgetKind } from '@/lib/descriptor'
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

/** 通用胶囊：tone 只来自 observation.quality 或 presentation 提示 */
export function Chip({ v, className }: { v: SummaryValue; className?: string }) {
  return (
    <span
      title={v.title}
      className={cn('inline-flex max-w-full items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium',
        TONE_CLS[v.tone], className)}
    >
      <span className="truncate opacity-70">{v.label}</span>
      <span className="num truncate">{v.text}</span>
      {v.unit && <span className="num opacity-60">{v.unit}</span>}
    </span>
  )
}

export function SummaryChips({ chips, className }: { chips: SummaryValue[]; className?: string }) {
  if (!chips.length) return null
  return (
    <div className={cn('flex min-w-0 flex-wrap gap-1.5', className)}>
      {chips.map((c, i) => <Chip key={`${c.label}-${c.title ?? ''}-${i}`} v={c} />)}
    </div>
  )
}

/** 数组型观测（列表/分格类）→ 分组胶囊，标签与条目全部来自数据 */
export function SummaryGroups({ groups, className }: { groups: SummaryGroup[]; className?: string }) {
  if (!groups.length) return null
  return (
    <div className={cn('space-y-2', className)}>
      {groups.map((g) => (
        <div key={g.label} className="min-w-0">
          <p className="mb-1 truncate text-[11px] text-ink-3">{g.label}</p>
          <div className="flex flex-wrap gap-1.5">
            {g.items.map((it, i) => (
              <span key={`${it.label}-${i}`} title={it.title}
                className={cn('max-w-full truncate rounded-full px-2 py-0.5 text-[11px] font-medium', TONE_CLS[it.tone])}>
                {it.text}
              </span>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}

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
                {c === 'value' ? '值' : humanize(c)}
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
      return <GenericTable value={value} label={`${humanize(obs.property)} 数据表`} />
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
      return <JsonBlock value={value} maxHeight="max-h-40" label={`${humanize(obs.property)} 原始 JSON`} />
    }
  }

  if (widget === 'json') {
    return <JsonBlock value={value} maxHeight="max-h-40" label={`${humanize(obs.property)} 原始 JSON`} />
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
                {humanize(o.property)}
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

/** 单个 Entity：主观测大字 + 其余观测表 + 未收录 Capability 的 JSON 回落 */
export function EntityPanel({ entity, idx = EMPTY_INDEX, className }: {
  entity: DescriptorEntity
  idx?: CapabilityIndex
  className?: string
}) {
  const Icon = CATEGORY_ICON[entity.category] ?? Boxes
  const obs = observationsOf(entity)
  const primary = primaryObservation(entity, idx)
  const rest = obs.filter((o) => o !== primary)
  const unknown = entity.capabilities.filter((c) => !resolveCapability(c, idx))
  const unknownObs = obs.filter((o) => !resolveCapability(o.capability, idx))

  return (
    <Panel
      className={className}
      title={
        <span className="flex min-w-0 items-center gap-1.5">
          <Icon size={14} className="shrink-0" />
          <span className="truncate" title={entity.name || entity.unique_key}>{entityTitle(entity)}</span>
        </span>
      }
      right={<Badge tone="idle">{CATEGORY_LABEL[entity.category] ?? entity.category}</Badge>}
    >
      {primary ? (
        <div className="flex min-w-0 items-end justify-between gap-3">
          <p className="min-w-0 flex-1 truncate text-[11px] text-ink-3"
            title={`${primary.capability} · ${primary.property}`}>
            {capabilityLabel(primary.capability, idx)} · {humanize(primary.property)}
          </p>
          <ValueWidget obs={primary} idx={idx} emphasis />
        </div>
      ) : (
        <p className="py-3 text-center text-xs text-ink-3">等待观测值…</p>
      )}

      {rest.length > 0 && (
        <div className="mt-4 border-t border-hairline pt-3">
          <ObservationTable observations={rest} idx={idx} />
        </div>
      )}

      {unknown.length > 0 && (
        <div className="mt-4 border-t border-hairline pt-3">
          <p className="mb-1.5 flex items-center gap-1 text-[11px] text-ink-3">
            <Boxes size={11} /> 未收录 Capability · 通用视图
          </p>
          <div className="mb-2 flex flex-wrap gap-1.5">
            {unknown.map((u) => (
              <span key={u} className="num max-w-full truncate rounded-full bg-ink-3/10 px-2 py-0.5 font-mono text-[10px] text-ink-2"
                title={u}>
                {u}
              </span>
            ))}
          </div>
          {unknownObs.length > 0 && (
            <JsonBlock value={unknownObs} maxHeight="max-h-40"
              label="未收录 Capability 的观测原始 JSON" />
          )}
        </div>
      )}

      <p className="num mt-3 truncate border-t border-hairline pt-2 font-mono text-[10px] text-ink-3"
        title={`entity_id ${entity.entity_id} · unique_key ${entity.unique_key} · capabilities ${entity.capabilities.length}`}>
        {entity.unique_key}
      </p>
    </Panel>
  )
}

/** 整份 Descriptor：设备标识 + 按 category 分组的 Entity 面板 */
export function DescriptorView({ descriptor, idx = EMPTY_INDEX, className }: {
  descriptor: DeviceDescriptor
  idx?: CapabilityIndex
  className?: string
}) {
  const grouped = CATEGORY_ORDER
    .map((category) => ({ category, entities: descriptor.entities.filter((e) => e.category === category) }))
    .filter((g) => g.entities.length > 0)

  return (
    <div className={cn('space-y-5', className)}>
      <Panel title={<span className="flex items-center gap-1.5"><Braces size={14} />Schema 描述</span>}
        right={<StatusBadge status={descriptor.status} />}>
        <dl className="space-y-2.5">
          <KeyValue k="Device ID" v={descriptor.device_id} mono />
          <KeyValue k="External ID" v={descriptor.external_id} mono />
          {descriptor.manufacturer && <KeyValue k="厂商" v={descriptor.manufacturer} />}
          {descriptor.model && <KeyValue k="型号" v={descriptor.model} />}
          <KeyValue k="Entity" v={`${descriptor.entities.length} 个`} />
          <KeyValue k="Capability 引用"
            v={`${new Set(descriptor.entities.flatMap((e) => e.capabilities)).size} 种`} />
        </dl>
      </Panel>

      {grouped.length === 0 && (
        <Panel>
          <p className="py-4 text-center text-sm text-ink-3">Descriptor 未声明 Entity</p>
        </Panel>
      )}

      {grouped.map((g) => (
        <section key={g.category}>
          <h3 className="mb-2 flex items-center gap-1.5 px-1 text-[13px] font-semibold text-ink-2">
            {CATEGORY_LABEL[g.category]}
            <span className="num text-[11px] font-normal text-ink-3">{g.entities.length}</span>
          </h3>
          <div className="grid items-start gap-4 md:grid-cols-2">
            {g.entities.map((e) => (
              <EntityPanel key={e.entity_id || e.unique_key} entity={e} idx={idx} />
            ))}
          </div>
        </section>
      ))}
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