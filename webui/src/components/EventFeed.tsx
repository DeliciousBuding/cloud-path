import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import { ChevronRight } from 'lucide-react'
import type { EventView } from '@/lib/types'
import { Badge } from './ui'
import { useCapabilityIndex } from '@/hooks/useDescriptor'
import { useDevices } from '@/hooks/useDevices'
import { cn } from '@/lib/cn'
import {
  cmdMeta, eventLabel, eventTone, fmtDay, fmtDateTime, fmtTime, isDirtyLabel, payloadHasMore, payloadLabel,
} from '@/lib/format'
import { eventDecl, humanize } from '@/lib/descriptor'
import type { CapabilityIndex, CommandAction } from '@/lib/descriptor'
import { i18n } from '@/i18n'


/** 机器名规范化：只用于展示名查词典，原始值仍原样放在 title/技术详情。 */
function machineNameKeys(value: string): string[] {
  const normalized = value
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, ' ')
    .trim()
  const tail = value.split(/[/#]/).pop() ?? value
  const tailKey = tail
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, ' ')
    .trim()
  // 命名空间事件（如 stcb.sensor）的最后一段常是稳定语义词；
  // 保留完整键，同时允许通用词典命中 sensor / led / buzzer 等尾段。
  const parts = normalized.split(' ').filter(Boolean)
  return [...new Set([normalized, tailKey, ...parts.slice(1), ...parts])]
}

function translate(key: string, options?: Record<string, unknown>): string {
  return String(i18n.t(key, { ns: 'activity', ...options }))
}

function isMachineName(value: string): boolean {
  const text = value.trim()
  if (!text) return true
  if (/\s/.test(text)) return false
  return /^[a-z0-9._:/-]+$/i.test(text)
}

function displayableText(value?: string): string | undefined {
  const text = value?.trim()
  return text && !isMachineName(text) && !isDirtyLabel(text) ? text : undefined
}

/** 通用事件展示词典：只覆盖跨设备、低歧义的机器名；未知值继续 humanize。 */
const EVENT_LABEL_KEYS: Record<string, string> = {
  'device boot': 'event.labels.deviceBooted',
  'device booted': 'event.labels.deviceBooted',
  'device online': 'event.labels.deviceOnline',
  sensor: 'event.labels.sensor',
  led: 'event.labels.led',
  buzzer: 'event.labels.buzzer',
  motor: 'event.labels.motor',
  'device offline': 'event.labels.deviceOffline',
  'edge online': 'event.labels.edgeOnline',
  'edge offline': 'event.labels.edgeOffline',
  'device state': 'event.labels.deviceState',
  'device state changed': 'event.labels.deviceStateChanged',
  'state changed': 'event.labels.stateChanged',
  'device descriptor': 'event.labels.deviceDescriptor',
  'device compartment opened': 'event.labels.compartmentOpened',
  'device compartment closed': 'event.labels.compartmentClosed',
  'pillbox remind': 'event.labels.pillboxRemind',
  'pillbox reminded': 'event.labels.pillboxRemind',
  'pillbox missed': 'event.labels.pillboxMissed',
  'pillbox taken': 'event.labels.pillboxTaken',
  'read sample': 'event.labels.readSample',
  'read samples': 'event.labels.readSample',
  'read register': 'event.labels.readRegister',
  'register read': 'event.labels.readRegister',
  'write register': 'event.labels.writeRegister',
  'register write': 'event.labels.writeRegister',
  'door opened': 'event.labels.doorOpened',
  'door closed': 'event.labels.doorClosed',
  opened: 'event.labels.opened',
  closed: 'event.labels.closed',
  'temperature high': 'event.labels.temperatureHigh',
  'air quality bad': 'event.labels.airQualityBad',
  'operation failed': 'event.labels.operationFailed',
  probed: 'event.labels.probed',
  'setpoint changed': 'event.labels.setpointChanged',
  'setpoint-changed': 'event.labels.setpointChanged',
  toggled: 'event.labels.toggled',
  'command completed': 'event.labels.commandCompleted',
}

