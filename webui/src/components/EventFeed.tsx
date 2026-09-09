import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { ChevronRight } from 'lucide-react'
import type { EventView } from '@/lib/types'
import { Badge } from './ui'
import { useCapabilityIndex } from '@/hooks/useDescriptor'
import { useDevices } from '@/hooks/useDevices'
import { cn } from '@/lib/cn'
import { cmdMeta, eventLabel, eventTone, fmtDay, fmtDateTime, fmtTime, payloadHasMore, payloadLabel } from '@/lib/format'
import { eventDecl } from '@/lib/descriptor'
import type { CapabilityIndex } from '@/lib/descriptor'

const CJK_RE = /[\u3400-\u9fff]/

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

/** 通用事件展示词典：只覆盖跨设备、低歧义的机器名；未知值继续 humanize。 */
const EVENT_LABEL_FALLBACK: Record<string, string> = {
  'device booted': '设备启动',
  'device online': '设备上线',
  sensor: '传感器状态',
  led: '指示灯状态',
  buzzer: '蜂鸣器状态',
  motor: '电机状态',
  'device offline': '设备离线',
  'edge online': '网关上线',
  'edge offline': '网关离线',
  'device state changed': '状态变化',
  'state changed': '状态变化',
  'device compartment opened': '设备舱门已打开',
  'device compartment closed': '设备舱门已关闭',
  'pillbox remind': '药盒提醒',
  'pillbox reminded': '药盒提醒',
  'pillbox missed': '错过服药',
  'pillbox taken': '已取药',
  'read sample': '读取采样值',
  'read samples': '读取采样值',
  'read register': '读取寄存器',
  'register read': '读取寄存器',
  'write register': '写入寄存器',
  'register write': '写入寄存器',
  'door opened': '门窗已打开',
  'door closed': '门窗已关闭',
  opened: '已打开',
  closed: '已关闭',
  'temperature high': '温度过高',
  'air quality bad': '空气质量异常',
  'operation failed': '操作失败',
  probed: '探测完成',
  'setpoint changed': '设定值已更新',
  'setpoint-changed': '设定值已更新',
  toggled: '开关状态已切换',
  'command completed': '操作已完成',
}

/** 通用操作展示词典：声明 title 缺席时优先于 humanize，未知操作仍回落 humanize。 */
const COMMAND_LABEL_FALLBACK: Record<string, string> = {
  'read register': '读取寄存器',
  'register read': '读取寄存器',
  'write register': '写入寄存器',
  'register write': '写入寄存器',
  'read registers': '读取寄存器',
  'get status': '读取状态',
  'pillbox remind': '触发药盒提醒',
}

/** 事件主标签：中文声明 title → 中文后端标签 → 平台/通用词典 → 中文兜底；不采信英文机器标签。 */
export function eventDisplayLabel(type: string, index?: CapabilityIndex, label?: string): string {
  const declared = index ? eventDecl(type, index)?.title : undefined
  if (declared) return declared
  if (label && CJK_RE.test(label)) return label
  const base = eventLabel(type, index)
  for (const key of machineNameKeys(type)) {
    if (EVENT_LABEL_FALLBACK[key]) return EVENT_LABEL_FALLBACK[key]
  }
  if (CJK_RE.test(base)) return base
  for (const key of machineNameKeys(base)) {
    if (EVENT_LABEL_FALLBACK[key]) return EVENT_LABEL_FALLBACK[key]
  }
  return CJK_RE.test(base) ? base : '未知事件'
}

/** 操作主标签：声明/平台词典 → 通用词典 → 中文兜底；原始 cmd 只由调用方放进 title。 */
export function commandDisplayMeta(cmd: string, index?: CapabilityIndex): { label: string; hint: string } {
  const meta = cmdMeta(cmd, undefined, index)
  if (CJK_RE.test(meta.label)) return meta
  for (const key of machineNameKeys(cmd)) {
    if (COMMAND_LABEL_FALLBACK[key]) return { ...meta, label: COMMAND_LABEL_FALLBACK[key] }
  }
  for (const key of machineNameKeys(meta.label)) {
    if (COMMAND_LABEL_FALLBACK[key]) return { ...meta, label: COMMAND_LABEL_FALLBACK[key] }
  }
  return { ...meta, label: '未知操作' }
}

/** 事件载荷里的英文状态/错误摘要：只翻译低歧义的通用词，其余不进入主路径。 */
const PAYLOAD_SUMMARY_FALLBACK: Record<string, string> = {
  'network timeout': '网络连接超时',
  'connection timeout': '连接超时',
  'connection refused': '连接被拒绝',
  'device offline': '设备离线',
  'operation failed': '操作失败',
  'request failed': '请求失败',
}

