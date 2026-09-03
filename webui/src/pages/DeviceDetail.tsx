import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import {
  ArrowLeft, BellRing, Braces, CalendarClock, Clock3, Command, DoorOpen, Gauge,
  Grid3x3, History, RadioTower, Terminal, Usb,
} from 'lucide-react'
import { Badge, EmptyState, KeyValue, Panel, StatusDot, type Tone } from '@/components/ui'
import { SlotChips, SlotLegend } from '@/components/SlotChips'
import { EventFeed } from '@/components/EventFeed'
import { DriftChart } from '@/components/DriftChart'
import { CommandButton } from '@/components/CommandButton'
import { CommandHistory } from '@/components/CommandHistory'
import { RowSkeleton } from '@/components/Skeleton'
import { api } from '@/lib/api'
import { useLive } from '@/store/ws'
import { useNow } from '@/hooks/useNow'
import { driftTone, fmtClock, fmtDrift, mergeEvents, timeAgo } from '@/lib/format'
import { cn } from '@/lib/cn'

function stateTone(raw: number | undefined): Tone {
  if (raw === 1) return 'warn'
  if (raw === 2) return 'bad'
  return 'idle'
}

const DRIFT_TONE_CLS: Record<Tone, string> = {
  ok: 'text-ok', warn: 'text-warn', bad: 'text-bad', accent: 'text-accent', idle: 'text-ink-3',
}

/** 命令展示元数据：白名单由后端 /api/adapters 决定，这里只负责外观 */
const CMD_UI: Record<string, {
  label: string; icon: typeof Clock3; variant?: 'primary' | 'ghost' | 'danger'
  okText: string; confirmText?: string; needArgs?: boolean
}> = {
  sync:    { label: '对时',     icon: CalendarClock, variant: 'primary', okText: '对时成功' },
  dump:    { label: '读取状态', icon: Terminal,      okText: '已请求状态转储' },
  trigger: { label: '触发提醒', icon: BellRing,      okText: '已触发提醒' },
  open:    { label: '模拟确认', icon: DoorOpen,      okText: '已模拟确认动作' },
  isp:     {
    label: '进入刷机模式', icon: Usb, variant: 'danger', okText: 'ISP 指令已发送，设备稍后复位',
    confirmText: '确认让设备进入 ISP 刷机模式？设备会离线，直到重新烧录固件。',
  },
  raw:     { label: '发送原始指令', icon: Braces, okText: '原始指令已写入串口', needArgs: true },
}

const FALLBACK_CMDS = ['sync', 'dump']