/** 通用操作展示词典：声明 title 缺席时优先于 humanize，未知操作仍回落 humanize。 */
const COMMAND_LABEL_KEYS: Record<string, string> = {
  'read register': 'event.commandLabels.readRegister',
  'register read': 'event.commandLabels.readRegister',
  'write register': 'event.commandLabels.writeRegister',
  'register write': 'event.commandLabels.writeRegister',
  'read registers': 'event.commandLabels.readRegister',
  'get status': 'event.commandLabels.getStatus',
  'pillbox remind': 'event.commandLabels.pillboxRemind',
  tone: 'event.commandLabels.tone',
  'tone sequence': 'event.commandLabels.toneSequence',
  tone_sequence: 'event.commandLabels.toneSequence',
  isp: 'event.commandLabels.isp',
}

/** 事件主标签：声明 title → 后端标签 → 平台/通用词典 → 兜底；机器名只放 title/技术详情。 */
export function eventDisplayLabel(type: string, index?: CapabilityIndex, label?: string): string {
  if (isDirtyLabel(type)) return translate('event.labels.invalid')
  const declared = index ? eventDecl(type, index)?.title : undefined
  if (declared) return declared
  const backendLabel = displayableText(label)
  if (backendLabel) return backendLabel
  for (const key of machineNameKeys(type)) {
    if (EVENT_LABEL_KEYS[key]) return translate(EVENT_LABEL_KEYS[key])
  }
  const base = eventLabel(type, index)
  for (const key of machineNameKeys(base)) {
    if (EVENT_LABEL_KEYS[key]) return translate(EVENT_LABEL_KEYS[key])
  }
  if (base !== humanize(type)) {
    const readable = displayableText(base)
    if (readable) return readable
  }
  return translate('event.unknownEvent')
}

/** 操作主标签：声明/平台词典 → 通用词典 → 兜底；原始 cmd 只由调用方放进 title。 */
export function commandStatusLabel(status: string): string {
  return translate(`command.status.${status}`, { defaultValue: translate('command.statusUnknown') })
}

export function commandDisplayMeta(cmd: string, index?: CapabilityIndex, actions?: CommandAction[]): { label: string; hint: string } {
  const meta = cmdMeta(cmd, actions, index)
  for (const key of machineNameKeys(cmd)) {
    if (COMMAND_LABEL_KEYS[key]) return { ...meta, label: translate(COMMAND_LABEL_KEYS[key]) }
  }
  if (meta.label !== humanize(cmd)) {
    const readable = displayableText(meta.label)
    if (readable) return { ...meta, label: readable }
  }
  for (const key of machineNameKeys(meta.label)) {
    if (COMMAND_LABEL_KEYS[key]) return { ...meta, label: translate(COMMAND_LABEL_KEYS[key]) }
  }
  return { ...meta, label: translate('event.unknownCommand') }
}

/** 事件载荷里的英文状态/错误摘要：只翻译低歧义的通用词，其余不进入主路径。 */
const PAYLOAD_SUMMARY_KEYS: Record<string, string> = {
  'network timeout': 'event.payload.networkTimeout',
  'connection timeout': 'event.payload.connectionTimeout',
  'connection refused': 'event.payload.connectionRefused',
  'device offline': 'event.payload.deviceOffline',
  'operation failed': 'event.payload.operationFailed',
  'request failed': 'event.payload.requestFailed',
}

function payloadSummary(raw: string | undefined): string | undefined {
  if (!raw) return undefined
  for (const key of machineNameKeys(raw)) {
    if (PAYLOAD_SUMMARY_KEYS[key]) return translate(PAYLOAD_SUMMARY_KEYS[key])
  }
  return displayableText(raw)
}