function payloadSummary(raw: string | undefined): string | undefined {
  if (!raw) return undefined
  if (CJK_RE.test(raw)) return raw
  for (const key of machineNameKeys(raw)) {
    if (PAYLOAD_SUMMARY_FALLBACK[key]) return PAYLOAD_SUMMARY_FALLBACK[key]
  }
  return undefined
}

/** 操作失败原因 → 人话 + 下一步。文案与设备详情「操作记录」保持同源，原始 result 只放 title。 */
export function commandFailureInfo(result?: string): { message: string; next: string } {
  const text = result?.trim()
  if (!text) return { message: '设备没有完成操作', next: '请稍后重试' }
  if (/timeout|timed out|超时/i.test(text)) return { message: '设备响应超时', next: '确认设备在线且空闲后重试' }
  if (/busy|queue full|忙/i.test(text)) return { message: '设备正忙', next: '等待设备空闲后重试' }
  if (/offline|离线/i.test(text)) return { message: '设备当前离线', next: '确认设备恢复在线后重试' }
  if (/permission|forbidden|unauthorized|权限/i.test(text)) return { message: '当前账号没有操作权限', next: '请联系管理员授权后重试' }
  if (/unsupported|not supported|invalid|参数无效/i.test(text)) return { message: '设备不支持此操作或参数无效', next: '检查参数后重试' }
  if (/^[\u3400-\u9fff\s，。！？、；：（）\-—]+$/.test(text)) return { message: text, next: '请根据提示检查后重试' }
  return { message: '设备没有完成操作', next: '请稍后重试；如果持续失败，请查看技术详情' }
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
  // 设备列展示人话名字（机器 ID 收进 title）：与操作历史同一纪律
  const { list: devices } = useDevices()
  const names = useMemo(() => new Map(
    devices.filter((d) => d.name).map((d) => [d.id, d.name as string]),
  ), [devices])

  if (!events.length) {
    return <p className="py-6 text-center text-sm text-ink-3">暂无事件。设备上报事件或操作结果会显示在这里。</p>
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
          <h4 className="sticky top-0 z-10 -my-1 bg-surface py-1 px-0.5 text-[12px] font-medium text-ink-3">{g.day}</h4>
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
        <span className="min-w-0 max-w-[9rem] truncate lg:col-start-1" title={`原始类型：${e.type}`}>
          <Badge tone={tone} className="max-w-full truncate">{label}</Badge>
        </span>
        {showDevice && (
          <Link
            to={`/devices/${encodeURIComponent(edgeId ?? '')}/${encodeURIComponent(devId ?? '')}`}
            className={cn('flex min-h-11 min-w-0 max-w-[10rem] items-center text-[12px] text-ink-3 transition-colors hover:text-accent sm:min-h-0 lg:col-start-2',
              !name && 'num font-mono')}
            title={`${e.device_id} · 查看设备`}
          >
            <span className="min-w-0 truncate">{name || devId}</span>
          </Link>
        )}
        {summary && (
          <span className="col-span-3 row-start-2 min-w-0 truncate text-[12px] text-ink-2 lg:col-span-1 lg:col-start-3 lg:row-start-1"
            title={e.payload || undefined}>{summary}</span>
        )}
        <div className="col-start-3 row-start-1 flex shrink-0 items-center gap-2 justify-self-end lg:col-span-2 lg:col-start-4">
          {hasPayload && (
            <button
              type="button"
              onClick={() => setOpen((v) => !v)}
              aria-expanded={open}
              aria-label={open ? '收起事件详情' : '查看事件详情'}
              className="flex h-11 w-11 shrink-0 items-center justify-center text-ink-3 transition-colors hover:text-ink-2 sm:h-8 sm:w-8">
              <ChevronRight size={12} className={open ? 'rotate-90 transition-transform' : 'transition-transform'} />
            </button>
          )}
          <span className="num shrink-0 font-mono text-[11px] text-ink-3" title={`${fmtDateTime(e.ts)} · ${e.type}`}>
            {fmtTime(e.ts)}
          </span>
        </div>
      </div>
      {open && (
        <pre tabIndex={0} role="group" aria-label="事件详情数据"
          className="num mb-2 max-h-40 overflow-auto rounded-lg bg-surface-2 p-2 font-mono text-[11px] leading-relaxed text-ink-2">
          {e.payload}
        </pre>
      )}
    </li>
  )
}