export default function DeviceDetail() {
  const { edgeId = '', deviceId = '' } = useParams()
  const key = `${decodeURIComponent(edgeId)}/${decodeURIComponent(deviceId)}`
  const now = useNow()
  const [rawArgs, setRawArgs] = useState('')

  const live = useLive((s) => s.devices[key])
  const liveEvents = useLive((s) => s.events)
  const driftPoints = useLive((s) => s.drift[key]) ?? []

  // 详情兜底（WS 未覆盖/刚重启）、事件历史与适配器命令白名单
  const { data: rest } = useQuery({
    queryKey: ['device', key], queryFn: () => api.device(edgeId, deviceId), refetchInterval: 10000,
  })
  const { data: evHist, isLoading: evLoading } = useQuery({
    queryKey: ['device-events', key], queryFn: () => api.events({ device: key, limit: 100 }),
    refetchInterval: 5000,
  })
  const { data: adapters } = useQuery({
    queryKey: ['adapters'], queryFn: api.adapters, staleTime: 5 * 60_000,
  })

  const d = live ?? rest
  const events = useMemo(
    () => mergeEvents(liveEvents.filter((e) => e.device_id === key), evHist?.events ?? []),
    [liveEvents, evHist, key],
  )

  const supported = useMemo(() => {
    const a = adapters?.adapters.find((x) => x.name === d?.adapter)
    return a?.commands?.length ? a.commands : FALLBACK_CMDS
  }, [adapters, d?.adapter])

  if (!d) {
    return (
      <>
        <Link to="/devices" className="link mb-5 inline-flex items-center gap-1 text-sm">
          <ArrowLeft size={15} /> 设备
        </Link>
        <EmptyState icon={<RadioTower size={24} />} title="设备未注册"
          hint={`没有找到 ${key}。设备接入后会自动注册；请检查 edge 配置与串口连接。`} />
      </>
    )
  }

  const raw = d.state ?? {}
  const drift = typeof raw.drift_min === 'number' ? raw.drift_min : null
  const slots = Array.isArray(raw.slots) ? raw.slots : undefined

  return (
    <>
      <Link to="/devices"
        className="mb-5 inline-flex items-center gap-1 text-sm text-ink-2 transition-colors hover:text-accent fade-up">
        <ArrowLeft size={15} /> 设备
      </Link>

      <header className="mb-7 flex flex-wrap items-center gap-3 fade-up">
        <StatusDot online={d.online} />
        <h1 className="text-[26px] font-bold tracking-tight">{d.name || deviceId}</h1>
        <Badge tone="accent">{d.adapter || '未知适配器'}</Badge>
        {d.port && <Badge tone="idle"><Terminal size={11} />{d.port}</Badge>}
        <Badge tone="idle"><RadioTower size={11} />{d.edge_id}</Badge>
        <span className="num ml-auto text-xs text-ink-3" title={`设备键 ${d.id}`}>
          {d.online ? `更新于 ${timeAgo(d.updated_at)}` : `最后见 ${timeAgo(d.last_seen)}`}
        </span>
      </header>

      <div className="grid items-start gap-5 lg:grid-cols-3">
        {/* 时钟 */}
        <Panel title={<span className="flex items-center gap-1.5"><Clock3 size={14} />设备时钟</span>}
          className="lg:col-span-2">
          <div className="flex flex-wrap items-end justify-between gap-4">
            <div className={cn('num text-6xl font-semibold leading-none tracking-tight',
              !d.online && 'text-ink-3', raw.state === 1 && 'remind')}>
              {fmtClock(raw.clock as string | undefined)}
            </div>
            <div className="text-right">
              <Badge tone={stateTone(raw.state as number | undefined)}>
                {(raw.state_label as string) ?? (d.online ? '等待转储' : '离线')}
              </Badge>
              <p className="num mt-2 text-xs text-ink-3" title="浏览器所在时区的参考时间">
                参考时间 {now.toLocaleTimeString('zh-CN', { hour12: false })}
              </p>
            </div>
          </div>
          <div className="mt-5 grid grid-cols-3 gap-3 border-t border-hairline pt-4 text-center">
            <div>
              <p className="text-[11px] text-ink-3">漂移</p>
              <p className={cn('num mt-0.5 text-lg font-semibold', DRIFT_TONE_CLS[driftTone(drift)])}
                title="设备时钟与参考时间的偏差（分钟）">
                {fmtDrift(drift)}
              </p>
            </div>
            <div>
              <p className="text-[11px] text-ink-3">时</p>
              <p className="num mt-0.5 text-lg font-semibold">{raw.hour ?? '--'}</p>
            </div>
            <div>
              <p className="text-[11px] text-ink-3">分</p>
              <p className="num mt-0.5 text-lg font-semibold">{raw.min ?? '--'}</p>
            </div>
          </div>
        </Panel>

        {/* 命令面板：按钮集合由适配器白名单驱动 */}
        <Panel title={<span className="flex items-center gap-1.5"><Command size={14} />命令</span>}
          right={<span className="text-[11px] text-ink-3">{d.adapter || '—'}</span>}>
          <div className="grid grid-cols-2 gap-2">
            {supported.map((c) => {
              const ui = CMD_UI[c]
              if (!ui || ui.needArgs) return null
              const Icon = ui.icon
              return (
                <CommandButton key={c} deviceId={key} cmd={c} label={ui.label}
                  icon={<Icon size={14} />} variant={ui.variant}
                  confirmText={ui.confirmText} okText={ui.okText}
                  title={ui.label}
                  className={c === 'isp' ? 'col-span-2' : undefined} />
              )
            })}
          </div>
          {supported.includes('raw') && (
            <div className="mt-3 border-t border-hairline pt-3">
              <label className="mb-1.5 block text-[11px] text-ink-3" htmlFor="raw-args">
                原始指令（按原样写入串口，长度 ≤64，不含换行）
              </label>
              <div className="flex gap-2">
                <input id="raw-args" value={rawArgs} maxLength={64}
                  onChange={(e) => setRawArgs(e.target.value.replace(/[\r\n\0]/g, ''))}
                  placeholder="例如 S"
                  className="num min-w-0 flex-1 rounded-full border border-hairline bg-surface-2 px-3.5 py-1.5 font-mono text-xs outline-none transition-colors focus:border-accent" />
                <CommandButton deviceId={key} cmd="raw" args={rawArgs} label="发送"
                  icon={<Braces size={14} />} okText="原始指令已发送"
                  className="shrink-0" title="发送原始指令" />
              </div>
            </div>
          )}
          <p className="mt-3 text-[11px] leading-relaxed text-ink-3">
            命令经 server → 边缘节点 → 串口下发，回执通过实时通道返回并落库。
          </p>
        </Panel>

        {/* 槽位 */}
        <Panel title={<span className="flex items-center gap-1.5"><Grid3x3 size={14} />槽位状态</span>}>
          {slots ? (
            <>
              <SlotChips slots={slots} />
              <SlotLegend />
            </>
          ) : (
            <p className="py-4 text-center text-sm text-ink-3">等待设备转储…</p>
          )}
        </Panel>

        {/* 漂移趋势 */}
        <Panel title={<span className="flex items-center gap-1.5"><Gauge size={14} />漂移趋势</span>}
          right={<span className="text-[11px] text-ink-3">本次会话 {driftPoints.length} 点</span>}>
          <DriftChart points={driftPoints} />
        </Panel>

        {/* 事件时间线 */}
        <Panel title={<span className="flex items-center gap-1.5"><History size={14} />事件时间线</span>}
          className="lg:row-span-2">
          <div className="max-h-[420px] overflow-y-auto pr-1">
            {evLoading && events.length === 0
              ? <RowSkeleton rows={5} />
              : <EventFeed events={events} showDevice={false} limit={60} />}
          </div>
        </Panel>

        {/* 命令历史 */}
        <div className="lg:col-span-2">
          <CommandHistory deviceId={key} />
        </div>

        {/* 原始状态（取证用） */}
        <Panel className="lg:col-span-3"
          title={<span className="flex items-center gap-1.5"><Braces size={14} />原始状态</span>}
          right={<span className="num text-[11px] text-ink-3">
            {typeof raw.dump_raw === 'string' ? raw.dump_raw : '无转储行'}
          </span>}>
          <div className="grid gap-5 md:grid-cols-2">
            <dl className="space-y-2.5">
              <KeyValue k="设备键" v={d.id} mono />
              <KeyValue k="边缘节点" v={d.edge_id || '—'} mono />
              <KeyValue k="适配器" v={d.adapter || '—'} mono />
              <KeyValue k="串口" v={d.port || '—'} mono />
              <KeyValue k="在线" v={d.online ? '是' : '否'} />
              <KeyValue k="最后更新" v={d.updated_at ? timeAgo(d.updated_at) : '—'} />
            </dl>
            <pre className="num max-h-56 overflow-auto rounded-xl bg-surface-2 p-3 font-mono text-[11px] leading-relaxed text-ink-2"
              title="状态 JSON（适配器上报的原始语义）">
              {JSON.stringify(d.state ?? {}, null, 2)}
            </pre>
          </div>
        </Panel>
      </div>
    </>
  )
}