/** 操作失败原因 → 人话 + 下一步。只依赖稳定状态/错误码，不解析中文文本。 */
function failureSignal(result?: string): string {
  const text = result?.trim()
  if (!text) return ''
  const parts = [text]
  try {
    const parsed = JSON.parse(text) as unknown
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      const record = parsed as Record<string, unknown>
      for (const key of ['code', 'error', 'reason', 'status', 'message']) {
        const value = record[key]
        if (typeof value === 'string' && value.trim()) parts.push(value.trim())
      }
    }
  } catch { /* 原文仍可用于稳定机器标记匹配 */ }
  return parts.join(' ').toLowerCase()
}

function includesAny(text: string, tokens: string[]): boolean {
  return tokens.some((token) => text.includes(token))
}

export function commandFailureInfo(result?: string, status?: string): { message: string; next: string } {
  const text = failureSignal(result)
  if (status === 'timeout' || includesAny(text, ['timeout', 'timed out', 'deadline', 'etimedout'])) {
    return { message: translate('failure.timeoutMessage'), next: translate('failure.timeoutNext') }
  }
  if (includesAny(text, ['busy', 'queue full', 'queue_full', 'err_busy', 'resource_exhausted'])) {
    return { message: translate('failure.busyMessage'), next: translate('failure.busyNext') }
  }
  if (includesAny(text, ['offline', 'unavailable', 'edge_offline', 'device_offline'])) {
    return { message: translate('failure.offlineMessage'), next: translate('failure.offlineNext') }
  }
  if (includesAny(text, ['permission', 'forbidden', 'unauthorized', 'permission_denied'])) {
    return { message: translate('failure.permissionMessage'), next: translate('failure.permissionNext') }
  }
  if (includesAny(text, ['unsupported', 'not supported', 'invalid', 'bad request', 'bad_request', 'err_invalid'])) {
    return { message: translate('failure.unsupportedMessage'), next: translate('failure.unsupportedNext') }
  }
  if (!result?.trim()) return { message: translate('failure.genericMessage'), next: translate('failure.retryNext') }
  return { message: translate('failure.genericMessage'), next: translate('failure.retryDetailNext') }
}

/**
 * 事件流（新→旧）。来源可为 WS 实时环形缓冲、REST 历史，或两者合并结果。
 * 事件类型属于 Capability/Application 命名空间：标签优先取后端给的 label，
 * 其次 Capability 声明的 title，最后 humanize(类型名)——前端不维护事件枚举。
 * 单行高密度：类型 / 对象 / 载荷展开 / 时刻一行放下；原始载荷按需展开（取证面，不污染扫读），
 * 且只在载荷确有增量信息时才给展开入口——见 payloadHasMore。
 */
export function EventFeed({ events, showDevice = true, limit = 30, dayGrouped = false }: {
  events: EventView[]
  showDevice?: boolean
  limit?: number
  /** 长历史模式：按天分组，组头承载日期、行内只留时刻（完整时间悬停可见）；紧凑列表不分组 */
  dayGrouped?: boolean
}) {
  const { t } = useTranslation('activity')
  // 设备列展示人话名字（机器 ID 收进 title）：与操作历史同一纪律
  const { list: devices } = useDevices()
  const names = useMemo(() => new Map(
    devices.filter((d) => d.name).map((d) => [d.id, d.name as string]),
  ), [devices])

  if (!events.length) {
    return <p className="py-6 text-center text-body text-ink-3">{t('event.empty')}</p>
  }
  const shown = events.slice(0, limit)
  if (!dayGrouped) {
    return (
      <ul className="divide-y divide-hairline">
        {shown.map((e, i) => (
          <EventRow key={`${e.id}-${i}`} e={e} first={i === 0} showDevice={showDevice} name={names.get(e.device_id)} />
        ))}
      </ul>
    )
  }
  // 跨天历史按天分组：组头是扫读锚点（今天/昨天/日期），组内仍是单行高密度时间线
  const groups: { day: string; items: EventView[] }[] = []
  for (const e of shown) {
    const day = fmtDay(e.ts)
    const last = groups[groups.length - 1]
    if (last && last.day === day) last.items.push(e)
    else groups.push({ day, items: [e] })
  }
  return (
    <div className="space-y-4">
      {groups.map((g, gi) => (
        <section key={`${g.day}-${gi}`}>
          <h4 className="sticky top-0 z-local -my-1 bg-surface py-1 px-0.5 text-meta font-medium text-ink-3">{g.day}</h4>
          <ul className="divide-y divide-hairline">
            {g.items.map((e, i) => (
              <EventRow key={`${e.id}-${gi}-${i}`} e={e} first={gi === 0 && i === 0} showDevice={showDevice} name={names.get(e.device_id)} />
            ))}
          </ul>
        </section>
      ))}
    </div>
  )
}

function EventRow({ e, first, showDevice, name }: {
  e: EventView; first: boolean; showDevice: boolean; name?: string
}) {
  const { t } = useTranslation('activity')
  const index = useCapabilityIndex()
  const [open, setOpen] = useState(false)
  const label = eventDisplayLabel(e.type, index)
  const tone = eventTone(e.type, index)
  const [edgeId, devId] = e.device_id.split('/')
  // 只有载荷相对行内已有内容还有增量时才给展开入口：{"type":"device-booted"} 这种
  // 展开就是它自己，给按钮等于骗点击（真实数据里 9/9 行都是这个形状）。
  const hasPayload = payloadHasMore(e.payload)
  // 行内摘要只取载荷里的人话字段（label/message/reason/text）：机器 key 不进默认视图
  const rawSummary = payloadSummary(payloadLabel(e.payload))
  const typeKeys = new Set(machineNameKeys(e.type))
  const summary = rawSummary && machineNameKeys(rawSummary).some((key) => typeKeys.has(key))
    ? undefined
    : rawSummary
  return (
    <li className={first ? 'fade-up' : undefined}>
      <div className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 py-2 lg:grid-cols-[auto_minmax(8rem,0.45fr)_minmax(0,1fr)_auto_auto]">
        <span className="min-w-0 max-w-[9rem] truncate lg:col-start-1" title={t('event.rawType', { type: e.type })}>
          <Badge tone={tone} className="max-w-full truncate">{label}</Badge>
        </span>
        {showDevice && (
          <Link
            to={`/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`}
            className={cn('flex min-h-touch min-w-0 max-w-[10rem] items-center text-meta text-ink-3 transition-colors hover:text-accent sm:min-h-0 lg:col-start-2',
              !name && 'num font-mono')}
            title={t('event.viewDeviceTitle', { id: e.device_id })}
          >
            <span className="min-w-0 truncate">{name || devId}</span>
          </Link>
        )}
        {summary && (
          <span className="col-span-3 row-start-2 min-w-0 truncate text-meta text-ink-2 lg:col-span-1 lg:col-start-3 lg:row-start-1"
            title={e.payload || undefined}>{summary}</span>
        )}
        <div className="col-start-3 row-start-1 flex shrink-0 items-center gap-2 justify-self-end lg:col-span-2 lg:col-start-4">
          {hasPayload && (
            <button
              type="button"
              onClick={() => setOpen((v) => !v)}
              aria-expanded={open}
              aria-label={open ? t('event.collapse') : t('event.expand')}
              className="flex h-touch w-touch shrink-0 items-center justify-center text-ink-3 transition-colors hover:text-ink-2 sm:h-8 sm:w-8">
              <ChevronRight size={12} className={open ? 'rotate-90 transition-transform' : 'transition-transform'} />
            </button>
          )}
          <span className="num shrink-0 font-mono text-micro text-ink-3" title={`${fmtDateTime(e.ts)} · ${e.type}`}>
            {fmtTime(e.ts)}
          </span>
        </div>
      </div>
      {open && (
        <pre tabIndex={0} role="group" aria-label={t('event.detailsData')}
          className="num mb-2 max-h-40 overflow-auto rounded-tile bg-surface-2 p-2 font-mono text-micro leading-relaxed text-ink-2">
          {e.payload}
        </pre>
      )}
    </li>
  )
}
